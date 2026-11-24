// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// DXXMLAccessReconciler runs XMLAccess requests against DX, once per change of the spec or of its source (like a Job).
type DXXMLAccessReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	*Shared
}

// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxxmlaccesses,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxxmlaccesses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxxmlaccesses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

const (
	xmlInlineName         = "request.xml"
	defaultXMLTimeout     = 10 * time.Minute
	xmlCleanupGiveUp      = 10 * time.Minute
	maxOutputBytes        = 900 << 10 // under the 1 MiB of a ConfigMap
	reasonXMLFailed       = "RequestFailed"
	reasonXMLPartial      = "SucceededWithErrors"
	reasonXMLInvalid      = "InvalidRequest"
	reasonXMLOutputFailed = "OutputFailed"
	reasonXMLOutputLarge  = "OutputTooLarge"
)

var (
	xmlVariable    = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	xmlRequestType = regexp.MustCompile(`<request\b[^>]*\btype="([A-Za-z-]+)"`)
	unsafeKey      = regexp.MustCompile(`[^-._a-zA-Z0-9]+`)
)

// xmlRequest is one request to run.
type xmlRequest struct {
	File string
	Body string
	Type string
}

// xmlHash identifies a run: the spec (without suspend, dependsOn and cleanup) and the reconcile annotation. The
// source revision is added once the source is fetched (a new image digest or archive is a new run).
func xmlHash(o *dxv1.DXXMLAccess, revision string) string {
	s := o.Spec.DeepCopy()
	s.Suspend, s.DependsOn, s.Cleanup = false, nil, nil
	b, _ := json.Marshal(s)
	return hashOf(string(b), revision, forceToken(o))
}

// substitute replaces ${NAME} with the variables; unknown names are left as they are.
func substitute(body string, vars map[string]string) string {
	if len(vars) == 0 {
		return body
	}
	return xmlVariable.ReplaceAllStringFunc(body, func(m string) string {
		if v, ok := vars[m[2:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// checkRequest says whether the text is a well-formed XMLAccess request and returns its type.
func checkRequest(body string) (string, error) {
	m := xmlRequestType.FindStringSubmatch(body)
	if m == nil {
		return "", errors.New(`not an XMLAccess request: no <request type="..."> element`)
	}
	d := xml.NewDecoder(strings.NewReader(body))
	d.Strict = true
	for {
		if _, err := d.Token(); err != nil {
			if err == io.EOF {
				return m[1], nil
			}
			return "", fmt.Errorf("not well-formed XML: %v", err)
		}
	}
}

// requestFiles lists the requests of a source folder: the given files in that order, or every *.xml sorted by path.
func requestFiles(dir string, files []string) ([]string, error) {
	if len(files) > 0 {
		out := make([]string, 0, len(files))
		for _, f := range files {
			clean := filepath.Clean("/" + f)[1:]
			if clean == "" || strings.HasPrefix(clean, "..") {
				return nil, fmt.Errorf("file %q: a path inside the source", f)
			}
			if _, err := os.Stat(filepath.Join(dir, clean)); err != nil {
				return nil, fmt.Errorf("file %q is not in the source", f)
			}
			out = append(out, clean)
		}
		return out, nil
	}
	var out []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && strings.EqualFold(filepath.Ext(p), ".xml") {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, errors.New("the source has no .xml files")
	}
	return out, nil
}

// ObjectIDsNew: spec.objectIDs value that makes DX give new object IDs.
const ObjectIDsNew = "New"

// objectIDs applies spec.objectIDs to a request of the run: New sets create-oids="true" on its <request> elements;
// Keep (default) sends it as it is. The cleanup request (only != "") is sent as written.
func objectIDs(o *dxv1.DXXMLAccess, only, body string) string {
	if o.Spec.ObjectIDs == ObjectIDsNew && only == "" {
		return dx.SetCreateOIDs(body)
	}
	return body
}

// requests reads the requests of the spec (inline or from the source) with the variables replaced. The returned
// revision identifies the source (empty for inline XML).
func (r *DXXMLAccessReconciler) requests(ctx context.Context, o *dxv1.DXXMLAccess, only string) ([]xmlRequest, string, error) {
	if o.Spec.Source == nil || only == xmlInlineName {
		body := o.Spec.XML
		if only == xmlInlineName && o.Spec.Cleanup != nil {
			body = o.Spec.Cleanup.XML
		}
		return []xmlRequest{{File: xmlInlineName, Body: objectIDs(o, only, substitute(body, o.Spec.Variables))}}, "", nil
	}
	tree, err := r.Fetcher.Fetch(ctx, o.Namespace, *o.Spec.Source)
	if err != nil {
		var nf *source.NoFilesError
		if errors.As(err, &nf) { // a wrong folder: retrying cannot fix it, the spec must change
			return nil, "", permanent{reason: reasonSourceError, err: fmt.Errorf("source: %w; nothing was sent to DX", err)}
		}
		return nil, "", fmt.Errorf("source: %w", err)
	}
	files := o.Spec.Files
	if only != "" {
		files = []string{only}
	}
	names, err := requestFiles(tree.Dir, files)
	if err != nil {
		return nil, tree.Revision, permanent{reason: reasonXMLInvalid, err: err}
	}
	out := make([]xmlRequest, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(tree.Dir, n))
		if err != nil {
			return nil, tree.Revision, err
		}
		out = append(out, xmlRequest{File: n, Body: objectIDs(o, only, substitute(string(b), o.Spec.Variables))})
	}
	return out, tree.Revision, nil
}

// Reconcile runs the requests when the spec or the source changed since the last finished run.
func (r *DXXMLAccessReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var o dxv1.DXXMLAccess
	if err := r.Get(ctx, req.NamespacedName, &o); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !o.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &o)
	}
	if o.Spec.Cleanup != nil {
		if controllerutil.AddFinalizer(&o, Finalizer) {
			if err := r.Update(ctx, &o); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else if controllerutil.RemoveFinalizer(&o, Finalizer) {
		if err := r.Update(ctx, &o); err != nil {
			return ctrl.Result{}, err
		}
	}
	orig := o.DeepCopy()
	if o.Spec.Suspend {
		setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, false, "Suspended", "reconciliation is suspended (spec.suspend)")
		return ctrl.Result{}, r.Status().Patch(ctx, &o, client.MergeFrom(orig))
	}
	res, err := r.run(ctx, &o)
	o.Status.ObservedGeneration = o.Generation
	if perr := r.Status().Patch(ctx, &o, client.MergeFrom(orig)); perr != nil {
		return ctrl.Result{}, perr
	}
	return res, err
}

func (r *DXXMLAccessReconciler) run(ctx context.Context, o *dxv1.DXXMLAccess) (ctrl.Result, error) {
	gen := o.Generation
	st := &o.Status
	pending := func(reason, msg string, after time.Duration) (ctrl.Result, error) {
		st.Phase, st.Message = dxv1.XMLAccessPending, msg
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, reason, msg)
		return ctrl.Result{RequeueAfter: after}, nil
	}

	// Inline XML: nothing to fetch, so a finished run with the same hash is known before anything else
	if o.Spec.Source == nil && st.AppliedHash == xmlHash(o, "") && (st.Phase == dxv1.XMLAccessSucceeded || st.Phase == dxv1.XMLAccessFailed) {
		return ctrl.Result{}, nil
	}
	if err := Dependencies(ctx, r.Client, o.Namespace, o.Spec.DependsOn); err != nil {
		if isNotReady(err) {
			setCondition(&st.Conditions, gen, dxv1.ConditionDependenciesReady, false, "Waiting", err.Error())
			return pending("Waiting", err.Error(), waitShort)
		}
		return ctrl.Result{}, err
	}
	setCondition(&st.Conditions, gen, dxv1.ConditionDependenciesReady, true, "Ready", "all dependencies are ready")

	reqs, rev, err := r.requests(ctx, o, "")
	hash := xmlHash(o, rev)
	if st.AppliedHash == hash && (st.Phase == dxv1.XMLAccessSucceeded || st.Phase == dxv1.XMLAccessFailed) {
		return ctrl.Result{}, nil // done for this spec and source: one-shot
	}
	finish := func(phase dxv1.XMLAccessPhase, ready bool, reason, msg string) (ctrl.Result, error) {
		st.Phase, st.Message, st.AppliedHash, st.CompletionTime, st.SourceRevision = phase, msg, hash, now(), rev
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, ready, reason, msg)
		typ := corev1.EventTypeNormal
		if !ready {
			typ = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(o, nil, typ, reason, "XMLAccess", "%s", msg)
		return ctrl.Result{}, nil
	}
	var p permanent
	if errors.As(err, &p) {
		st.Results = nil
		return finish(dxv1.XMLAccessFailed, false, p.reason, p.err.Error())
	}
	if err != nil {
		r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, reasonSourceError, "XMLAccess", "%v", err)
		return pending(reasonSourceError, err.Error(), retryFailed)
	}

	// Check every request before running any: a broken file must not leave half of a change applied
	st.Results, st.DryRun, st.StartTime, st.CompletionTime = nil, o.Spec.DryRun, now(), nil
	for i := range reqs {
		t, cerr := checkRequest(reqs[i].Body)
		if cerr != nil {
			st.Results = append(st.Results, dxv1.XMLAccessFileResult{File: reqs[i].File, Result: "error", Message: cerr.Error()})
			return finish(dxv1.XMLAccessFailed, false, reasonXMLInvalid, fmt.Sprintf("%s: %v; nothing was sent to DX", reqs[i].File, cerr))
		}
		reqs[i].Type = t
	}
	where := "the base portal"
	vp := ""
	if o.Spec.VirtualPortal != nil {
		vp = o.Spec.VirtualPortal.Context
		where = "the virtual portal " + vp
	}
	if o.Spec.DryRun {
		names := make([]string, 0, len(reqs))
		for _, q := range reqs {
			st.Results = append(st.Results, dxv1.XMLAccessFileResult{File: q.File, Type: q.Type, Result: "checked"})
			names = append(names, fmt.Sprintf("%s (%s)", q.File, q.Type))
		}
		return finish(dxv1.XMLAccessSucceeded, true, reasonDryRun, fmt.Sprintf("dry run: would send %d request(s) to %s: %s", len(reqs), where, strings.Join(names, ", ")))
	}

	conn, err := r.Connection(ctx, r.Client, o.Namespace, o.Spec.ConnectionRef.Name)
	if err != nil {
		if isNotReady(err) {
			return pending("Waiting", err.Error(), waitShort)
		}
		return ctrl.Result{}, err
	}
	st.Phase, st.Message = dxv1.XMLAccessRunning, fmt.Sprintf("sending %d request(s) to %s", len(reqs), where)
	setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, string(dxv1.XMLAccessRunning), st.Message)

	timeout := defaultXMLTimeout
	if o.Spec.Timeout != nil && o.Spec.Timeout.Duration > 0 {
		timeout = o.Spec.Timeout.Duration
	}
	outputs := map[string]string{}
	failed, stopped := 0, false
	for _, q := range reqs {
		if stopped {
			st.Results = append(st.Results, dxv1.XMLAccessFileResult{File: q.File, Type: q.Type, Result: "skipped", Message: "not sent: an earlier request failed (continueOnError: false)"})
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, timeout)
		res, xerr := conn.Client.XMLAccessAt(rctx, vp, q.Body)
		cancel()
		if errors.Is(xerr, context.Canceled) && ctx.Err() != nil {
			return ctrl.Result{}, ctx.Err()
		}
		ok, msg := dx.XMLAccessOK(res)
		if xerr != nil {
			ok, msg = false, xerr.Error()
		}
		if res != "" {
			outputs[q.File] = res
		}
		item := dxv1.XMLAccessFileResult{File: q.File, Type: q.Type, Result: "ok"}
		if !ok {
			item.Result, item.Message = "error", truncate(msg, 500)
			failed++
			stopped = !o.Spec.ContinueOnError
		}
		st.Results = append(st.Results, item)
	}
	st.OutputRef, st.OutputDigest = "", ""
	if out := o.Spec.Output; out != nil {
		// The registry first: it has no size limit, so the ConfigMap can point to it when the answers do not fit
		if out.OCI != nil {
			if oerr := r.pushOutput(ctx, o, conn.Client.Config().BaseURL, vp, outputs); oerr != nil {
				return finish(dxv1.XMLAccessFailed, false, reasonXMLOutputFailed, fmt.Sprintf("the requests ran (%d failed) but the output was not pushed: %v", failed, oerr))
			}
		}
		if out.ConfigMap != nil {
			size, oerr := r.saveOutput(ctx, o, outputs)
			if oerr != nil {
				return finish(dxv1.XMLAccessFailed, false, reasonXMLOutputFailed, fmt.Sprintf("the requests ran (%d failed) but the output was not saved: %v", failed, oerr))
			}
			if size > maxOutputBytes && out.OCI == nil {
				return finish(dxv1.XMLAccessFailed, false, reasonXMLOutputLarge, fmt.Sprintf(
					"the requests ran (%d failed) but DX's answers are %d KiB and a ConfigMap holds at most 1 MiB: nothing was kept; set output.oci (a registry, no size limit)", failed, size>>10))
			}
		}
	}
	switch {
	case failed == 0:
		return finish(dxv1.XMLAccessSucceeded, true, reasonSucceeded, fmt.Sprintf("%d request(s) sent to %s, all ok", len(reqs), where))
	case o.Spec.ContinueOnError && failed < len(reqs):
		return finish(dxv1.XMLAccessFailed, false, reasonXMLPartial, fmt.Sprintf("%d of %d request(s) failed in %s; first: %s", failed, len(reqs), where, firstError(st.Results)))
	default:
		return finish(dxv1.XMLAccessFailed, false, reasonXMLFailed, fmt.Sprintf("request failed in %s: %s", where, firstError(st.Results)))
	}
}

func firstError(rs []dxv1.XMLAccessFileResult) string {
	for _, x := range rs {
		if x.Result == "error" {
			return x.File + ": " + x.Message
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// outputKey turns a file path into a ConfigMap key ("a/b.xml" -> "a__b.xml").
func outputKey(file string) string {
	return unsafeKey.ReplaceAllString(strings.ReplaceAll(file, "/", "__"), "_")
}

// saveOutput writes DX's answers to the output ConfigMap (owned by the resource) and returns their size. Answers that
// do not fit in a ConfigMap are never cut: the ConfigMap then only holds README, which says where they are (the
// registry of output.oci) or why they were not kept.
func (r *DXXMLAccessReconciler) saveOutput(ctx context.Context, o *dxv1.DXXMLAccess, outputs map[string]string) (int, error) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: o.Spec.Output.ConfigMap.Name, Namespace: o.Namespace}}
	size := 0
	for _, v := range outputs {
		size += len(v)
	}
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
		cm.Labels["dx.europesip.com/xmlaccess"] = o.Name
		cm.Data = map[string]string{}
		if size > maxOutputBytes {
			where := "they were not kept: set output.oci (a registry, no size limit)"
			if o.Status.OutputRef != "" {
				where = fmt.Sprintf("they are in %s (%s): oras pull %s", o.Status.OutputRef, o.Status.OutputDigest, o.Status.OutputRef)
			}
			cm.Data["README"] = fmt.Sprintf("DX's answers are %d KiB and a ConfigMap holds at most 1 MiB, so %s\n", size>>10, where)
			return nil
		}
		for k, v := range outputs {
			cm.Data[outputKey(k)] = v
		}
		return nil
	})
	return size, err
}

// ociTag is the tag of a run: output.oci.tag with ${DATE} replaced (default ${DATE}).
func ociTag(tag string, t time.Time) string {
	if tag == "" {
		tag = "${DATE}"
	}
	return strings.ReplaceAll(tag, "${DATE}", t.UTC().Format("20060102-150405"))
}

// pushOutput pushes DX's answers and results.json to the registry as an OCI artifact (one file per request).
func (r *DXXMLAccessReconciler) pushOutput(ctx context.Context, o *dxv1.DXXMLAccess, server, vp string, outputs map[string]string) error {
	oci := o.Spec.Output.OCI
	ref := oci.Repository + ":" + ociTag(oci.Tag, time.Now())
	files := map[string][]byte{}
	for k, v := range outputs {
		files[k] = []byte(v)
	}
	summary, _ := json.MarshalIndent(map[string]any{
		"resource": o.Namespace + "/" + o.Name, "server": server, "virtualPortal": vp, "results": o.Status.Results,
		"sourceRevision": o.Status.SourceRevision, "time": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	files["results.json"] = summary
	ann := source.ArtifactMeta{
		Kind: source.KindXMLAccess, Title: "XMLAccess answers " + o.Name, Description: "Answers of DX to the XMLAccess requests of " + o.Namespace + "/" + o.Name,
		Resource: o.Namespace + "/" + o.Name, SourceServer: o.Spec.ConnectionRef.Name, SourceURL: server, SourceConnection: o.Namespace + "/" + o.Spec.ConnectionRef.Name,
		SourcePortal: vp, ResourceAnnotations: o.Annotations, Created: time.Now(),
	}.Annotations()
	// the names of 0.6 and earlier, kept for whoever reads them
	ann["dx.europesip.com/resource"], ann["dx.europesip.com/virtual-portal"] = o.Namespace+"/"+o.Name, vp
	digest, err := r.Fetcher.Push(ctx, o.Namespace, ref, oci.PushSecrets, files, "application/xml", ann)
	if err != nil {
		return err
	}
	o.Status.OutputRef, o.Status.OutputDigest = ref, digest
	r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, "OutputPushed", "XMLAccess", "answers pushed to %s (%s)", ref, digest)
	return nil
}

// finalize runs the cleanup request (if any) and lets the resource go. It never blocks the deletion for long: a DX
// that does not answer for 10 minutes, or a cleanup that fails, is reported and the resource is removed anyway.
func (r *DXXMLAccessReconciler) finalize(ctx context.Context, o *dxv1.DXXMLAccess) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(o, Finalizer) {
		return ctrl.Result{}, nil
	}
	giveUp := time.Since(o.DeletionTimestamp.Time) > xmlCleanupGiveUp
	if c := o.Spec.Cleanup; c != nil && (c.XML != "" || c.File != "") && !o.Spec.DryRun && o.Status.AppliedHash != "" {
		conn, err := r.Connection(ctx, r.Client, o.Namespace, o.Spec.ConnectionRef.Name)
		switch {
		case err != nil && !giveUp:
			log.Info("cleanup waits for the connection", "reason", err.Error())
			return ctrl.Result{RequeueAfter: waitShort}, nil
		case err != nil:
			r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, "CleanupSkipped", "Delete", "no connection to DX for %s: cleanup not run (%v)", xmlCleanupGiveUp, err)
		default:
			only := xmlInlineName
			if c.XML == "" {
				only = c.File
			}
			reqs, _, rerr := r.requests(ctx, o, only)
			vp := ""
			if o.Spec.VirtualPortal != nil {
				vp = o.Spec.VirtualPortal.Context
			}
			if rerr == nil {
				var res string
				res, rerr = conn.Client.XMLAccessAt(ctx, vp, reqs[0].Body)
				if ok, msg := dx.XMLAccessOK(res); rerr == nil && !ok {
					rerr = errors.New(msg)
				}
			}
			if rerr != nil && !giveUp {
				r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, "CleanupFailed", "Delete", "%v (retried until %s after the deletion)", rerr, xmlCleanupGiveUp)
				return ctrl.Result{RequeueAfter: retryFailed}, nil
			}
			if rerr != nil {
				r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, "CleanupFailed", "Delete", "%v: giving up, the resource is removed", rerr)
			} else {
				r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, "CleanedUp", "Delete", "cleanup request sent")
			}
		}
	}
	controllerutil.RemoveFinalizer(o, Finalizer)
	if err := r.Update(ctx, o); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func xmlAccessRefs(list *dxv1.DXXMLAccessList) map[client.ObjectKey][]refersTo {
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

// SetupWithManager sets up the controller. Only spec and annotation changes start a run; a change of the DXConnection
// wakes the resources that are waiting for it (a finished run is not repeated).
func (r *DXXMLAccessReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() *dxv1.DXXMLAccessList { return &dxv1.DXXMLAccessList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxv1.DXXMLAccess{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&dxv1.DXConnection{}, handler.EnqueueRequestsFromMapFunc(mapReferrers(r.Client, newList, xmlAccessRefs, "DXConnection"))).
		Named("dxxmlaccess").
		Complete(r)
}
