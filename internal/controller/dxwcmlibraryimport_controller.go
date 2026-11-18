package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
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

// DXWCMLibraryImportReconciler imports WCM libraries from a JCR export into DX, once per spec.
type DXWCMLibraryImportReconciler struct {
	client.Client
	// APIReader reads straight from the API server. An import is long and saves its status several times: an event
	// that arrives right after it (a change of the DXConnection) must not see the cached object of before the end of
	// the run, think it is still pending and import everything again.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	*Shared
}

// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxwcmlibraryimports,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxwcmlibraryimports/status,verbs=get;update;patch

const (
	defaultMaxChunk     = int64(450 << 20)
	defaultImportWait   = 2 * time.Hour
	defaultRealm        = "defaultWIMFileBasedRealm"
	vpReadyWait         = 10 * time.Minute
	reasonSucceeded     = "Succeeded"
	reasonDryRun        = "DryRun"
	reasonWarnings      = "SucceededWithWarnings"
	reasonInvalidSpec   = "InvalidSpec"
	reasonVPNotFound    = "VirtualPortalNotFound"
	reasonImportFailed  = "ImportFailed"
	reasonNothingToDo   = "NothingToImport"
	reasonSourceError   = "SourceError"
	reasonDXError       = "DXError"
	reasonExportInvalid = "InvalidExport"
)

// permanent errors end the run (Failed) until the spec changes; the others are retried.
type permanent struct {
	reason string
	err    error
}

func (p permanent) Error() string { return p.err.Error() }

func fatal(reason string, format string, a ...any) error {
	return permanent{reason: reason, err: fmt.Errorf(format, a...)}
}

// importHash identifies a run: the spec (without suspend and dependsOn) and the reconcile annotation. A changed source
// sha256 / image reference is a changed spec.
func importHash(o *dxv1.DXWCMLibraryImport) string {
	s := o.Spec.DeepCopy()
	s.Suspend, s.DependsOn = false, nil
	b, _ := json.Marshal(s)
	return hashOf(string(b), forceToken(o))
}

var unsafeChars = regexp.MustCompile(`[^a-z0-9-]+`)

// transferDir is the dxFileTransfer folder of a resource (one per resource; emptied after every run).
func transferDir(o *dxv1.DXWCMLibraryImport) string {
	return "dxop-" + unsafeChars.ReplaceAllString(strings.ToLower(o.Namespace+"-"+o.Name), "-")
}

// Reconcile runs the import when the spec changed since the last finished run.
func (r *DXWCMLibraryImportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var o dxv1.DXWCMLibraryImport
	var rd client.Reader = r.Client
	if r.APIReader != nil {
		rd = r.APIReader
	}
	if err := rd.Get(ctx, req.NamespacedName, &o); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !o.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil // libraries are never deleted from DX
	}
	orig := o.DeepCopy()
	if o.Spec.Suspend {
		setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, false, "Suspended", "reconciliation is suspended (spec.suspend)")
		return ctrl.Result{}, r.Status().Patch(ctx, &o, client.MergeFrom(orig))
	}
	hash := importHash(&o)
	if o.Status.AppliedHash == hash && (o.Status.Phase == dxv1.WCMImportSucceeded || o.Status.Phase == dxv1.WCMImportFailed) {
		return ctrl.Result{}, nil // done for this spec: one-shot
	}
	res, err := r.run(ctx, &o, hash)
	o.Status.ObservedGeneration = o.Generation
	if uerr := r.saveStatus(ctx, &o); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return res, err
}

// saveStatus writes the status of o as it is in memory: a merge patch from the status stored in the API server (so that
// fields cleared by the run are removed too), applied to a copy (the answer must not replace fields of o).
func (r *DXWCMLibraryImportReconciler) saveStatus(ctx context.Context, o *dxv1.DXWCMLibraryImport) error {
	var cur dxv1.DXWCMLibraryImport
	var rd client.Reader = r.Client
	if r.APIReader != nil {
		rd = r.APIReader
	}
	if err := rd.Get(ctx, client.ObjectKeyFromObject(o), &cur); err != nil {
		return err
	}
	cp := cur.DeepCopy()
	cp.Status = *o.Status.DeepCopy()
	return r.Status().Patch(ctx, cp, client.MergeFrom(&cur))
}

// progress saves the status during a long run, so that kubectl and Argo CD show the step.
func (r *DXWCMLibraryImportReconciler) progress(ctx context.Context, o *dxv1.DXWCMLibraryImport, phase dxv1.WCMImportPhase, msg string) {
	o.Status.Phase, o.Status.Message = phase, msg
	o.Status.ObservedGeneration = o.Generation
	setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, false, string(phase), msg)
	if err := r.saveStatus(ctx, o); err != nil {
		logf.FromContext(ctx).Info("cannot save the progress", "error", err.Error())
	}
}

func (r *DXWCMLibraryImportReconciler) run(ctx context.Context, o *dxv1.DXWCMLibraryImport, hash string) (ctrl.Result, error) {
	gen := o.Generation
	finish := func(phase dxv1.WCMImportPhase, ready bool, reason, msg string) {
		o.Status.Phase, o.Status.Message, o.Status.AppliedHash, o.Status.CompletionTime, o.Status.CurrentChunk = phase, msg, hash, now(), 0
		setCondition(&o.Status.Conditions, gen, dxv1.ConditionReady, ready, reason, msg)
		typ := corev1.EventTypeNormal
		if !ready {
			typ = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(o, nil, typ, reason, "Import", "%s", msg)
	}
	retry := func(reason string, err error) (ctrl.Result, error) {
		setCondition(&o.Status.Conditions, gen, dxv1.ConditionReady, false, reason, err.Error())
		o.Status.Phase, o.Status.Message = dxv1.WCMImportPending, err.Error()
		if reason != "Waiting" {
			r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, reason, "Import", "%v", err)
		}
		return ctrl.Result{RequeueAfter: retryFailed}, nil
	}

	if err := Dependencies(ctx, r.Client, o.Namespace, o.Spec.DependsOn); err != nil {
		if isNotReady(err) {
			setCondition(&o.Status.Conditions, gen, dxv1.ConditionDependenciesReady, false, "Waiting", err.Error())
			setCondition(&o.Status.Conditions, gen, dxv1.ConditionReady, false, "Waiting", err.Error())
			o.Status.Phase = dxv1.WCMImportPending
			return ctrl.Result{RequeueAfter: waitShort}, nil
		}
		return ctrl.Result{}, err
	}
	setCondition(&o.Status.Conditions, gen, dxv1.ConditionDependenciesReady, true, "Ready", "all dependencies are ready")
	conn, err := r.Connection(ctx, r.Client, o.Namespace, o.Spec.ConnectionRef.Name)
	if err != nil {
		if isNotReady(err) {
			setCondition(&o.Status.Conditions, gen, dxv1.ConditionReady, false, "Waiting", err.Error())
			o.Status.Phase = dxv1.WCMImportPending
			return ctrl.Result{RequeueAfter: waitShort}, nil
		}
		return ctrl.Result{}, err
	}

	// A new run: reset the results of the previous one.
	st := &o.Status
	st.StartTime, st.CompletionTime = now(), nil
	st.ImportedLibraries, st.SkippedLibraries, st.FailedLibraries = nil, nil, nil
	st.Chunks, st.CurrentChunk, st.LibrariesInExport, st.DryRun = 0, 0, 0, o.Spec.DryRun
	st.ExportLibraries = nil

	err = r.importAll(ctx, o, conn)
	var p permanent
	switch {
	case err == nil:
		return ctrl.Result{}, nil
	case errors.As(err, &p):
		finish(dxv1.WCMImportFailed, false, p.reason, p.err.Error())
		return ctrl.Result{}, nil
	case errors.Is(err, context.Canceled):
		return ctrl.Result{}, err
	default:
		return retry(reasonDXError, err)
	}
}

// importAll does one run. It returns nil after it has set the final status itself, a permanent error for a run that
// must not be retried, or another error to retry later (nothing was imported yet).
func (r *DXWCMLibraryImportReconciler) importAll(ctx context.Context, o *dxv1.DXWCMLibraryImport, conn *Connection) error {
	st := &o.Status
	c := conn.Client
	vp := ""
	if o.Spec.VirtualPortal != nil {
		vp = o.Spec.VirtualPortal.Context
	}
	where := "the base portal"
	if vp != "" {
		where = "the virtual portal " + vp
	}

	// 1. The virtual portal, before anything else
	vpMissing := false
	if vp != "" {
		r.progress(ctx, o, dxv1.WCMImportPending, "checking the virtual portal "+vp)
		vps, err := c.VirtualPortals(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, v := range vps {
			if strings.EqualFold(v.Context, vp) {
				found = true
			}
		}
		if !found {
			if !o.Spec.VirtualPortal.Create {
				return fatal(reasonVPNotFound, "the virtual portal %q does not exist in DX (set virtualPortal.create: true to create it); nothing was imported", vp)
			}
			vpMissing = true
			if !o.Spec.DryRun {
				if err := r.createVirtualPortal(ctx, o, c, vps); err != nil {
					return err
				}
				vpMissing = false
			}
		}
	}

	// 2. The export
	r.progress(ctx, o, dxv1.WCMImportFetching, "getting the export")
	tree, err := r.Fetcher.Fetch(ctx, o.Namespace, o.Spec.Source)
	if err != nil {
		var nf *source.NoFilesError
		if errors.As(err, &nf) { // a wrong folder: retrying cannot fix it, the spec must change
			return fatal(reasonSourceError, "source: %v; nothing was imported", err)
		}
		return fmt.Errorf("source: %w", err)
	}
	st.SourceRevision = tree.Revision
	defer os.RemoveAll(tree.Dir) //nolint:errcheck // exports are big: never kept in the cache after a run
	exp, err := wcmexport.Open(tree.Dir)
	if err != nil {
		// The export as the customer delivers it: one archive (the .tar.bz2 / .zip of export-wcm-data) inside the
		// source, for example an ORAS artifact in Harbor. Unpack it and look again.
		archive, aerr := singleArchive(tree.Dir)
		if aerr != nil {
			return fatal(reasonExportInvalid, "%v", err)
		}
		r.progress(ctx, o, dxv1.WCMImportFetching, "unpacking "+filepath.Base(archive))
		unpacked := filepath.Join(r.WorkDir, "wcm-import", o.Namespace+"-"+o.Name+"-export")
		_ = os.RemoveAll(unpacked)
		defer os.RemoveAll(unpacked) //nolint:errcheck
		if _, xerr := source.ExtractArchive(archive, unpacked); xerr != nil {
			return fatal(reasonExportInvalid, "unpacking %s: %v", filepath.Base(archive), xerr)
		}
		if exp, err = wcmexport.Open(unpacked); err != nil {
			return fatal(reasonExportInvalid, "%s: %v", filepath.Base(archive), err)
		}
	}
	st.LibrariesInExport = len(exp.Libraries)
	sel, left, err := wcmexport.Select(exp.Libraries, o.Spec.Libraries.Include, o.Spec.Libraries.Exclude, o.Spec.Libraries.ExcludeHCLDefaults)
	if err != nil {
		return fatal(reasonInvalidSpec, "%v; nothing was imported", err)
	}
	for t, why := range left {
		st.SkippedLibraries = append(st.SkippedLibraries, dxv1.WCMLibraryResult{Title: t, Reason: why})
	}

	// 3. What the target already has
	existing := map[string]bool{}
	if !vpMissing {
		libs, err := c.ListLibraries(ctx, vp)
		if err != nil {
			return fmt.Errorf("libraries of %s: %w", where, err)
		}
		for _, l := range libs {
			existing[strings.ToLower(l.Title)] = true
		}
	}
	var todo []wcmexport.Library
	var unsafe []dxv1.WCMLibraryResult
	plans := map[string]dxv1.WCMExportLibrary{} // by title: what this run does with each library of the export
	for t, why := range left {
		plans[t] = dxv1.WCMExportLibrary{Plan: dxv1.WCMPlanNotSelected, Reason: why}
	}
	for _, l := range sel {
		if l.IsPortalSite() && !o.Spec.PortalSiteLibraryOverwrite {
			why := "Portal Site is never imported unless portalSiteLibraryOverwrite: true"
			st.SkippedLibraries = append(st.SkippedLibraries, dxv1.WCMLibraryResult{Title: l.Title, Reason: why})
			plans[l.Title] = dxv1.WCMExportLibrary{Plan: dxv1.WCMPlanSkip, Reason: why}
			continue
		}
		if len(l.UnsafePaths) > 0 {
			ex := l.UnsafePaths
			if len(ex) > 3 {
				ex = append(ex[:3:3], fmt.Sprintf("and %d more", len(l.UnsafePaths)-3))
			}
			unsafe = append(unsafe, dxv1.WCMLibraryResult{Title: l.Title, Reason: "not uploaded: DX dxFileTransfer rejects file or folder names with spaces or " +
				"special characters, and they cannot be renamed (DX finds the values of an item by its name): " + strings.Join(ex, ", ") +
				". Rename those items in the source portal and export again"})
			plans[l.Title] = dxv1.WCMExportLibrary{Plan: dxv1.WCMPlanCannotUpload, Reason: unsafe[len(unsafe)-1].Reason}
			continue
		}
		if existing[strings.ToLower(l.Title)] && !o.Spec.Overwrite {
			why := "already exists in " + where + " (overwrite: false)"
			st.SkippedLibraries = append(st.SkippedLibraries, dxv1.WCMLibraryResult{Title: l.Title, Reason: why})
			plans[l.Title] = dxv1.WCMExportLibrary{Plan: dxv1.WCMPlanSkip, Reason: why}
			continue
		}
		plans[l.Title] = dxv1.WCMExportLibrary{Plan: dxv1.WCMPlanImport}
		todo = append(todo, l)
	}
	sort.Slice(st.SkippedLibraries, func(i, j int) bool { return st.SkippedLibraries[i].Title < st.SkippedLibraries[j].Title })
	st.FailedLibraries = append(st.FailedLibraries, unsafe...)
	st.ExportLibraries = exportLibraries(exp.Libraries, plans, existing)
	maxChunk := defaultMaxChunk
	if q := o.Spec.Upload.MaxChunkSize; q != nil && q.Value() > 0 {
		maxChunk = q.Value()
	}
	if maxChunk > dx.MaxUploadBytes {
		maxChunk = dx.MaxUploadBytes
	}
	maxFiles := 4000
	if o.Spec.Upload.MaxChunkFiles != nil {
		maxFiles = int(*o.Spec.Upload.MaxChunkFiles)
	}
	chunks := wcmexport.Chunk(todo, maxChunk, maxFiles)
	st.Chunks = len(chunks)
	if len(todo) == 0 && len(unsafe) > 0 {
		return fatal(reasonImportFailed, "nothing imported into %s: %s", where, failedSummary(unsafe))
	}
	if len(todo) == 0 {
		o.Status.Phase, o.Status.CompletionTime = dxv1.WCMImportSucceeded, now()
		r.done(o, reasonNothingToDo, true, fmt.Sprintf("nothing to import into %s: %d libraries in the export, all skipped", where, len(exp.Libraries)))
		return nil
	}
	if o.Spec.DryRun {
		msg := fmt.Sprintf("dry run: would import %d of %d libraries into %s in %d chunk(s): %s", len(todo), len(exp.Libraries), where, len(chunks), strings.Join(wcmexport.Titles(todo), ", "))
		if len(unsafe) > 0 {
			msg += fmt.Sprintf("; %d cannot be uploaded (names DX rejects): %s", len(unsafe), failedSummary(unsafe))
		}
		if vpMissing {
			msg = "dry run: would create the virtual portal " + vp + "; " + strings.TrimPrefix(msg, "dry run: ")
		}
		o.Status.Phase, o.Status.CompletionTime = dxv1.WCMImportSucceeded, now()
		r.done(o, reasonDryRun, true, msg)
		return nil
	}

	// 4. Chunk by chunk: zip, upload, import
	work := filepath.Join(r.WorkDir, "wcm-import", o.Namespace+"-"+o.Name)
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(work) //nolint:errcheck
	// one dxFileTransfer folder per chunk, deleted as soon as the chunk is imported: the files land on the disk of
	// the DX container (an unpacked export is ~1.5x its zip)
	subOf := func(i int) string { return fmt.Sprintf("%s-%02d", transferDir(o), i+1) }
	cleanup := func(i int) {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := c.FileTransferDeleteDir(cctx, subOf(i)); err != nil {
			r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, "CleanupFailed", "Import", "the uploaded files stay in %s/%s: %v", dx.FileTransferRoot, subOf(i), err)
		}
	}
	timeout := defaultImportWait
	if o.Spec.Timeout != nil && o.Spec.Timeout.Duration > 0 {
		timeout = o.Spec.Timeout.Duration
	}
	failed := map[string]string{}
	for i, chunk := range chunks {
		st.CurrentChunk = i + 1
		titles := wcmexport.Titles(chunk)
		zipPath := filepath.Join(work, fmt.Sprintf("chunk-%02d.zip", i+1))
		r.progress(ctx, o, dxv1.WCMImportUploading, fmt.Sprintf("chunk %d/%d: packing and uploading %d libraries", i+1, len(chunks), len(chunk)))
		zr, err := exp.WriteZip(chunk, zipPath)
		if err != nil {
			return err
		}
		if zr.Bytes > dx.MaxUploadBytes {
			for _, t := range titles {
				failed[t] = fmt.Sprintf("its zip is %d MB: DX accepts at most 500 MB per upload", zr.Bytes>>20)
			}
			_ = os.Remove(zipPath)
			continue
		}
		_ = c.FileTransferDeleteDir(ctx, subOf(i)) // leftovers of a run that was cut
		if err := c.FileTransferCreateDir(ctx, subOf(i)); err != nil {
			_ = os.Remove(zipPath)
			if i == 0 {
				return fmt.Errorf("dxFileTransfer: %w", err)
			}
			for _, t := range titles {
				failed[t] = "dxFileTransfer: " + err.Error()
			}
			continue
		}
		dir, err := c.FileTransferUpload(ctx, subOf(i), zipPath)
		_ = os.Remove(zipPath)
		if err != nil {
			cleanup(i)
			if i == 0 {
				return fmt.Errorf("upload of chunk 1: %w", err) // nothing imported yet: retry later
			}
			for _, t := range titles {
				failed[t] = "upload failed: " + err.Error()
			}
			continue
		}
		r.progress(ctx, o, dxv1.WCMImportImporting, fmt.Sprintf("chunk %d/%d: importing %s into %s", i+1, len(chunks), strings.Join(titles, ", "), where))
		_, err = c.DataModuleImport(ctx, vp, dir, o.Spec.Mode == dxv1.WCMImportCopy, timeout)
		if errors.Is(err, dx.ErrCutByProxy) {
			// Do not delete the files: DX goes on importing them (deleting them made the import fail on CF239).
			// The next run removes the folder before uploading again.
			err = fmt.Errorf("%v: the files stay in %s/%s until the next run; check the DX log, and use smaller chunks "+
				"(upload.maxChunkFiles) if it happens again", err, dx.FileTransferRoot, subOf(i))
		} else {
			cleanup(i)
		}
		if err != nil {
			for _, t := range titles {
				failed[t] = err.Error()
			}
			r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, reasonImportFailed, chunkAction(i), "chunk %d/%d: %v", i+1, len(chunks), err)
		} else {
			r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, "ChunkImported", chunkAction(i), "chunk %d/%d imported: %s", i+1, len(chunks), strings.Join(titles, ", "))
		}
	}

	// 5. Check that the libraries are there, and grant the roles
	r.progress(ctx, o, dxv1.WCMImportVerifying, "checking the libraries in "+where)
	after, err := c.ListLibraries(ctx, vp)
	if err != nil {
		return fatal(reasonDXError, "the import ran but the libraries of %s cannot be listed: %v", where, err)
	}
	byTitle := map[string]dx.WCMLibrary{}
	for _, l := range after {
		byTitle[strings.ToLower(l.Title)] = l
	}
	var warnings []string
	for _, l := range todo {
		if why, bad := failed[l.Title]; bad {
			// a chunk can fail on one item and still import the rest of its libraries (seen on CF239)
			if _, there := byTitle[strings.ToLower(l.Title)]; there && !existing[strings.ToLower(l.Title)] {
				why = "imported with errors: " + why
			}
			st.FailedLibraries = append(st.FailedLibraries, dxv1.WCMLibraryResult{Title: l.Title, Reason: why})
			continue
		}
		lib, there := byTitle[strings.ToLower(l.Title)]
		if !there {
			st.FailedLibraries = append(st.FailedLibraries, dxv1.WCMLibraryResult{Title: l.Title, Reason: "not listed by DX after the import"})
			continue
		}
		st.ImportedLibraries = append(st.ImportedLibraries, l.Title)
		for _, a := range o.Spec.Access {
			href, aerr := lib.AccessControl, error(nil)
			if href == "" {
				href, aerr = c.LibraryAccessControl(ctx, vp, lib.ID)
			}
			if aerr == nil {
				aerr = c.GrantLibraryRole(ctx, vp, href, a.DN, a.Type, a.Role)
			}
			if aerr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: role %s for %s: %v", l.Title, a.Role, a.DN, aerr))
			}
		}
	}
	sort.Strings(st.ImportedLibraries)
	st.CurrentChunk = 0
	msg := fmt.Sprintf("%d libraries imported into %s in %d chunk(s), %d skipped, %d failed", len(st.ImportedLibraries), where, len(chunks), len(st.SkippedLibraries), len(st.FailedLibraries))
	switch {
	case len(st.FailedLibraries) > 0:
		return fatal(reasonImportFailed, "%s: %s", msg, failedSummary(st.FailedLibraries))
	case len(warnings) > 0:
		o.Status.Phase, o.Status.CompletionTime = dxv1.WCMImportSucceeded, now()
		r.done(o, reasonWarnings, true, msg+"; access not granted: "+strings.Join(warnings, "; "))
	default:
		o.Status.Phase, o.Status.CompletionTime = dxv1.WCMImportSucceeded, now()
		r.done(o, reasonSucceeded, true, msg)
	}
	return nil
}

// singleArchive returns the only archive file of a source tree (the export as the customer delivers it).
func singleArchive(dir string) (string, error) {
	var found []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && source.IsArchive(d.Name()) {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 1 {
		return "", fmt.Errorf("%d archives in the source", len(found))
	}
	return found[0], nil
}

// chunkAction gives each chunk its own event action: Kubernetes merges events with the same object, reason and action
// into one series (only the count changes), so "chunk 3 imported" would show as a repeat of "chunk 2 imported".
func chunkAction(i int) string { return fmt.Sprintf("ImportChunk%d", i+1) }

func failedSummary(f []dxv1.WCMLibraryResult) string {
	var b []string
	for i, x := range f {
		if i == 5 {
			b = append(b, fmt.Sprintf("and %d more (see status.failedLibraries)", len(f)-5))
			break
		}
		b = append(b, x.Title)
	}
	return strings.Join(b, ", ")
}

// done records the final status of a run that ended as planned.
func (r *DXWCMLibraryImportReconciler) done(o *dxv1.DXWCMLibraryImport, reason string, ready bool, msg string) {
	o.Status.Message, o.Status.AppliedHash = msg, importHash(o)
	setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, ready, reason, msg)
	r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, reason, "Import", "%s", msg)
}

// createVirtualPortal creates the virtual portal of the spec and waits until its WCM answers.
func (r *DXWCMLibraryImportReconciler) createVirtualPortal(ctx context.Context, o *dxv1.DXWCMLibraryImport, c *dx.Client, existing []dx.VirtualPortal) error {
	v := o.Spec.VirtualPortal
	realm := v.Realm
	if realm == "" {
		realm = defaultRealm
		for _, e := range existing {
			if e.Realm != "" {
				realm = e.Realm
				break
			}
		}
	}
	title := v.Title
	if title == "" {
		title = v.Context
	}
	r.progress(ctx, o, dxv1.WCMImportPending, "creating the virtual portal "+v.Context)
	if err := c.CreateVirtualPortal(ctx, dx.VirtualPortal{Name: title, Realm: realm, Description: v.Description, Context: v.Context}); err != nil {
		return fatal(reasonDXError, "%v", err)
	}
	r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, "VirtualPortalCreated", "Import", "virtual portal %s (%s) created, realm %s", v.Context, title, realm)
	deadline := time.Now().Add(vpReadyWait)
	for {
		if _, err := c.ListLibraries(ctx, v.Context); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fatal(reasonDXError, "the virtual portal %s was created but its WCM did not answer within %s", v.Context, vpReadyWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}

func wcmImportRefs(list *dxv1.DXWCMLibraryImportList) map[client.ObjectKey][]refersTo {
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

// SetupWithManager sets up the controller. Only spec and annotation changes start a run; a change of the
// DXConnection only wakes the imports that are waiting for it.
func (r *DXWCMLibraryImportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() *dxv1.DXWCMLibraryImportList { return &dxv1.DXWCMLibraryImportList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxv1.DXWCMLibraryImport{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&dxv1.DXConnection{}, handler.EnqueueRequestsFromMapFunc(mapReferrers(r.Client, newList, wcmImportRefs, "DXConnection"))).
		Named("dxwcmlibraryimport").
		Complete(r)
}

// exportLibraries lists every library of the export with its description, size and plan (Import when it has none).
func exportLibraries(libs []wcmexport.Library, plans map[string]dxv1.WCMExportLibrary, existing map[string]bool) []dxv1.WCMExportLibrary {
	out := make([]dxv1.WCMExportLibrary, 0, len(libs))
	for _, l := range libs {
		e, ok := plans[l.Title]
		if !ok {
			e.Plan = dxv1.WCMPlanImport
		}
		e.Title, e.Bytes, e.Files, e.ExistsInTarget = l.Title, l.Bytes, l.Files, existing[strings.ToLower(l.Title)]
		e.Description = l.Description
		if r := []rune(e.Description); len(r) > 300 {
			e.Description = string(r[:299]) + "…"
		}
		out = append(out, e)
	}
	return out
}
