// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

// Package startuphook serves the Java extensions to the startup script of the DX Compose Web Engine (DXJavaExtension
// delivery "startupHook", issue #72). The script, written by the operator on a volume mounted on the HCL customer
// startup folder, calls GET /dx-java-extensions/v1/bundle?namespace=…&statefulset=…&pod=… with the per-Web-Engine
// token on every container start; the operator pulls the extensions from the registry (with the pull secrets of the
// namespace, so the credentials never reach the DX container) and answers a tar.gz, or 204 when there is nothing.
package startuphook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/javaext"
	"github.com/europesip/dx-delivery-operator/internal/webhook"
)

// KeySecretKey: the data key of the operator's startup-hook Secret.
const KeySecretKey = "key"

// EnsureKey reads the HMAC key of the startup tokens from a Secret of the operator namespace, creating it the first
// time (random 32 bytes). Every operator replica and restart uses the same key, so the tokens on the volumes stay
// valid.
func EnsureKey(ctx context.Context, c client.Client, rd client.Reader, namespace, name string) ([]byte, error) {
	var sec corev1.Secret
	err := rd.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &sec)
	if err == nil {
		if k := sec.Data[KeySecretKey]; len(k) >= 16 {
			return k, nil
		}
		return nil, fmt.Errorf("secret %s/%s has no %q of at least 16 bytes", namespace, name, KeySecretKey)
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	key := []byte(hex.EncodeToString(b))
	sec = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "dx-delivery-operator"}},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{KeySecretKey: key},
	}
	if err := c.Create(ctx, &sec); err != nil {
		if apierrors.IsAlreadyExists(err) { // another replica won the race
			return EnsureKey(ctx, c, rd, namespace, name)
		}
		return nil, err
	}
	return key, nil
}

// Server is the HTTP endpoint (a manager Runnable; every replica serves it, it does not need the leader).
type Server struct {
	Addr   string
	Key    []byte
	Reader client.Reader // without cache: the extensions right after a status change
	Client client.Client // to annotate the pod
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: every replica serves.
func (s *Server) NeedLeaderElection() bool { return false }

// Start implements manager.Runnable.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle(javaext.StartupHookPath, s)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{Addr: s.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg + "\n"))
}

// ServeHTTP answers the bundle of one Web Engine.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := logf.Log.WithName("java-extensions-startup-hook")
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	ns, sts, pod := r.URL.Query().Get("namespace"), r.URL.Query().Get("statefulset"), r.URL.Query().Get("pod")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ns == "" || sts == "" || !javaext.ValidStartupToken(s.Key, ns, sts, token) {
		log.Info("rejected request", "namespace", ns, "statefulSet", sts, "remote", r.RemoteAddr)
		httpError(w, http.StatusUnauthorized, "invalid token for this Web Engine")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	// Both modes on one Web Engine never deliver twice: the webhook label wins and the script stops the start.
	var nsObj corev1.Namespace
	if err := s.Reader.Get(ctx, client.ObjectKey{Name: ns}, &nsObj); err != nil {
		httpError(w, http.StatusServiceUnavailable, "cannot read the namespace: "+err.Error())
		return
	}
	if nsObj.Labels[dxv1.LabelJavaExtensionsNamespace] == "enabled" {
		httpError(w, http.StatusConflict, fmt.Sprintf("namespace %s has the label %s=enabled (webhook delivery): the startup hook and the webhook cannot be used together on one portal", ns, dxv1.LabelJavaExtensionsNamespace))
		return
	}
	entries, _, err := webhook.EntriesForMode(ctx, s.Reader, ns, sts, dxv1.DeliveryStartupHook)
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "cannot read the DXJavaExtensions: "+err.Error())
		return
	}
	if len(entries) == 0 {
		s.annotate(ctx, ns, sts, pod, "")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	start := time.Now()
	res, err := javaext.BuildBundle(ctx, entries, func(e javaext.Entry) (authn.Keychain, error) {
		cfgs := map[string][]byte{}
		for _, name := range e.PullSecrets {
			var sec corev1.Secret
			if err := s.Reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &sec); err != nil {
				return nil, fmt.Errorf("pull secret %s: %w", name, err)
			}
			cfgs[name] = sec.Data[corev1.DockerConfigJsonKey]
		}
		return javaext.KeychainFromDockerConfigs(cfgs)
	})
	if err != nil {
		log.Info("bundle failed", "namespace", ns, "statefulSet", sts, "pod", pod, "error", err.Error())
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	for _, sk := range res.Skipped {
		log.Info("optional extension skipped", "namespace", ns, "statefulSet", sts, "pod", pod, "reason", sk)
	}
	s.annotate(ctx, ns, sts, pod, res.Summary)
	log.Info("bundle served", "namespace", ns, "statefulSet", sts, "pod", pod, "extensions", res.Summary,
		"bytes", len(res.Data), "duration", time.Since(start).Round(time.Millisecond).String())
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("X-DX-Java-Extensions", res.Summary)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Data)
}

// annotate records on the pod what it loaded (the same annotation the webhook sets), so the rollout logic of the
// controller works the same in both modes. Only pods of that StatefulSet.
func (s *Server) annotate(ctx context.Context, ns, sts, podName, summary string) {
	if podName == "" || s.Client == nil {
		return
	}
	var p corev1.Pod
	if err := s.Reader.Get(ctx, client.ObjectKey{Namespace: ns, Name: podName}, &p); err != nil {
		return
	}
	owned := false
	for _, o := range p.OwnerReferences {
		owned = owned || (o.Kind == "StatefulSet" && o.Name == sts)
	}
	if !owned {
		return
	}
	val := any(summary)
	if summary == "" {
		val = nil
	}
	patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{dxv1.AnnotationJavaExtensions: val}}})
	if err := s.Client.Patch(ctx, &p, client.RawPatch(types.MergePatchType, patch)); err != nil {
		logf.Log.WithName("java-extensions-startup-hook").Info("cannot annotate the pod", "pod", ns+"/"+podName, "error", err.Error())
	}
}
