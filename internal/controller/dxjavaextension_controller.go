/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/javaext"
	"github.com/europesip/dx-delivery-operator/internal/webhook"
)

// DXJavaExtensionReconciler loads Java extensions (portlet WARs, shared-library JARs) from a registry onto the
// official DX Compose Web Engine image (issue #54). It resolves the artifact to a digest before anything changes,
// rolls the Web Engine out through its StatefulSet (the webhook injects the init container into the new pods),
// waits for every pod to be Ready with the new digest, and registers the portlets in DX with XMLAccess
// predeployed only when they are missing or changed. A rollout that fails goes back to the previous digest.
type DXJavaExtensionReconciler struct {
	client.Client
	// APIReader reads without the cache (the namespaces and the extensions right after a status change).
	APIReader client.Reader
	Scheme    *runtime.Scheme
	*Shared

	mu   sync.Mutex
	apps map[string]*javaext.PortletApp // digest -> portlet.xml (artifacts are immutable by digest)
}

// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxjavaextensions,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxjavaextensions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxjavaextensions/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

const (
	reasonJXNotFound       = "ArtifactNotFound"
	reasonJXRegistry       = "RegistryError"
	reasonJXNamespace      = "NamespaceNotEnabled"
	reasonJXRollingOut     = "RollingOut"
	reasonJXRolledBack     = "RolledBack"
	reasonJXRolloutFailed  = "RolloutFailed"
	reasonJXNotInjected    = "WebhookNotActive"
	reasonJXRegisterFailed = "RegisterFailed"
	reasonJXInvalid        = "InvalidArtifact"
	jxDefaultTimeout       = 30 * time.Minute
	jxPoll                 = 15 * time.Second
)

// Reconcile implements reconcile.Reconciler.
func (r *DXJavaExtensionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var e dxv1.DXJavaExtension
	if err := r.Get(ctx, req.NamespacedName, &e); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !e.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &e)
	}
	if controllerutil.AddFinalizer(&e, Finalizer) {
		if err := r.Update(ctx, &e); err != nil {
			return ctrl.Result{}, err
		}
	}
	if e.Status.ObservedGeneration != e.Generation {
		e.Status.FailedRef = "" // a new spec may be rolled out again
	}
	e.Status.ObservedGeneration = e.Generation
	res, err := r.reconcile(ctx, &e)
	if serr := r.Status().Update(ctx, &e); serr != nil && !apierrors.IsConflict(serr) {
		log.Error(serr, "status update")
	}
	if err != nil && isNotReady(err) {
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	return res, err
}

func (r *DXJavaExtensionReconciler) fail(e *dxv1.DXJavaExtension, cond, reason, msg string) {
	if cond != dxv1.ConditionReady {
		setCondition(&e.Status.Conditions, e.Generation, cond, false, reason, msg)
	}
	setCondition(&e.Status.Conditions, e.Generation, dxv1.ConditionReady, false, reason, msg)
}

func (r *DXJavaExtensionReconciler) keychain(ctx context.Context, e *dxv1.DXJavaExtension) (authn.Keychain, error) {
	cfgs := map[string][]byte{}
	for _, s := range e.Spec.Artifact.PullSecrets {
		var sec corev1.Secret
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: s.Name}, &sec); err != nil {
			return nil, fmt.Errorf("pull secret %s: %w", s.Name, err)
		}
		cfgs[s.Name] = sec.Data[corev1.DockerConfigJsonKey]
	}
	return javaext.KeychainFromDockerConfigs(cfgs)
}

func (r *DXJavaExtensionReconciler) reconcile(ctx context.Context, e *dxv1.DXJavaExtension) (ctrl.Result, error) {
	// The namespace must be enabled: the webhook only acts there (without it the pods would restart for nothing).
	var ns corev1.Namespace
	if err := r.APIReader.Get(ctx, client.ObjectKey{Name: e.Namespace}, &ns); err != nil {
		return ctrl.Result{}, err
	}
	if ns.Labels[dxv1.LabelJavaExtensionsNamespace] != "enabled" {
		r.fail(e, dxv1.ConditionReady, reasonJXNamespace, fmt.Sprintf("the namespace needs the label %s=enabled (the webhook acts only there)", dxv1.LabelJavaExtensionsNamespace))
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// 1. Resolve the artifact BEFORE anything changes: a missing tag never restarts the Web Engine.
	kc, err := r.keychain(ctx, e)
	if err != nil {
		r.fail(e, dxv1.ConditionArtifactResolved, reasonJXRegistry, err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	digest, repo, err := javaext.Resolve(ctx, e.Spec.Artifact.Ref, kc)
	if err != nil {
		reason := reasonJXRegistry
		if errors.Is(err, javaext.ErrNotFound) {
			reason = reasonJXNotFound
		}
		msg := err.Error()
		if e.Status.DesiredRef != "" {
			msg += "; the Web Engine keeps " + e.Status.DesiredRef + " (no restart)"
		}
		r.fail(e, dxv1.ConditionArtifactResolved, reason, msg)
		r.Recorder.Eventf(e, nil, corev1.EventTypeWarning, reason, "Resolve", "%s", msg)
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}
	pinned := repo + "@" + digest
	e.Status.ResolvedDigest = digest
	setCondition(&e.Status.Conditions, e.Generation, dxv1.ConditionArtifactResolved, true, "Resolved", pinned)

	// The portlet.xml of the artifact (portlets): checked before the rollout too.
	var app *javaext.PortletApp
	if e.KindOf() == dxv1.JavaExtensionPortlet {
		if app, err = r.portletApp(ctx, e, pinned, kc); err != nil {
			r.fail(e, dxv1.ConditionReady, reasonJXInvalid, err.Error())
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
	}

	// 2. A new digest: record it (the webhook injects DesiredRef) and roll the Web Engine out.
	if e.Status.DesiredRef != pinned && e.Status.FailedRef != pinned {
		if e.Status.DesiredRef != "" {
			e.Status.PreviousRef = e.Status.DesiredRef
		}
		e.Status.DesiredRef = pinned
		e.Status.RolloutStarted = now()
		setCondition(&e.Status.Conditions, e.Generation, dxv1.ConditionRolledOut, false, reasonJXRollingOut, "rolling the Web Engine out with "+pinned)
		setCondition(&e.Status.Conditions, e.Generation, dxv1.ConditionReady, false, reasonJXRollingOut, "rolling the Web Engine out with "+pinned)
		r.Recorder.Eventf(e, nil, corev1.EventTypeNormal, "RolloutStarted", "Rollout", "Web Engine %s: rolling out %s", e.StatefulSetName(), pinned)
		// The status first: the webhook reads it when the new pods are created.
		if err := r.Status().Update(ctx, e); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(e), e); err != nil {
			return ctrl.Result{}, err
		}
	}
	if e.Status.FailedRef == pinned && e.Status.DesiredRef != pinned {
		msg := fmt.Sprintf("the rollout of %s failed and the Web Engine went back to %s; change the spec to try again", pinned, e.Status.DesiredRef)
		r.fail(e, dxv1.ConditionReady, reasonJXRolledBack, msg)
	}
	if err := r.syncHash(ctx, e); err != nil {
		return ctrl.Result{}, err
	}
	if e.Status.DesiredRef == "" { // the first rollout failed: nothing of this extension runs
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// 3. Wait until every Web Engine pod runs Ready with the desired digest.
	done, failure, err := r.rolledOut(ctx, e)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !done {
		timeout := jxDefaultTimeout
		if e.Spec.RolloutTimeout != nil {
			timeout = e.Spec.RolloutTimeout.Duration
		}
		if failure == "" && e.Status.RolloutStarted != nil && time.Since(e.Status.RolloutStarted.Time) > timeout {
			failure = fmt.Sprintf("not finished after %s", timeout)
		}
		if failure != "" {
			return r.rollback(ctx, e, failure)
		}
		return ctrl.Result{RequeueAfter: jxPoll}, nil
	}
	if e.Status.RolledOutRef != e.Status.DesiredRef {
		r.Recorder.Eventf(e, nil, corev1.EventTypeNormal, "RolledOut", "Rollout", "every Web Engine pod runs %s", e.Status.DesiredRef)
	}
	e.Status.RolledOutRef = e.Status.DesiredRef
	e.Status.RolloutStarted = nil
	setCondition(&e.Status.Conditions, e.Generation, dxv1.ConditionRolledOut, true, "RolledOut", "every Web Engine pod runs "+e.Status.DesiredRef)

	// 4. Register the portlets in DX (only if missing or changed).
	if e.Registers() && app != nil && e.Status.DesiredRef == pinned {
		if err := r.register(ctx, e, app); err != nil {
			if isNotReady(err) {
				r.fail(e, dxv1.ConditionRegistered, "ConnectionNotReady", err.Error())
				return ctrl.Result{}, err
			}
			if transientDX(err) { // the Web Engine is Ready but HAProxy/DX does not route it yet
				r.fail(e, dxv1.ConditionRegistered, "WaitingForDX", "DX is starting: "+err.Error())
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			r.fail(e, dxv1.ConditionRegistered, reasonJXRegisterFailed, err.Error())
			r.Recorder.Eventf(e, nil, corev1.EventTypeWarning, reasonJXRegisterFailed, "Register", "%v", err)
			return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
		}
	}
	if meta.IsStatusConditionFalse(e.Status.Conditions, dxv1.ConditionReady) && meta.FindStatusCondition(e.Status.Conditions, dxv1.ConditionReady).Reason == reasonJXRolledBack {
		return ctrl.Result{}, nil // keep saying why the requested digest is not running
	}
	msg := e.Status.DesiredRef + " loaded by the Web Engine"
	if len(e.Status.Portlets) > 0 {
		msg += fmt.Sprintf("; portlets registered in DX: %s", strings.Join(e.Status.Portlets, ", "))
	}
	setCondition(&e.Status.Conditions, e.Generation, dxv1.ConditionReady, true, "Ready", msg)
	return ctrl.Result{}, nil
}

func (r *DXJavaExtensionReconciler) portletApp(ctx context.Context, e *dxv1.DXJavaExtension, pinned string, kc authn.Keychain) (*javaext.PortletApp, error) {
	digest := pinned[strings.LastIndex(pinned, "@")+1:]
	r.mu.Lock()
	app := r.apps[digest]
	r.mu.Unlock()
	if app == nil {
		f, err := javaext.Fetch(ctx, pinned, kc)
		if err != nil {
			return nil, err
		}
		if app, err = javaext.ReadPortletXML(f.Data); err != nil {
			return nil, fmt.Errorf("%s: %w", pinned, err)
		}
		if app.UID == "" {
			app.UID = strings.TrimSuffix(f.Annotations[javaext.AnnotationWebAppUID], ".webmod")
		}
		r.mu.Lock()
		if r.apps == nil {
			r.apps = map[string]*javaext.PortletApp{}
		}
		r.apps[digest] = app
		r.mu.Unlock()
	}
	out := *app
	if e.Spec.PortletAppUID != "" {
		out.UID = e.Spec.PortletAppUID
	}
	if out.UID == "" {
		return nil, errors.New("the portlet application has no uid: set an id on <portlet-app> in portlet.xml, the annotation " + javaext.AnnotationWebAppUID + " or spec.portletAppUID")
	}
	return &out, nil
}

// syncHash sets the pod-template annotation of the Web Engine StatefulSet from every extension that targets it;
// a change rolls the Web Engine out (RollingUpdate: one pod at a time, waiting for Ready).
func (r *DXJavaExtensionReconciler) syncHash(ctx context.Context, e *dxv1.DXJavaExtension) error {
	entries, _, err := webhook.EntriesFor(ctx, r.APIReader, e.Namespace, e.StatefulSetName())
	if err != nil {
		return err
	}
	want := ""
	if len(entries) > 0 {
		want = hashOf(javaext.Summary(entries))
	}
	var sts appsv1.StatefulSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.StatefulSetName()}, &sts); err != nil {
		if apierrors.IsNotFound(err) {
			return notReady("StatefulSet %s does not exist", e.StatefulSetName())
		}
		return err
	}
	if sts.Spec.Template.Annotations[dxv1.AnnotationJavaExtensionsHash] == want {
		return nil
	}
	val := any(want)
	if want == "" {
		val = nil // remove the annotation
	}
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{dxv1.AnnotationJavaExtensionsHash: val}}}}})
	logf.FromContext(ctx).Info("rolling the Web Engine out", "statefulSet", sts.Name, "extensions", javaext.Summary(entries))
	return r.Patch(ctx, &sts, client.RawPatch(types.MergePatchType, patch))
}

// rolledOut says whether every pod of the StatefulSet is Ready with the desired digest of this extension. failure
// is not empty when the rollout cannot finish (the init container failed).
func (r *DXJavaExtensionReconciler) rolledOut(ctx context.Context, e *dxv1.DXJavaExtension) (bool, string, error) {
	var sts appsv1.StatefulSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.StatefulSetName()}, &sts); err != nil {
		return false, "", err
	}
	replicas := int32(1)
	if sts.Spec.Replicas != nil {
		replicas = *sts.Spec.Replicas
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(e.Namespace)); err != nil {
		return false, "", err
	}
	want := e.Name + "@" + e.Status.DesiredRef[strings.LastIndex(e.Status.DesiredRef, "@")+1:]
	ready, injected := int32(0), int32(0)
	for i := range pods.Items {
		p := &pods.Items[i]
		if !ownedBy(p, sts.Name) || !p.DeletionTimestamp.IsZero() {
			continue
		}
		for _, cs := range p.Status.InitContainerStatuses {
			if cs.Name != javaext.InitContainerName {
				continue
			}
			if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
				return false, fmt.Sprintf("pod %s: the init container failed (%s); see its log", p.Name, t.Reason), nil
			}
			if w := cs.State.Waiting; w != nil && w.Reason == "CrashLoopBackOff" && cs.RestartCount >= 2 {
				return false, fmt.Sprintf("pod %s: the init container keeps failing; see its log", p.Name), nil
			}
		}
		if !strings.Contains(","+p.Annotations[dxv1.AnnotationJavaExtensions]+",", ","+want+",") {
			continue
		}
		injected++
		if podReady(p) {
			ready++
		}
	}
	stsDone := sts.Status.ObservedGeneration >= sts.Generation && sts.Status.UpdatedReplicas == replicas &&
		sts.Status.ReadyReplicas == replicas && (sts.Status.UpdateRevision == "" || sts.Status.CurrentRevision == sts.Status.UpdateRevision)
	if stsDone && injected < replicas {
		r.fail(e, dxv1.ConditionRolledOut, reasonJXNotInjected, "the Web Engine pods were restarted but none has the extension: is the operator webhook enabled (chart value javaExtensions.enabled) and is its namespaceSelector matching?")
	}
	return stsDone && ready == replicas && injected == replicas, "", nil
}

func ownedBy(p *corev1.Pod, sts string) bool {
	for _, o := range p.OwnerReferences {
		if o.Kind == "StatefulSet" && o.Name == sts {
			return true
		}
	}
	return false
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// rollback goes back to the previous digest (or, on the first rollout, removes the extension from the pods).
func (r *DXJavaExtensionReconciler) rollback(ctx context.Context, e *dxv1.DXJavaExtension, why string) (ctrl.Result, error) {
	failed := e.Status.DesiredRef
	e.Status.FailedRef = failed
	e.Status.DesiredRef = e.Status.PreviousRef
	e.Status.PreviousRef = ""
	e.Status.RolloutStarted = nil
	back := e.Status.DesiredRef
	if back == "" {
		back = "no version of this extension"
	}
	msg := fmt.Sprintf("rollout of %s failed (%s): back to %s", failed, why, back)
	r.fail(e, dxv1.ConditionRolledOut, reasonJXRolledBack, msg)
	r.Recorder.Eventf(e, nil, corev1.EventTypeWarning, reasonJXRolloutFailed, "Rollout", "%s", msg)
	if err := r.Status().Update(ctx, e); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.syncHash(ctx, e); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(e), e); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: jxPoll}, nil
}

// register runs XMLAccess predeployed only when the web module is missing, its portlets differ, or what is
// registered (uid, portlets, file, context root, page) changed since the last time.
func (r *DXJavaExtensionReconciler) register(ctx context.Context, e *dxv1.DXJavaExtension, app *javaext.PortletApp) error {
	reg := javaext.Registration{App: *app, File: e.File(), ContextRoot: e.Context(), DisplayName: e.Name}
	if p := e.Spec.Page; p != nil {
		titles := map[string]string{"en": p.Title}
		for l, t := range p.Titles {
			titles[l] = t
		}
		reg.Page = &javaext.Page{UniqueName: p.UniqueName, ParentUniqueName: p.ParentUniqueName, Titles: titles,
			FriendlyName: p.FriendlyName, Anonymous: p.Anonymous == nil || *p.Anonymous}
	}
	pageJSON, _ := json.Marshal(reg.Page)
	want := hashOf(app.UID, strings.Join(app.Portlets, ","), reg.File, reg.ContextRoot, string(pageJSON))
	if e.Status.RegisteredHash == want && meta.IsStatusConditionTrue(e.Status.Conditions, dxv1.ConditionRegistered) {
		return nil
	}
	conn, err := r.Connection(ctx, r.Client, e.Namespace, e.Spec.ConnectionRef.Name)
	if err != nil {
		return err
	}
	out, err := conn.Client.XMLAccess(ctx, javaext.ExportXML(app.UID))
	if err != nil {
		return err
	}
	exists, portlets, err := javaext.ParseExport(out)
	if err != nil {
		return err
	}
	if !exists || !javaext.SameSet(portlets, app.Portlets) || reg.Page != nil {
		res, err := conn.Client.XMLAccess(ctx, javaext.RegisterXML(reg))
		if err != nil {
			return err
		}
		if ok, msg := javaextOK(res); !ok {
			return fmt.Errorf("XMLAccess predeployed registration failed: %s", msg)
		}
		r.Recorder.Eventf(e, nil, corev1.EventTypeNormal, "Registered", "Register", "web module %s with %d portlet(s) registered in DX", javaext.WebModuleUID(app.UID), len(app.Portlets))
	}
	e.Status.PortletAppUID = app.UID
	e.Status.Portlets = app.Portlets
	e.Status.RegisteredHash = want
	setCondition(&e.Status.Conditions, e.Generation, dxv1.ConditionRegistered, true, "Registered",
		fmt.Sprintf("%s: %s", javaext.WebModuleUID(app.UID), strings.Join(app.Portlets, ", ")))
	return nil
}

// transientDX: an answer of a portal that is still starting (HAProxy without a ready backend, a gateway timeout).
func transientDX(err error) bool {
	m := err.Error()
	return strings.Contains(m, "HTTP 502") || strings.Contains(m, "HTTP 503") || strings.Contains(m, "HTTP 504")
}

func javaextOK(result string) (bool, string) {
	exists, _, err := javaext.ParseExport(result) // same success/failure rule as an export
	if err != nil {
		return false, err.Error()
	}
	if !exists {
		return false, "XMLAccess did not report success"
	}
	return true, ""
}

// finalize removes the extension: unregisters the portlets (and the page) from DX when it registered them, then
// rolls the Web Engine out without it.
func (r *DXJavaExtensionReconciler) finalize(ctx context.Context, e *dxv1.DXJavaExtension) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(e, Finalizer) {
		return ctrl.Result{}, nil
	}
	if e.Registers() && e.Status.PortletAppUID != "" {
		page := ""
		if e.Spec.Page != nil {
			page = e.Spec.Page.UniqueName
		}
		conn, err := r.Connection(ctx, r.Client, e.Namespace, e.Spec.ConnectionRef.Name)
		if err == nil {
			_, err = conn.Client.XMLAccess(ctx, javaext.UnregisterXML(e.Status.PortletAppUID, page))
		}
		if err != nil {
			r.Recorder.Eventf(e, nil, corev1.EventTypeWarning, "UnregisterFailed", "Delete", "portlets left in DX: %v", err)
		} else {
			r.Recorder.Eventf(e, nil, corev1.EventTypeNormal, "Unregistered", "Delete", "web module %s removed from DX", javaext.WebModuleUID(e.Status.PortletAppUID))
		}
	}
	if err := r.syncHash(ctx, e); err != nil && !isNotReady(err) {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(e, Finalizer)
	return ctrl.Result{}, r.Update(ctx, e)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DXJavaExtensionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxv1.DXJavaExtension{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Named("dxjavaextension").
		Complete(r)
}

var _ = metav1.Now
