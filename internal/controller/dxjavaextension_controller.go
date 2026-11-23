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
	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

	// RolloutTimeout is the default time a rollout may take before it is rolled back (spec.rolloutTimeout
	// overrides it); 0 = 30 minutes. Env JAVA_EXTENSIONS_ROLLOUT_TIMEOUT (chart javaExtensions.rolloutTimeout).
	RolloutTimeout time.Duration

	mu   sync.Mutex
	apps map[string]*javaext.PortletApp // digest -> portlet.xml (artifacts are immutable by digest)
}

// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxjavaextensions,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxjavaextensions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxjavaextensions/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=get;list
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list

const (
	reasonJXNotFound       = "ArtifactNotFound"
	reasonJXRegistry       = "RegistryError"
	reasonJXNamespace      = "NamespaceNotEnabled"
	reasonJXRollingOut     = "RollingOut"
	reasonJXRolledBack     = "RolledBack"
	reasonJXRolloutFailed  = "RolloutFailed"
	reasonJXNotInjected    = "WebhookNotActive"
	reasonJXPodPending     = "PodPending"
	reasonJXRegisterFailed = "RegisterFailed"
	reasonJXInvalid        = "InvalidArtifact"
	jxDefaultTimeout       = 30 * time.Minute
	jxPoll                 = 10 * time.Second
	// jxReconcileTimeout bounds one reconcile (registry calls, XMLAccess): a hung call must never block the
	// controller worker, or the extensions stop being reconciled (#57).
	jxReconcileTimeout = 4 * time.Minute
)

// Reconcile implements reconcile.Reconciler.
func (r *DXJavaExtensionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, jxReconcileTimeout)
	defer cancel()
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
		if failure = rolloutFailure(failure, e.Status.RolloutStarted, r.rolloutTimeout(e), time.Now()); failure != "" {
			return r.rollback(ctx, e, failure)
		}
		// Keep polling until the pods are Ready (#57: the controller also watches the Web Engine pods, but a
		// poll never lets a rollout stay RollingOut once it has finished).
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
// is not empty when the rollout cannot finish (the init container failed). While it is not done, the RolledOut and
// Ready conditions say why from the facts of the pods (#61): RollingOut, PodPending (scheduler message) or
// WebhookNotActive (only for a pod created from the current template without the injection, with a diagnosis).
func (r *DXJavaExtensionReconciler) rolledOut(ctx context.Context, e *dxv1.DXJavaExtension) (bool, string, error) {
	var sts appsv1.StatefulSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.StatefulSetName()}, &sts); err != nil {
		return false, "", err
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(e.Namespace)); err != nil {
		return false, "", err
	}
	want := e.Name + "@" + e.Status.DesiredRef[strings.LastIndex(e.Status.DesiredRef, "@")+1:]
	st := jxRolloutState(&sts, pods.Items, want)
	if st.failure != "" || st.done {
		return st.done, st.failure, nil
	}
	msg := st.msg
	if st.reason == reasonJXNotInjected {
		if why := r.webhookDiagnosis(ctx, e.Namespace); why != "" {
			msg += ": " + why
		} else {
			msg += " although the webhook configuration, the namespace label and the webhook endpoints look right; check the operator log"
		}
	}
	r.fail(e, dxv1.ConditionRolledOut, st.reason, msg)
	return false, "", nil
}

// jxState is what the pods of a Web Engine StatefulSet say about a rollout.
type jxState struct {
	done    bool
	failure string // the rollout cannot finish: roll back
	reason  string // while not done: RollingOut, PodPending or WebhookNotActive
	msg     string
}

// jxRolloutState classifies the pods of the StatefulSet from facts only (#61). A pod is "current" when it was
// created from the current pod template (same java-extensions hash annotation, or the update revision). Only a
// current pod without the injection means the webhook did not act; a terminating old pod or a new pod that is not
// created or not scheduled yet is part of a normal rollout.
func jxRolloutState(sts *appsv1.StatefulSet, pods []corev1.Pod, want string) jxState {
	replicas := int32(1)
	if sts.Spec.Replicas != nil {
		replicas = *sts.Spec.Replicas
	}
	wantHash := sts.Spec.Template.Annotations[dxv1.AnnotationJavaExtensionsHash]
	var notInjected, unschedulable []string
	terminating, current := 0, 0
	ready, injected := int32(0), int32(0)
	for i := range pods {
		p := &pods[i]
		if !ownedBy(p, sts.Name) {
			continue
		}
		if !p.DeletionTimestamp.IsZero() {
			terminating++
			continue
		}
		for _, cs := range p.Status.InitContainerStatuses {
			if cs.Name != javaext.InitContainerName {
				continue
			}
			if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
				return jxState{failure: fmt.Sprintf("pod %s: the init container failed (%s); see its log", p.Name, t.Reason)}
			}
			if w := cs.State.Waiting; w != nil && w.Reason == "CrashLoopBackOff" && cs.RestartCount >= 2 {
				return jxState{failure: fmt.Sprintf("pod %s: the init container keeps failing; see its log", p.Name)}
			}
		}
		isCurrent := false
		if wantHash != "" {
			isCurrent = p.Annotations[dxv1.AnnotationJavaExtensionsHash] == wantHash
		} else if rev := sts.Status.UpdateRevision; rev != "" {
			isCurrent = p.Labels[appsv1.StatefulSetRevisionLabel] == rev
		}
		hasIt := strings.Contains(","+p.Annotations[dxv1.AnnotationJavaExtensions]+",", ","+want+",")
		if isCurrent {
			current++
			if !hasIt {
				notInjected = append(notInjected, p.Name)
			}
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
				unschedulable = append(unschedulable, p.Name+": "+c.Message)
			}
		}
		if hasIt {
			injected++
			if podReady(p) {
				ready++
			}
		}
	}
	stsDone := sts.Status.ObservedGeneration >= sts.Generation && sts.Status.UpdatedReplicas == replicas &&
		sts.Status.ReadyReplicas == replicas && (sts.Status.UpdateRevision == "" || sts.Status.CurrentRevision == sts.Status.UpdateRevision)
	if stsDone && ready == replicas && injected == replicas {
		return jxState{done: true}
	}
	switch {
	case len(notInjected) > 0:
		return jxState{reason: reasonJXNotInjected, msg: "pod " + strings.Join(notInjected, ", ") + " was created from the current template without the extension (the operator webhook did not inject it)"}
	case len(unschedulable) > 0:
		return jxState{reason: reasonJXPodPending, msg: "the new Web Engine pod cannot be scheduled: " + strings.Join(unschedulable, "; ")}
	case terminating > 0:
		return jxState{reason: reasonJXRollingOut, msg: "waiting for the old Web Engine pod to stop"}
	case current < int(replicas):
		return jxState{reason: reasonJXRollingOut, msg: "waiting for the new Web Engine pod"}
	default:
		return jxState{reason: reasonJXRollingOut, msg: "waiting for the Web Engine pod to be Ready"}
	}
}

// webhookDiagnosis says why the webhook may not act in the namespace ("" = everything looks right): the
// MutatingWebhookConfiguration that serves the java-extensions path, its namespaceSelector against the namespace
// labels, and ready endpoints of its Service. Read without the cache (get/list only).
func (r *DXJavaExtensionReconciler) webhookDiagnosis(ctx context.Context, ns string) string {
	rd := r.APIReader
	if rd == nil {
		rd = r.Client
	}
	var cfgs admissionv1.MutatingWebhookConfigurationList
	if err := rd.List(ctx, &cfgs); err != nil {
		return "cannot read the MutatingWebhookConfigurations: " + err.Error()
	}
	var nsObj corev1.Namespace
	if err := rd.Get(ctx, client.ObjectKey{Name: ns}, &nsObj); err != nil {
		return "cannot read the namespace: " + err.Error()
	}
	return diagnoseWebhook(cfgs.Items, nsObj.Labels, func(svcNS, svc string) (bool, error) {
		var slices discoveryv1.EndpointSliceList
		if err := rd.List(ctx, &slices, client.InNamespace(svcNS), client.MatchingLabels{discoveryv1.LabelServiceName: svc}); err != nil {
			return false, err
		}
		for _, s := range slices.Items {
			for _, ep := range s.Endpoints {
				if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
					return true, nil
				}
			}
		}
		return false, nil
	})
}

// diagnoseWebhook checks the configurations (pure, for the tests): one webhook serving the java-extensions path,
// whose namespaceSelector matches nsLabels and whose Service has ready endpoints.
func diagnoseWebhook(cfgs []admissionv1.MutatingWebhookConfiguration, nsLabels map[string]string, ready func(ns, svc string) (bool, error)) string {
	found := false
	for _, c := range cfgs {
		for _, w := range c.Webhooks {
			svc := w.ClientConfig.Service
			if svc == nil || svc.Path == nil || *svc.Path != webhook.JavaExtensionsPath {
				continue
			}
			found = true
			if w.NamespaceSelector != nil {
				sel, err := metav1.LabelSelectorAsSelector(w.NamespaceSelector)
				if err != nil || !sel.Matches(labels.Set(nsLabels)) {
					return fmt.Sprintf("the namespaceSelector of %s does not match the namespace labels (it needs %s=enabled)", c.Name, dxv1.LabelJavaExtensionsNamespace)
				}
			}
			ok, err := ready(svc.Namespace, svc.Name)
			if err != nil {
				return "cannot read the endpoints of the webhook Service " + svc.Namespace + "/" + svc.Name + ": " + err.Error()
			}
			if !ok {
				return "the webhook Service " + svc.Namespace + "/" + svc.Name + " has no ready endpoints (is the operator pod running?)"
			}
			return ""
		}
	}
	if !found {
		return "no MutatingWebhookConfiguration serves " + webhook.JavaExtensionsPath + " (chart value javaExtensions.enabled)"
	}
	return ""
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

// rolloutTimeout: spec.rolloutTimeout, else the operator default, else 30 minutes.
func (r *DXJavaExtensionReconciler) rolloutTimeout(e *dxv1.DXJavaExtension) time.Duration {
	if e.Spec.RolloutTimeout != nil && e.Spec.RolloutTimeout.Duration > 0 {
		return e.Spec.RolloutTimeout.Duration
	}
	if r.RolloutTimeout > 0 {
		return r.RolloutTimeout
	}
	return jxDefaultTimeout
}

// rolloutFailure returns why an unfinished rollout must be rolled back ("" = keep waiting): the failure found in
// the pods, or the timeout since the rollout started.
func rolloutFailure(failure string, started *metav1.Time, timeout time.Duration, now time.Time) string {
	if failure != "" {
		return failure
	}
	if started != nil && now.Sub(started.Time) > timeout {
		return fmt.Sprintf("not finished after %s", timeout)
	}
	return ""
}

// extensionsOfPod maps a Web Engine pod (or its StatefulSet) to the DXJavaExtensions that target it, so a pod that
// becomes Ready (or fails) reconciles them at once instead of waiting for the next poll (#57).
func (r *DXJavaExtensionReconciler) extensionsOfPod(ctx context.Context, o client.Object) []reconcile.Request {
	sts := ""
	switch obj := o.(type) {
	case *appsv1.StatefulSet:
		sts = obj.Name
	case *corev1.Pod:
		for _, ow := range obj.OwnerReferences {
			if ow.Kind == "StatefulSet" {
				sts = ow.Name
			}
		}
	}
	if sts == "" {
		return nil
	}
	var list dxv1.DXJavaExtensionList
	if err := r.List(ctx, &list, client.InNamespace(o.GetNamespace())); err != nil {
		return nil
	}
	return requestsFor(list.Items, sts)
}

func requestsFor(items []dxv1.DXJavaExtension, sts string) []reconcile.Request {
	var out []reconcile.Request
	for i := range items {
		if items[i].StatefulSetName() == sts {
			out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: items[i].Namespace, Name: items[i].Name}})
		}
	}
	return out
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
	// Only pods that the webhook touched (annotation) and StatefulSets with the extensions hash: the rest of the
	// cluster never reaches the map function.
	injected := predicate.NewPredicateFuncs(func(o client.Object) bool {
		a := o.GetAnnotations()
		if _, ok := a[dxv1.AnnotationJavaExtensions]; ok {
			return true
		}
		if sts, ok := o.(*appsv1.StatefulSet); ok {
			_, ok := sts.Spec.Template.Annotations[dxv1.AnnotationJavaExtensionsHash]
			return ok
		}
		return false
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxv1.DXJavaExtension{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.extensionsOfPod), builder.WithPredicates(injected)).
		Watches(&appsv1.StatefulSet{}, handler.EnqueueRequestsFromMapFunc(r.extensionsOfPod), builder.WithPredicates(injected)).
		Named("dxjavaextension").
		Complete(r)
}

var _ = metav1.Now
