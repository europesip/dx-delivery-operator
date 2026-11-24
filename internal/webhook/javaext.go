// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

// Package webhook holds the admission webhooks of the operator.
package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/javaext"
)

// JavaExtensionsPath is where the API server sends the Web Engine pods (MutatingWebhookConfiguration of the chart).
const JavaExtensionsPath = "/mutate-web-engine-pod"

// JavaExtensionInjector is the mutating admission webhook of DXJavaExtension (issue #54). When the API server is
// about to create a Web Engine pod of a namespace with java extensions, it adds the init container that pulls them,
// the emptyDir volumes and the mounts on the official Web Engine container. It never changes the HCL chart, the
// Helm release or the image: only the pod being created.
type JavaExtensionInjector struct {
	Client  client.Reader
	Decoder admission.Decoder
	Options javaext.InjectOptions
}

// Handle implements admission.Handler.
func (h *JavaExtensionInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := logf.FromContext(ctx).WithName("java-extensions-webhook")
	if req.Operation != admissionv1.Create {
		return admission.Allowed("not a create")
	}
	var pod corev1.Pod
	if err := h.Decoder.Decode(req, &pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	sts := ""
	for _, o := range pod.OwnerReferences {
		if o.Kind == "StatefulSet" {
			sts = o.Name
		}
	}
	if sts == "" {
		return admission.Allowed("not a StatefulSet pod")
	}
	// Only the extensions delivered by the webhook (issue #72: startupHook ones come through the HCL startup folder).
	entries, container, err := EntriesForMode(ctx, h.Client, req.Namespace, sts, dxv1.DeliveryWebhook)
	if err != nil {
		// The API server applies the failurePolicy of the chart (Fail by default): the pod is not created and the
		// StatefulSet retries, instead of starting a Web Engine without its extensions.
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if len(entries) == 0 {
		return admission.Allowed("no java extensions for " + sts)
	}
	opts := h.Options
	opts.Container = container
	orig, err := json.Marshal(&pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	changed, err := javaext.Inject(&pod, entries, opts)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if !changed {
		return admission.Allowed("already injected or no " + container + " container")
	}
	cur, err := json.Marshal(&pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	log.Info("injecting java extensions", "namespace", req.Namespace, "statefulSet", sts, "extensions", javaext.Summary(entries))
	return admission.PatchResponseFromRaw(orig, cur)
}

// EntriesFor returns the extensions to inject into the pods of a Web Engine StatefulSet: every DXJavaExtension of
// the namespace that targets it and has a desired reference (a resolved digest), and the container name.
func EntriesFor(ctx context.Context, c client.Reader, namespace, sts string) ([]javaext.Entry, string, error) {
	return EntriesForMode(ctx, c, namespace, sts, "")
}

// EntriesForMode is EntriesFor limited to one delivery mode ("" = every mode).
func EntriesForMode(ctx context.Context, c client.Reader, namespace, sts, mode string) ([]javaext.Entry, string, error) {
	var list dxv1.DXJavaExtensionList
	if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, "", err
	}
	var out []javaext.Entry
	container := "web-engine"
	for i := range list.Items {
		e := &list.Items[i]
		if e.StatefulSetName() != sts || e.Status.DesiredRef == "" || !e.DeletionTimestamp.IsZero() {
			continue
		}
		if mode != "" && e.DeliveryMode() != mode {
			continue
		}
		container = e.ContainerName()
		var secrets []string
		for _, s := range e.Spec.Artifact.PullSecrets {
			secrets = append(secrets, s.Name)
		}
		sort.Strings(secrets)
		out = append(out, javaext.Entry{
			Name:        e.Name,
			Type:        e.KindOf(),
			Ref:         e.Status.DesiredRef,
			File:        e.File(),
			ContextRoot: e.Context(),
			Optional:    e.Spec.Optional,
			PullSecrets: secrets,
		})
	}
	javaext.SortEntries(out)
	return out, container, nil
}
