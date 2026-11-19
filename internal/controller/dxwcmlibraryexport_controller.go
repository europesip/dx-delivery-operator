package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/dx"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/source"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/wcmexport"
)

// DXWCMLibraryExportReconciler exports WCM libraries of DX to an OCI artifact, once per spec.
type DXWCMLibraryExportReconciler struct {
	client.Client
	// APIReader reads straight from the API server (see DXWCMLibraryImportReconciler).
	APIReader client.Reader
	Scheme    *runtime.Scheme
	*Shared
}

// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxwcmlibraryexports,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxwcmlibraryexports/status,verbs=get;update;patch

const (
	defaultExportWait   = time.Hour
	reasonExportFailed  = "ExportFailed"
	reasonPushFailed    = "PushFailed"
	reasonLibNotFound   = "LibraryNotFound"
	exportInfoFile      = "EXPORT-INFO.txt"
	exportZipMediaType  = "application/zip"
	exportInfoMediaType = "text/plain"
)

func exportHash(o *dxv1.DXWCMLibraryExport) string {
	s := o.Spec.DeepCopy()
	s.Suspend, s.DependsOn = false, nil
	b, _ := json.Marshal(s)
	return hashOf(string(b), forceToken(o))
}

func exportTransferDir(o *dxv1.DXWCMLibraryExport) string {
	return "dxop-export-" + unsafeChars.ReplaceAllString(strings.ToLower(o.Namespace+"-"+o.Name), "-")
}

func (r *DXWCMLibraryExportReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Reconcile runs the export when the spec changed since the last finished run.
func (r *DXWCMLibraryExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var o dxv1.DXWCMLibraryExport
	rd := r.reader()
	if err := rd.Get(ctx, req.NamespacedName, &o); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !o.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil // the pushed artifact is kept
	}
	orig := o.DeepCopy()
	if o.Spec.Suspend {
		setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, false, "Suspended", "reconciliation is suspended (spec.suspend)")
		return ctrl.Result{}, r.Status().Patch(ctx, &o, client.MergeFrom(orig))
	}
	hash := exportHash(&o)
	if o.Status.AppliedHash == hash && (o.Status.Phase == dxv1.WCMExportSucceeded || o.Status.Phase == dxv1.WCMExportFailed) {
		return ctrl.Result{}, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.cancelWhenDeleted(runCtx, cancel, req.NamespacedName, rd)
	res, err := r.run(runCtx, &o, hash)
	if ctx.Err() == nil && runCtx.Err() != nil {
		logf.FromContext(ctx).Info("the export was deleted while it ran: stopped", "name", req.NamespacedName.String())
		return ctrl.Result{}, nil
	}
	o.Status.ObservedGeneration = o.Generation
	if uerr := r.saveStatus(ctx, &o); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return res, err
}

func (r *DXWCMLibraryExportReconciler) saveStatus(ctx context.Context, o *dxv1.DXWCMLibraryExport) error {
	var cur dxv1.DXWCMLibraryExport
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(o), &cur); err != nil {
		return err
	}
	cp := cur.DeepCopy()
	cp.Status = *o.Status.DeepCopy()
	return r.Status().Patch(ctx, cp, client.MergeFrom(&cur))
}

func (r *DXWCMLibraryExportReconciler) cancelWhenDeleted(ctx context.Context, cancel context.CancelFunc, key client.ObjectKey, rd client.Reader) {
	t := time.NewTicker(cancelCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var o dxv1.DXWCMLibraryExport
			err := rd.Get(ctx, key, &o)
			if apierrors.IsNotFound(err) || (err == nil && !o.DeletionTimestamp.IsZero()) {
				cancel()
				return
			}
		}
	}
}

func (r *DXWCMLibraryExportReconciler) progress(ctx context.Context, o *dxv1.DXWCMLibraryExport, phase dxv1.WCMExportPhase, msg string) {
	o.Status.Phase, o.Status.Message = phase, msg
	o.Status.ObservedGeneration = o.Generation
	setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, false, string(phase), msg)
	if err := r.saveStatus(ctx, o); err != nil {
		logf.FromContext(ctx).Info("cannot save the progress", "error", err.Error())
	}
}

func (r *DXWCMLibraryExportReconciler) run(ctx context.Context, o *dxv1.DXWCMLibraryExport, hash string) (ctrl.Result, error) {
	gen := o.Generation
	if err := Dependencies(ctx, r.Client, o.Namespace, o.Spec.DependsOn); err != nil {
		if isNotReady(err) {
			setCondition(&o.Status.Conditions, gen, dxv1.ConditionDependenciesReady, false, "Waiting", err.Error())
			setCondition(&o.Status.Conditions, gen, dxv1.ConditionReady, false, "Waiting", err.Error())
			o.Status.Phase = dxv1.WCMExportPending
			return ctrl.Result{RequeueAfter: waitShort}, nil
		}
		return ctrl.Result{}, err
	}
	setCondition(&o.Status.Conditions, gen, dxv1.ConditionDependenciesReady, true, "Ready", "all dependencies are ready")
	conn, err := r.Connection(ctx, r.Client, o.Namespace, o.Spec.ConnectionRef.Name)
	if err != nil {
		if isNotReady(err) {
			setCondition(&o.Status.Conditions, gen, dxv1.ConditionReady, false, "Waiting", err.Error())
			o.Status.Phase = dxv1.WCMExportPending
			return ctrl.Result{RequeueAfter: waitShort}, nil
		}
		return ctrl.Result{}, err
	}
	st := &o.Status
	st.StartTime, st.CompletionTime = now(), nil
	st.Image, st.Digest, st.ExportedLibraries, st.TotalBytes, st.CurrentLibrary = "", "", nil, 0, 0

	err = r.exportAll(ctx, o, conn)
	var p permanent
	switch {
	case err == nil:
		st.Phase, st.CompletionTime, st.AppliedHash, st.CurrentLibrary = dxv1.WCMExportSucceeded, now(), hash, 0
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, true, reasonSucceeded, st.Message)
		r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, reasonSucceeded, "Export", "%s", st.Message)
		return ctrl.Result{}, nil
	case errors.As(err, &p):
		st.Phase, st.Message, st.AppliedHash, st.CompletionTime, st.CurrentLibrary = dxv1.WCMExportFailed, p.err.Error(), hash, now(), 0
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, p.reason, st.Message)
		r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, p.reason, "Export", "%s", st.Message)
		return ctrl.Result{}, nil
	case errors.Is(err, context.Canceled):
		return ctrl.Result{}, err
	default:
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, reasonDXError, err.Error())
		st.Phase, st.Message, st.CurrentLibrary = dxv1.WCMExportPending, err.Error(), 0
		r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, reasonDXError, "Export", "%v", err)
		return ctrl.Result{RequeueAfter: retryFailed}, nil
	}
}

// exportAll does one run: nil (st.Message set), a permanent error, or another error to retry later.
func (r *DXWCMLibraryExportReconciler) exportAll(ctx context.Context, o *dxv1.DXWCMLibraryExport, conn *Connection) error {
	st := &o.Status
	c := conn.Client
	vp := o.Spec.VirtualPortal
	where := "the base portal"
	if vp != "" {
		where = "the virtual portal " + vp
	}
	titles, err := uniqueTitles(o.Spec.Libraries)
	if err != nil {
		return fatal(reasonInvalidSpec, "%v", err)
	}
	for k, v := range o.Spec.Destination.Attributes {
		if !source.AttributeKeyRe.MatchString(k) || len(v) > 256 {
			return fatal(reasonInvalidSpec, "destination.attributes: key %q must be lowercase letters, digits, dots and dashes (at most 63), its value at most 256 characters", k)
		}
	}

	// 1. The libraries must exist (DX would answer LibraryNotFoundException one by one)
	r.progress(ctx, o, dxv1.WCMExportPending, "checking the libraries of "+where)
	libs, err := c.ListLibraries(ctx, vp)
	if err != nil {
		return fmt.Errorf("libraries of %s: %w", where, err)
	}
	have := map[string]string{}
	for _, l := range libs {
		have[strings.ToLower(l.Title)] = l.Title
	}
	var missing []string
	for i, t := range titles {
		if real, ok := have[strings.ToLower(t)]; ok {
			titles[i] = real
		} else {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fatal(reasonLibNotFound, "not in %s: %s; nothing was exported", where, strings.Join(missing, ", "))
	}

	// 2. Export each library into one dxFileTransfer folder (the data module merges them into one JCR tree)
	sub := exportTransferDir(o)
	_ = c.FileTransferDeleteDir(ctx, sub) // leftovers of a run that was cut
	if err := c.FileTransferCreateDir(ctx, sub); err != nil {
		return fmt.Errorf("dxFileTransfer: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := c.FileTransferDeleteDir(cctx, sub); err != nil {
			r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, "CleanupFailed", "Export", "the exported files stay in %s/%s: %v", dx.FileTransferRoot, sub, err)
		}
	}()
	timeout := defaultExportWait
	if o.Spec.Timeout != nil && o.Spec.Timeout.Duration > 0 {
		timeout = o.Spec.Timeout.Duration
	}
	for i, t := range titles {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st.CurrentLibrary = i + 1
		r.progress(ctx, o, dxv1.WCMExportExporting, fmt.Sprintf("library %d/%d: exporting %s from %s", i+1, len(titles), t, where))
		if _, err := c.DataModuleExport(ctx, vp, t, dx.FileTransferRoot+"/"+sub, timeout); err != nil {
			if errors.Is(err, dx.ErrCutByProxy) {
				return fatal(reasonExportFailed, "library %s: %v", t, err)
			}
			if strings.Contains(err.Error(), "DX export failed") {
				return fatal(reasonExportFailed, "library %s: %v", t, err)
			}
			return fmt.Errorf("library %s: %w", t, err)
		}
	}
	st.CurrentLibrary = 0

	// 3. Download the folder as one zip and check it
	work := filepath.Join(r.WorkDir, "wcm-export", o.Namespace+"-"+o.Name)
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(work) //nolint:errcheck
	zipName := "wcm-export-" + unsafeChars.ReplaceAllString(strings.ToLower(o.Name), "-") + ".zip"
	zipPath := filepath.Join(work, zipName)
	r.progress(ctx, o, dxv1.WCMExportDownloading, fmt.Sprintf("downloading the export of %d libraries", len(titles)))
	n, err := c.FileTransferDownload(ctx, sub, zipPath)
	if err != nil {
		return err
	}
	st.TotalBytes = n
	unpacked := filepath.Join(work, "check")
	if _, err := source.ExtractArchiveProgressContext(ctx, zipPath, unpacked, nil); err != nil {
		return fatal(reasonExportFailed, "the zip downloaded from DX cannot be read: %v", err)
	}
	exp, err := wcmexport.Open(unpacked)
	if err != nil {
		return fatal(reasonExportFailed, "the export downloaded from DX: %v", err)
	}
	_ = os.RemoveAll(unpacked)
	got := map[string]wcmexport.Library{}
	for _, l := range exp.Libraries {
		got[strings.ToLower(l.Title)] = l
	}
	for _, t := range titles {
		l, ok := got[strings.ToLower(t)]
		if !ok {
			return fatal(reasonExportFailed, "DX said %s was exported, but it is not in the downloaded export", t)
		}
		st.ExportedLibraries = append(st.ExportedLibraries, dxv1.WCMExportedLibrary{Title: l.Title, Bytes: l.Bytes, Files: l.Files})
	}
	sort.Slice(st.ExportedLibraries, func(i, j int) bool { return st.ExportedLibraries[i].Title < st.ExportedLibraries[j].Title })

	// 4. Push the zip and EXPORT-INFO.txt
	when := time.Now().UTC()
	version := c.ServerVersion(ctx)
	infoPath := filepath.Join(work, exportInfoFile)
	if err := os.WriteFile(infoPath, []byte(exportInfo(conn, o, version, st.ExportedLibraries, n, when)), 0o600); err != nil {
		return err
	}
	r.progress(ctx, o, dxv1.WCMExportPushing, fmt.Sprintf("pushing %s (%d MB) to %s", zipName, n>>20, o.Spec.Destination.Image))
	ann := exportMeta(conn, o, version, titles, when).Annotations()
	digest, err := r.Fetcher.PushFiles(ctx, o.Namespace, o.Spec.Destination.Image, o.Spec.Destination.PushSecrets,
		[]source.ArtifactFile{{Path: zipPath, MediaType: exportZipMediaType}, {Path: infoPath, MediaType: exportInfoMediaType}}, ann)
	if err != nil {
		var te *transport.Error
		if errors.As(err, &te) && (te.StatusCode == http.StatusUnauthorized || te.StatusCode == http.StatusForbidden) {
			return fatal(reasonPushFailed, "%v (check destination.pushSecrets)", err)
		}
		return err
	}
	st.Digest = digest
	if ref, perr := repoOf(o.Spec.Destination.Image); perr == nil {
		st.Image = ref + "@" + digest
	}
	st.Message = fmt.Sprintf("%d libraries exported from %s to %s (%d MB): %s", len(titles), where, o.Spec.Destination.Image, n>>20, strings.Join(titles, ", "))
	return nil
}

func uniqueTitles(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			return nil, errors.New("libraries: empty title")
		}
		if !seen[strings.ToLower(t)] {
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("libraries: at least one")
	}
	return out, nil
}

// exportMeta: the attributes of the artifact (docs/artifact-annotations.md).
func exportMeta(conn *Connection, o *dxv1.DXWCMLibraryExport, version string, titles []string, when time.Time) source.ArtifactMeta {
	return source.ArtifactMeta{
		Kind: "wcm-export", Title: "WCM export " + o.Name,
		Description: fmt.Sprintf("WCM export of %d libraries from %s", len(titles), sourceName(conn, o.Spec.VirtualPortal)),
		Resource: o.Namespace + "/" + o.Name, SourceServer: conn.Config.BaseURL, SourceVersion: version,
		SourceConnection: o.Namespace + "/" + o.Spec.ConnectionRef.Name, SourcePortal: o.Spec.VirtualPortal,
		Libraries: titles, ResourceAnnotations: o.Annotations, Attributes: o.Spec.Destination.Attributes, Created: when,
	}
}

func sourceName(conn *Connection, vp string) string {
	s := conn.Config.BaseURL
	if vp != "" {
		s += " (virtual portal " + vp + ")"
	}
	return s
}

// exportInfo is the text of EXPORT-INFO.txt.
func exportInfo(conn *Connection, o *dxv1.DXWCMLibraryExport, version string, libs []dxv1.WCMExportedLibrary, zipBytes int64, when time.Time) string {
	var b strings.Builder
	vp := o.Spec.VirtualPortal
	if vp == "" {
		vp = "(base portal)"
	}
	if version == "" {
		version = "(not read)"
	}
	fmt.Fprintf(&b, "WCM library export\n\n")
	fmt.Fprintf(&b, "Source server:   %s\n", conn.Config.BaseURL)
	fmt.Fprintf(&b, "Source DX:       %s\n", version)
	fmt.Fprintf(&b, "Connection:      %s/%s\n", o.Namespace, o.Spec.ConnectionRef.Name)
	fmt.Fprintf(&b, "Virtual portal:  %s\n", vp)
	fmt.Fprintf(&b, "Exported at:     %s (UTC)\n", when.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Exported by:     DXWCMLibraryExport %s/%s\n", o.Namespace, o.Name)
	ann := o.Annotations
	who := strings.TrimPrefix(strings.TrimPrefix(ann[source.ResChangedBy], "user:"), "default/")
	if who == "" {
		who = "(not recorded)"
	}
	reason := strings.TrimSpace(ann[source.ResReason])
	if reason == "" {
		reason = "(none given)"
	}
	fmt.Fprintf(&b, "Requested by:    %s\n", who)
	fmt.Fprintf(&b, "Reason:          %s\n", reason)
	fmt.Fprintf(&b, "Operator:        hcl-dx-scriptapp-operator %s\n", source.OperatorVersion())
	if cw := strings.TrimSpace(ann[source.ResCreatedWith]); cw != "" {
		fmt.Fprintf(&b, "Created with:    %s\n", cw)
	} else {
		fmt.Fprintf(&b, "Created with:    the resource itself (kubectl, Git), no Backstage\n")
	}
	if len(o.Spec.Destination.Attributes) > 0 {
		keys := make([]string, 0, len(o.Spec.Destination.Attributes))
		for k := range o.Spec.Destination.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+o.Spec.Destination.Attributes[k])
		}
		fmt.Fprintf(&b, "Attributes:      %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "Libraries:       %d\n", len(libs))
	fmt.Fprintf(&b, "Zip size:        %d bytes\n\n", zipBytes)
	for _, l := range libs {
		fmt.Fprintf(&b, "- %s (%d files, %d bytes)\n", l.Title, l.Files, l.Bytes)
	}
	fmt.Fprintf(&b, `
How to import
-------------
The zip holds the export exactly as the WCM data module of DX writes it: the same JCR folder format as ConfigEngine
export-wcm-data. Do not rename its folders or files. Import it into a DX of the same CF level as the source
(%s) or a newer one.

1. HCL DX Compose: a DXWCMLibraryImport whose source is
     image:
       ref: %s
   (the operator unpacks the zip; choose libraries with spec.libraries).

2. Traditional HCL DX (WebSphere, 8.5 / 9.0 / 9.5): unzip it on the DX server, for example into /tmp/wcm-import, and
   run as the DX user
     ./ConfigEngine.sh import-wcm-data -Dimport.directory=/tmp/wcm-import \
       -DWasPassword=... -DPortalAdminPwd=...
   (add -DVirtualPortalContext=<context> for a virtual portal). import-wcm-data has no library name property: it
   imports every library of the folder. Or, signed in to the portal, open the WCM data module:
     /wps/wcm/myconnect[/<virtual portal>]?MOD=data&processLibraries=false&taskType=import&input.dir=/tmp/wcm-import
`, version, o.Spec.Destination.Image)
	return b.String()
}

func wcmExportRefs(list *dxv1.DXWCMLibraryExportList) map[client.ObjectKey][]refersTo {
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
func (r *DXWCMLibraryExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() *dxv1.DXWCMLibraryExportList { return &dxv1.DXWCMLibraryExportList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxv1.DXWCMLibraryExport{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&dxv1.DXConnection{}, handler.EnqueueRequestsFromMapFunc(mapReferrers(r.Client, newList, wcmExportRefs, "DXConnection"))).
		Named("dxwcmlibraryexport").
		Complete(r)
}

// repoOf is the repository of an image reference (without tag or digest).
func repoOf(ref string) (string, error) {
	r, err := name.ParseReference(ref)
	if err != nil {
		return "", err
	}
	return r.Context().String(), nil
}
