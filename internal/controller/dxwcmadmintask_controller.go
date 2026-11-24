// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

// DXWCMAdminTask: the WCM maintenance modules of DX Compose (member fixer, workflow checker) over HTTP, once per spec,
// one library after the other, report only unless spec.fix. The answer of DX for every library goes to the ConfigMap
// <name>-report (and to a registry with spec.output.oci); its counters go to status.libraries.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/dx"
	"github.com/europesip/dx-delivery-operator/internal/source"
)

// DXWCMAdminTaskReconciler runs WCM maintenance modules of DX.
type DXWCMAdminTaskReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
	*Shared
}

// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxwcmadmintasks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxwcmadmintasks/status,verbs=get;update;patch

const (
	defaultAdminWait     = time.Hour
	reasonAdminFailed    = "TaskFailed"
	reasonAdminWithIssue = "NeedsAttention"
)

func adminHash(o *dxv1.DXWCMAdminTask) string {
	s := o.Spec.DeepCopy()
	s.Suspend, s.DependsOn = false, nil
	b, _ := json.Marshal(s)
	return hashOf(string(b), forceToken(o))
}

func adminReportName(o *dxv1.DXWCMAdminTask) string {
	n := o.Name + "-report"
	if len(n) > 253 {
		n = n[:253]
	}
	return n
}

// validateAdmin checks what the CRD schema cannot: options of the right task, at least one action of the workflow
// checker (DX does nothing without one).
func validateAdmin(s *dxv1.DXWCMAdminTaskSpec) error {
	switch s.Task {
	case dxv1.WCMAdminMemberFixer:
		if s.WorkflowChecker != nil {
			return errors.New("workflowChecker options are for task WorkflowControlChecker")
		}
	case dxv1.WCMAdminWorkflowControlChecker:
		if s.MemberFixer != nil {
			return errors.New("memberFixer options are for task MemberFixer")
		}
		w := s.WorkflowChecker
		if w == nil || !(w.UpdateDocSecurity || w.RescheduleActions || w.RepairInvalidWorkflow) {
			return errors.New("workflowChecker: set updateDocSecurity, rescheduleActions or repairInvalidWorkflow (DX does nothing without one)")
		}
		if w.PreserveSecurityInheritancePropagation && !w.UpdateDocSecurity {
			return errors.New("workflowChecker.preserveSecurityInheritancePropagation only works with updateDocSecurity")
		}
	default:
		return fmt.Errorf("task %q: MemberFixer or WorkflowControlChecker", s.Task)
	}
	if !s.AllLibraries && len(s.Libraries) == 0 {
		return errors.New("libraries (one or more) or allLibraries: true")
	}
	if s.AllLibraries && len(s.Libraries) > 0 {
		return errors.New("libraries or allLibraries, not both")
	}
	return nil
}

// adminQuery is the query of one library (MOD is added by the client). Only documented parameters; names verified on
// CF239 where DX echoes them (preserve_dates, restrictOn, noRealmDn).
func adminQuery(s *dxv1.DXWCMAdminTaskSpec, library string) url.Values {
	q := url.Values{"library": {library}}
	if s.Fix {
		q.Set("fix", "true")
	}
	if len(s.RestrictOn) > 0 {
		parts := make([]string, len(s.RestrictOn))
		for i, t := range s.RestrictOn {
			parts[i] = string(t)
		}
		q.Set("restrictOn", strings.Join(parts, ","))
	}
	if s.PreserveDates {
		q.Set("preserve_dates", "true")
	}
	if m := s.MemberFixer; m != nil && s.Task == dxv1.WCMAdminMemberFixer {
		if m.AltDn != "" {
			q.Set("altDn", string(m.AltDn))
		}
		if m.InvalidDn != "" {
			q.Set("invalidDn", string(m.InvalidDn))
		}
		if m.MismatchedId != "" {
			q.Set("mismatchedId", string(m.MismatchedId))
		}
		if m.Realm != "" {
			q.Set("realm", m.Realm)
		}
		if m.NoRealmDn {
			q.Set("noRealmDn", "true")
		}
	}
	if w := s.WorkflowChecker; w != nil && s.Task == dxv1.WCMAdminWorkflowControlChecker {
		for k, v := range map[string]bool{"updateDocSecurity": w.UpdateDocSecurity, "rescheduleActions": w.RescheduleActions,
			"repairInvalidWorkflow": w.RepairInvalidWorkflow, "preserveSecurityInheritancePropagation": w.PreserveSecurityInheritancePropagation} {
			if v {
				q.Set(k, "true")
			}
		}
	}
	return q
}

func (r *DXWCMAdminTaskReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Reconcile runs the task when the spec changed since the last finished run.
func (r *DXWCMAdminTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var o dxv1.DXWCMAdminTask
	rd := r.reader()
	if err := rd.Get(ctx, req.NamespacedName, &o); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !o.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	orig := o.DeepCopy()
	if o.Spec.Suspend {
		setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, false, "Suspended", "reconciliation is suspended (spec.suspend)")
		return ctrl.Result{}, r.Status().Patch(ctx, &o, client.MergeFrom(orig))
	}
	hash := adminHash(&o)
	if o.Status.AppliedHash == hash && (o.Status.Phase == dxv1.WCMAdminSucceeded || o.Status.Phase == dxv1.WCMAdminFailed) {
		return ctrl.Result{}, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.cancelWhenDeleted(runCtx, cancel, req.NamespacedName, rd)
	res, err := r.run(runCtx, &o, hash)
	if ctx.Err() == nil && runCtx.Err() != nil {
		logf.FromContext(ctx).Info("the task was deleted while it ran: stopped", "name", req.NamespacedName.String())
		return ctrl.Result{}, nil
	}
	o.Status.ObservedGeneration = o.Generation
	if uerr := r.saveStatus(ctx, &o); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return res, err
}

func (r *DXWCMAdminTaskReconciler) saveStatus(ctx context.Context, o *dxv1.DXWCMAdminTask) error {
	var cur dxv1.DXWCMAdminTask
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(o), &cur); err != nil {
		return err
	}
	cp := cur.DeepCopy()
	cp.Status = *o.Status.DeepCopy()
	return r.Status().Patch(ctx, cp, client.MergeFrom(&cur))
}

func (r *DXWCMAdminTaskReconciler) cancelWhenDeleted(ctx context.Context, cancel context.CancelFunc, key client.ObjectKey, rd client.Reader) {
	t := time.NewTicker(cancelCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var o dxv1.DXWCMAdminTask
			err := rd.Get(ctx, key, &o)
			if apierrors.IsNotFound(err) || (err == nil && !o.DeletionTimestamp.IsZero()) {
				cancel()
				return
			}
		}
	}
}

func (r *DXWCMAdminTaskReconciler) run(ctx context.Context, o *dxv1.DXWCMAdminTask, hash string) (ctrl.Result, error) {
	gen := o.Generation
	st := &o.Status
	if err := validateAdmin(&o.Spec); err != nil {
		st.Phase, st.Message, st.AppliedHash, st.CompletionTime = dxv1.WCMAdminFailed, err.Error(), hash, now()
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, reasonInvalidSpec, st.Message)
		return ctrl.Result{}, nil
	}
	if err := Dependencies(ctx, r.Client, o.Namespace, o.Spec.DependsOn); err != nil {
		if isNotReady(err) {
			setCondition(&st.Conditions, gen, dxv1.ConditionDependenciesReady, false, "Waiting", err.Error())
			setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, "Waiting", err.Error())
			st.Phase = dxv1.WCMAdminPending
			return ctrl.Result{RequeueAfter: waitShort}, nil
		}
		return ctrl.Result{}, err
	}
	setCondition(&st.Conditions, gen, dxv1.ConditionDependenciesReady, true, "Ready", "all dependencies are ready")
	conn, err := r.Connection(ctx, r.Client, o.Namespace, o.Spec.ConnectionRef.Name)
	if err != nil {
		if isNotReady(err) {
			setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, "Waiting", err.Error())
			st.Phase = dxv1.WCMAdminPending
			return ctrl.Result{RequeueAfter: waitShort}, nil
		}
		return ctrl.Result{}, err
	}
	st.StartTime, st.CompletionTime, st.Libraries, st.CurrentLibrary, st.OutputRef, st.OutputDigest = now(), nil, nil, 0, "", ""

	err = r.runAll(ctx, o, conn)
	var p permanent
	switch {
	case err == nil:
		st.Phase, st.CompletionTime, st.AppliedHash, st.CurrentLibrary = dxv1.WCMAdminSucceeded, now(), hash, 0
		reason := reasonSucceeded
		for _, l := range st.Libraries {
			if l.Errors > 0 || l.Result != "ok" {
				reason = reasonAdminWithIssue
			}
		}
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, true, reason, st.Message)
		r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, reason, "WCMAdminTask", "%s", st.Message)
		return ctrl.Result{}, nil
	case errors.As(err, &p):
		st.Phase, st.Message, st.AppliedHash, st.CompletionTime, st.CurrentLibrary = dxv1.WCMAdminFailed, p.err.Error(), hash, now(), 0
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, p.reason, st.Message)
		r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, p.reason, "WCMAdminTask", "%s", st.Message)
		return ctrl.Result{}, nil
	case errors.Is(err, context.Canceled):
		return ctrl.Result{}, err
	default:
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, reasonDXError, err.Error())
		st.Phase, st.Message, st.CurrentLibrary = dxv1.WCMAdminPending, err.Error(), 0
		r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, reasonDXError, "WCMAdminTask", "%v", err)
		return ctrl.Result{RequeueAfter: retryFailed}, nil
	}
}

// runAll runs the module on every library, one after the other (each request holds the lock of the DX).
func (r *DXWCMAdminTaskReconciler) runAll(ctx context.Context, o *dxv1.DXWCMAdminTask, conn *Connection) error {
	st := &o.Status
	c := conn.Client
	vp := o.Spec.VirtualPortal
	// With spec.internalURL on the connection the requests go to the in-cluster URL (no proxy timeout), as imports do.
	libs := o.Spec.Libraries
	if o.Spec.AllLibraries {
		all, err := c.ListLibraries(ctx, vp)
		if err != nil {
			return fmt.Errorf("listing the libraries: %w", err)
		}
		libs = nil
		for _, l := range all {
			libs = append(libs, l.Title)
		}
		if len(libs) == 0 {
			return fatal(reasonAdminFailed, "the portal has no WCM libraries")
		}
	}
	wait := defaultAdminWait
	if o.Spec.Timeout != nil && o.Spec.Timeout.Duration > 0 {
		wait = o.Spec.Timeout.Duration
	}
	mode := "report"
	if o.Spec.Fix {
		mode = "fix"
	}
	reports := map[string]string{}
	var problems []string
	for i, lib := range libs {
		st.CurrentLibrary = i + 1
		st.Phase, st.Message = dxv1.WCMAdminRunning, fmt.Sprintf("%s (%s) %d/%d: %s", o.Spec.Task, mode, i+1, len(libs), lib)
		setCondition(&st.Conditions, o.Generation, dxv1.ConditionReady, false, string(dxv1.WCMAdminRunning), st.Message)
		if err := r.saveStatus(ctx, o); err != nil {
			logf.FromContext(ctx).Info("cannot save the progress", "error", err.Error())
		}
		text, err := c.AdminTaskModule(ctx, vp, string(o.Spec.Task), adminQuery(&o.Spec, lib), wait)
		res := dxv1.WCMAdminLibraryResult{Library: lib, Result: "ok"}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			res.Result = truncate(err.Error(), 400)
			problems = append(problems, lib)
		}
		s := dx.ParseAdminSummary(text)
		res.ItemsProcessed, res.ItemsUpdated, res.Errors = s.ItemsProcessed, s.ItemsUpdated, s.Errors
		res.ReferencesToRemove, res.ReferencesToUpdate, res.VirtualMembersToUpdate = s.ReferencesToRemove, s.ReferencesToUpdate, s.VirtualMembersToUpdate
		if s.Problem != "" && res.Result == "ok" {
			res.Result = s.Problem
		}
		st.Libraries = append(st.Libraries, res)
		reports[lib] = text
		if err != nil && !errors.Is(err, dx.ErrCutByProxy) && strings.Contains(err.Error(), "session") {
			return err // DX cannot be signed in to: retry the whole run later
		}
	}
	if err := r.saveReport(ctx, o, reports); err != nil {
		return err
	}
	if out := o.Spec.Output; out != nil && out.OCI != nil {
		if err := r.pushReport(ctx, o, conn, reports); err != nil {
			return fmt.Errorf("pushing the report: %w", err)
		}
	}
	tot := dx.AdminSummary{}
	for _, l := range st.Libraries {
		tot.ItemsProcessed += l.ItemsProcessed
		tot.ItemsUpdated += l.ItemsUpdated
		tot.ReferencesToRemove += l.ReferencesToRemove
		tot.ReferencesToUpdate += l.ReferencesToUpdate
		tot.Errors += l.Errors
	}
	st.Message = fmt.Sprintf("%s (%s) on %d librar%s: %d items processed, %d updated", o.Spec.Task, mode, len(libs), plural(len(libs), "y", "ies"), tot.ItemsProcessed, tot.ItemsUpdated)
	if o.Spec.Task == dxv1.WCMAdminMemberFixer {
		st.Message += fmt.Sprintf(", %d references to remove, %d to update", tot.ReferencesToRemove, tot.ReferencesToUpdate)
	}
	if tot.Errors > 0 {
		st.Message += fmt.Sprintf(", %d errors", tot.Errors)
	}
	if len(problems) > 0 {
		st.Message += fmt.Sprintf("; failed: %s", strings.Join(problems, ", "))
	}
	st.Message += fmt.Sprintf(" (report in the ConfigMap %s; per item detail in the log of the Web Engine)", st.ReportConfigMap)
	if len(problems) == len(libs) {
		return fatal(reasonAdminFailed, "%s", st.Message)
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// saveReport writes the answer of DX for every library to the ConfigMap <name>-report (owned by the resource).
func (r *DXWCMAdminTaskReconciler) saveReport(ctx context.Context, o *dxv1.DXWCMAdminTask, reports map[string]string) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: adminReportName(o), Namespace: o.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if owner := metav1.GetControllerOf(cm); owner != nil && owner.UID != o.UID {
			return fmt.Errorf("the ConfigMap %s belongs to another resource", cm.Name)
		}
		if err := controllerutil.SetControllerReference(o, cm, r.Scheme); err != nil {
			return err
		}
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels["dx.europesip.com/wcm-admin-task"] = o.Name
		cm.Data = map[string]string{}
		size := 0
		for k, v := range reports {
			if size+len(v) > maxOutputBytes {
				cm.Data["README"] = "the reports of the other libraries did not fit in a ConfigMap (1 MiB): set spec.output.oci"
				break
			}
			cm.Data[outputKey(k)+".txt"] = v
			size += len(v)
		}
		return nil
	})
	if err == nil {
		o.Status.ReportConfigMap = cm.Name
	}
	return err
}

// pushReport pushes the reports (one file per library and REPORT-INFO.txt) with the 0.8 annotations.
func (r *DXWCMAdminTaskReconciler) pushReport(ctx context.Context, o *dxv1.DXWCMAdminTask, conn *Connection, reports map[string]string) error {
	oci := o.Spec.Output.OCI
	ref := oci.Repository + ":" + ociTag(oci.Tag, time.Now())
	files := map[string][]byte{}
	var libs []string
	for k, v := range reports {
		files[outputKey(k)+".txt"] = []byte(v)
		libs = append(libs, k)
	}
	info, _ := json.MarshalIndent(map[string]any{
		"resource": o.Namespace + "/" + o.Name, "task": o.Spec.Task, "fix": o.Spec.Fix, "server": conn.Config.BaseURL,
		"virtualPortal": o.Spec.VirtualPortal, "libraries": o.Status.Libraries, "time": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	files["REPORT-INFO.json"] = info
	ann := source.ArtifactMeta{
		Kind: "wcm-admin-report", Title: fmt.Sprintf("WCM %s %s", o.Spec.Task, o.Name),
		Description: fmt.Sprintf("Report of the WCM module %s (fix=%v) of %s/%s", o.Spec.Task, o.Spec.Fix, o.Namespace, o.Name),
		Resource:    o.Namespace + "/" + o.Name, SourceServer: conn.Config.BaseURL, SourceConnection: o.Namespace + "/" + o.Spec.ConnectionRef.Name,
		SourcePortal: o.Spec.VirtualPortal, Libraries: libs, ResourceAnnotations: o.Annotations, Created: time.Now(),
	}.Annotations()
	digest, err := r.Fetcher.Push(ctx, o.Namespace, ref, oci.PushSecrets, files, "text/plain", ann)
	if err != nil {
		return err
	}
	o.Status.OutputRef, o.Status.OutputDigest = ref, digest
	return nil
}

func wcmAdminRefs(list *dxv1.DXWCMAdminTaskList) map[client.ObjectKey][]refersTo {
	out := map[client.ObjectKey][]refersTo{}
	for i := range list.Items {
		o := &list.Items[i]
		rs := []refersTo{{"DXConnection", o.Spec.ConnectionRef.Name}}
		for _, d := range o.Spec.DependsOn {
			rs = append(rs, refersTo{d.Kind, d.Name})
		}
		out[client.ObjectKeyFromObject(o)] = rs
	}
	return out
}

// SetupWithManager sets up the controller.
func (r *DXWCMAdminTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() *dxv1.DXWCMAdminTaskList { return &dxv1.DXWCMAdminTaskList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxv1.DXWCMAdminTask{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&dxv1.DXConnection{}, handler.EnqueueRequestsFromMapFunc(mapReferrers(r.Client, newList, wcmAdminRefs, "DXConnection"))).
		Named("dxwcmadmintask").
		Complete(r)
}
