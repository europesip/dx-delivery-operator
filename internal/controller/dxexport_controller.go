package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// DXExportReconciler exports pages, Portal Site and WCM libraries of DX into one portable "DX bundle" (an OCI
// artifact in the native formats of DX), once per spec.
type DXExportReconciler struct {
	client.Client
	// APIReader reads straight from the API server (see DXWCMLibraryImportReconciler).
	APIReader client.Reader
	Scheme    *runtime.Scheme
	*Shared
}

// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxexports,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=dx.europesip.com,resources=dxexports/status,verbs=get;update;patch

const (
	// BundleSchema is the format of the artifacts of DXExport (docs/portable-bundles.md).
	BundleSchema      = "dx-bundle/v1"
	bundleFile        = "bundle.json"
	bundlePagesFile   = "xmlaccess/01-pages.xml"
	reasonPageMissing = "PageNotFound"
)

func dxExportHash(o *dxv1.DXExport) string {
	s := o.Spec.DeepCopy()
	s.Suspend, s.DependsOn = false, nil
	b, _ := json.Marshal(s)
	return hashOf(string(b), forceToken(o))
}

func (r *DXExportReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Reconcile runs the export when the spec changed since the last finished run.
func (r *DXExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var o dxv1.DXExport
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
	hash := dxExportHash(&o)
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

func (r *DXExportReconciler) saveStatus(ctx context.Context, o *dxv1.DXExport) error {
	var cur dxv1.DXExport
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(o), &cur); err != nil {
		return err
	}
	cp := cur.DeepCopy()
	cp.Status = *o.Status.DeepCopy()
	return r.Status().Patch(ctx, cp, client.MergeFrom(&cur))
}

func (r *DXExportReconciler) cancelWhenDeleted(ctx context.Context, cancel context.CancelFunc, key client.ObjectKey, rd client.Reader) {
	t := time.NewTicker(cancelCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var o dxv1.DXExport
			err := rd.Get(ctx, key, &o)
			if apierrors.IsNotFound(err) || (err == nil && !o.DeletionTimestamp.IsZero()) {
				cancel()
				return
			}
		}
	}
}

func (r *DXExportReconciler) progress(ctx context.Context, o *dxv1.DXExport, phase dxv1.WCMExportPhase, msg string) {
	o.Status.Phase, o.Status.Message, o.Status.CurrentStep = phase, msg, msg
	o.Status.ObservedGeneration = o.Generation
	setCondition(&o.Status.Conditions, o.Generation, dxv1.ConditionReady, false, string(phase), msg)
	if err := r.saveStatus(ctx, o); err != nil {
		logf.FromContext(ctx).Info("cannot save the progress", "error", err.Error())
	}
}

func (r *DXExportReconciler) run(ctx context.Context, o *dxv1.DXExport, hash string) (ctrl.Result, error) {
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
	st.Image, st.Digest, st.Pages, st.PagesExported, st.ExportedLibraries, st.Files, st.Warnings, st.TotalBytes, st.CurrentStep =
		"", "", nil, 0, nil, nil, nil, 0, ""

	err = r.export(ctx, o, conn)
	var p permanent
	switch {
	case err == nil:
		st.Phase, st.CompletionTime, st.AppliedHash, st.CurrentStep = dxv1.WCMExportSucceeded, now(), hash, ""
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, true, reasonSucceeded, st.Message)
		r.Recorder.Eventf(o, nil, corev1.EventTypeNormal, reasonSucceeded, "Export", "%s", st.Message)
		return ctrl.Result{}, nil
	case errors.As(err, &p):
		st.Phase, st.Message, st.AppliedHash, st.CompletionTime, st.CurrentStep = dxv1.WCMExportFailed, p.err.Error(), hash, now(), ""
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, p.reason, st.Message)
		r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, p.reason, "Export", "%s", st.Message)
		return ctrl.Result{}, nil
	case errors.Is(err, context.Canceled):
		return ctrl.Result{}, err
	default:
		setCondition(&st.Conditions, gen, dxv1.ConditionReady, false, reasonDXError, err.Error())
		st.Phase, st.Message, st.CurrentStep = dxv1.WCMExportPending, err.Error(), ""
		r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, reasonDXError, "Export", "%v", err)
		return ctrl.Result{RequeueAfter: retryFailed}, nil
	}
}

// validateDXExport checks the spec: the pages (unique, not empty), the library titles and the attributes.
func validateDXExport(s *dxv1.DXExportSpec) (pages []dx.PageSelection, titles []string, err error) {
	p := s.Parts
	if len(p.Pages) == 0 && len(p.Libraries) == 0 && !p.PortalSite && len(p.Themes) == 0 {
		return nil, nil, errors.New("parts: at least one of themes, pages, portalSite and libraries")
	}
	for _, t := range p.Themes {
		if strings.TrimSpace(t) == "" {
			return nil, nil, errors.New("parts.themes: an empty unique name")
		}
	}
	seen := map[string]bool{}
	for _, pg := range p.Pages {
		u := strings.TrimSpace(pg.UniqueName)
		if u == "" {
			return nil, nil, errors.New("parts.pages: a page without uniqueName")
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		pages = append(pages, dx.PageSelection{UniqueName: u, Descendants: pg.Descendants == nil || *pg.Descendants})
	}
	libs := append([]string{}, p.Libraries...)
	if p.PortalSite {
		libs = append(libs, wcmexport.PortalSiteTitle)
	}
	if len(libs) > 0 {
		if titles, err = uniqueTitles(libs); err != nil {
			return nil, nil, fmt.Errorf("parts.libraries: %w", err)
		}
	}
	for k, v := range s.Destination.Attributes {
		if !source.AttributeKeyRe.MatchString(k) || len(v) > 256 {
			return nil, nil, fmt.Errorf("destination.attributes: key %q must be lowercase letters, digits, dots and dashes (at most 63), its value at most 256 characters", k)
		}
	}
	return pages, titles, nil
}

// export does one run: nil (st.Message set), a permanent error, or another error to retry later.
func (r *DXExportReconciler) export(ctx context.Context, o *dxv1.DXExport, conn *Connection) error {
	st := &o.Status
	c := conn.Client
	vp := o.Spec.VirtualPortal
	where := "the base portal"
	if vp != "" {
		where = "the virtual portal " + vp
	}
	pages, titles, err := validateDXExport(&o.Spec)
	if err != nil {
		return fatal(reasonInvalidSpec, "%v", err)
	}
	timeout := defaultExportWait
	if o.Spec.Timeout != nil && o.Spec.Timeout.Duration > 0 {
		timeout = o.Spec.Timeout.Duration
	}
	work, err := exportWorkDir(r.WorkDir, "dx-export", o.Namespace, o.Name)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work) //nolint:errcheck
	var files []source.ArtifactFile
	b := bundleOf(conn, o)

	// 1. The libraries must exist before anything is exported
	if len(titles) > 0 {
		r.progress(ctx, o, dxv1.WCMExportPending, "checking the libraries of "+where)
		if titles, err = resolveLibraries(ctx, c, vp, where, titles); err != nil {
			return err
		}
	}

	// 1b. Themes: registration with skins (XMLAccess) and the WebDAV folder (static files), imported first
	st.Themes = nil // a run that failed half-way must not leave its themes
	for i, un := range o.Spec.Parts.Themes {
		un = strings.TrimSpace(un)
		r.progress(ctx, o, dxv1.WCMExportExporting, fmt.Sprintf("theme %d/%d: exporting %s from %s", i+1, len(o.Spec.Parts.Themes), un, where))
		te, ok, err := c.ExportTheme(ctx, vp, un)
		if err != nil {
			return fmt.Errorf("theme %s: %w", un, err)
		}
		if !ok {
			return fatal(reasonExportFailed, "the theme %s is not in %s; nothing was exported", un, where)
		}
		slug := unsafeChars.ReplaceAllString(strings.ToLower(strings.TrimPrefix(un, "custom.portal.theme.")), "-")
		xmlTitle := "themes/theme-" + slug + ".xml"
		xp := filepath.Join(work, filepath.FromSlash(xmlTitle))
		if err := os.MkdirAll(filepath.Dir(xp), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(xp, []byte(te.XML), 0o600); err != nil {
			return err
		}
		files = append(files, source.ArtifactFile{Path: xp, Title: xmlTitle, MediaType: "application/xml"})
		et := dxv1.DXExportedTheme{UniqueName: un, Folder: te.Folder, Skins: te.Skins}
		if te.Folder != "" {
			dir := filepath.Join(work, "theme-files", slug)
			n, err := c.DownloadTheme(ctx, te.Folder, dir)
			if err != nil {
				return fmt.Errorf("theme %s: WebDAV download of %s: %w", un, te.Folder, err)
			}
			zipTitle := "themes/theme-" + slug + "-webdav.zip"
			zp := filepath.Join(work, filepath.FromSlash(zipTitle))
			if _, err := dx.ZipDir(dir, zp); err != nil {
				return err
			}
			_ = os.RemoveAll(dir)
			et.Files = n
			files = append(files, source.ArtifactFile{Path: zp, Title: zipTitle, MediaType: exportZipMediaType})
		}
		st.Themes = append(st.Themes, et)
	}

	// 2. Pages: one XMLAccess export, turned into a request that imports them into another portal
	var inv dx.PagesInventory
	if len(pages) > 0 {
		names := make([]string, len(pages))
		for i, p := range pages {
			names[i] = p.UniqueName
		}
		r.progress(ctx, o, dxv1.WCMExportExporting, fmt.Sprintf("exporting %d page(s) of %s with XMLAccess: %s", len(pages), where, strings.Join(names, ", ")))
		ans, err := c.XMLAccessAt(ctx, vp, dx.PagesExportXML(pages))
		if err != nil {
			return fmt.Errorf("XMLAccess export of the pages: %w", err)
		}
		req, iv, err := dx.PagesImportXML(ans, names)
		if errors.Is(err, dx.ErrPageNotFound) {
			return fatal(reasonPageMissing, "in %s: %v; nothing was exported", where, err)
		}
		if err != nil {
			return fatal(reasonExportFailed, "%v", err)
		}
		inv = iv
		// cloned portlets do not exist in another portal: their definitions go into the request
		req, clones, err := c.InlineClonedPortlets(ctx, vp, req)
		if err != nil {
			return fatal(reasonExportFailed, "%v", err)
		}
		var cloneWarn []string
		if len(clones) > 0 {
			cloneWarn = append(cloneWarn, fmt.Sprintf("%d cloned portlet(s) are defined in %s (created in the target with the same object ID): %s", len(clones), bundlePagesFile, strings.Join(clones, ", ")))
		}
		for _, n := range names {
			if iv.PerPage[n] == 0 {
				return fatal(reasonPageMissing, "%s is not in the XMLAccess answer of %s; nothing was exported", n, where)
			}
			st.Pages = append(st.Pages, dxv1.DXExportedPage{UniqueName: n, Nodes: iv.PerPage[n]})
		}
		st.PagesExported = iv.Nodes
		st.Warnings = append(pageWarnings(iv, o.Spec.Parts.PortalSite), cloneWarn...)
		p := filepath.Join(work, filepath.FromSlash(bundlePagesFile))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(req), 0o600); err != nil {
			return err
		}
		files = append(files, source.ArtifactFile{Path: p, Title: bundlePagesFile, MediaType: "application/xml"})
	}

	// 3. WCM: the libraries and Portal Site into one zip (the JCR format of ConfigEngine export-wcm-data)
	if len(titles) > 0 {
		zipTitle := "wcm/wcm-export-" + unsafeChars.ReplaceAllString(strings.ToLower(o.Name), "-") + ".zip"
		zipPath := filepath.Join(work, filepath.FromSlash(zipTitle))
		if err := os.MkdirAll(filepath.Dir(zipPath), 0o700); err != nil {
			return err
		}
		sub := "dxop-bundle-" + unsafeChars.ReplaceAllString(strings.ToLower(o.Namespace+"-"+o.Name), "-")
		_, exported, err := wcmExportJob{
			Client: c, VP: vp, Where: where, Titles: titles, Sub: sub, ZipPath: zipPath, Timeout: timeout,
			OnLibrary: func(i, total int, t string) {
				r.progress(ctx, o, dxv1.WCMExportExporting, fmt.Sprintf("library %d/%d: exporting %s from %s", i, total, t, where))
			},
			OnDownload: func() {
				r.progress(ctx, o, dxv1.WCMExportDownloading, fmt.Sprintf("downloading the export of %d libraries", len(titles)))
			},
			OnCleanupError: func(err error) {
				r.Recorder.Eventf(o, nil, corev1.EventTypeWarning, "CleanupFailed", "Export", "the exported files stay in %s/%s: %v", dx.FileTransferRoot, sub, err)
			},
		}.run(ctx)
		if err != nil {
			return err
		}
		st.ExportedLibraries = exported
		files = append(files, source.ArtifactFile{Path: zipPath, Title: zipTitle, MediaType: exportZipMediaType})
	}

	// 4. bundle.json and EXPORT-INFO.txt, then push
	when := time.Now().UTC()
	b.Created = when.Format(time.RFC3339)
	b.Source.Version = c.ServerVersion(ctx)
	for _, f := range files {
		bf, err := fileEntry(f)
		if err != nil {
			return err
		}
		b.Files = append(b.Files, bf)
		st.TotalBytes += bf.Bytes
	}
	b.Pages, b.Libraries, b.Warnings = st.Pages, st.ExportedLibraries, st.Warnings
	b.References = bundleRefs{Themes: inv.Themes, Skins: inv.Skins, Portlets: inv.Portlets}
	b.Import = append(themeSteps(st.Themes), importSteps(o, len(pages) > 0, titles)...)
	for i := range b.Import {
		b.Import[i].Step = i + 1
	}
	bj, _ := json.MarshalIndent(b, "", "  ")
	bjPath := filepath.Join(work, bundleFile)
	if err := os.WriteFile(bjPath, append(bj, '\n'), 0o600); err != nil {
		return err
	}
	infoPath := filepath.Join(work, exportInfoFile)
	if err := os.WriteFile(infoPath, []byte(bundleInfo(o, b)), 0o600); err != nil {
		return err
	}
	all := append([]source.ArtifactFile{{Path: infoPath, Title: exportInfoFile, MediaType: exportInfoMediaType}, {Path: bjPath, Title: bundleFile, MediaType: "application/json"}}, files...)
	st.Files = nil
	for _, f := range all {
		bf, err := fileEntry(f)
		if err != nil {
			return err
		}
		st.Files = append(st.Files, dxv1.DXBundleFile{Path: bf.Path, Bytes: bf.Bytes, SHA256: bf.SHA256})
	}
	r.progress(ctx, o, dxv1.WCMExportPushing, fmt.Sprintf("pushing the bundle (%d files, %d MB) to %s", len(all), st.TotalBytes>>20, o.Spec.Destination.Image))
	digest, err := r.Fetcher.PushFiles(ctx, o.Namespace, o.Spec.Destination.Image, o.Spec.Destination.PushSecrets, all, bundleMeta(o, b, when).Annotations())
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
	var what []string
	if len(st.Themes) > 0 {
		what = append(what, fmt.Sprintf("%d theme(s)", len(st.Themes)))
	}
	if len(pages) > 0 {
		what = append(what, fmt.Sprintf("%d page(s) (%d content nodes)", len(pages), st.PagesExported))
	}
	if len(titles) > 0 {
		what = append(what, fmt.Sprintf("%d WCM librar%s", len(titles), map[bool]string{true: "y", false: "ies"}[len(titles) == 1]))
	}
	st.Message = fmt.Sprintf("bundle of %s exported from %s to %s", strings.Join(what, " and "), where, o.Spec.Destination.Image)
	if len(st.Warnings) > 0 {
		st.Message += fmt.Sprintf(" (%d warning(s): see status.warnings and EXPORT-INFO.txt)", len(st.Warnings))
	}
	return nil
}

// pageWarnings: what the target must have, or what will not work there.
func pageWarnings(iv dx.PagesInventory, portalSite bool) []string {
	var w []string
	if len(iv.Themes) > 0 || len(iv.Skins) > 0 {
		w = append(w, fmt.Sprintf("the target must have the theme(s) %s and skin(s) %s (located by unique name)", strings.Join(iv.Themes, ", "), strings.Join(iv.Skins, ", ")))
	}
	if len(iv.Portlets) > 0 {
		w = append(w, fmt.Sprintf("the pages use %d portlet(s) (%s): portlets of your own (JSR 286) do not run on DX Compose without the Java Transition Module", len(iv.Portlets), strings.Join(firstN(iv.Portlets, 10), ", ")))
	}
	if len(iv.UnnamedParents) > 0 {
		w = append(w, fmt.Sprintf("%d parent page(s) have no unique name (%s): they do not exist in another portal; give them a unique name or export from a parent that has one", len(iv.UnnamedParents), strings.Join(firstN(iv.UnnamedParents, 5), ", ")))
	}
	if iv.ManagedPages > 0 && !portalSite {
		w = append(w, fmt.Sprintf("%d managed page(s) keep their content in the Portal Site library, which is not in this bundle (parts.portalSite)", iv.ManagedPages))
	}
	return w
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), fmt.Sprintf("and %d more", len(s)-n))
}

// ---------------------------------------------------------------------------------------------- bundle.json

type bundleSource struct {
	Server        string `json:"server"`
	URL           string `json:"url,omitempty"`
	Version       string `json:"version,omitempty"`
	Connection    string `json:"connection"`
	VirtualPortal string `json:"virtualPortal"`
}

type bundleFileEntry struct {
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

type bundleRefs struct {
	Themes   []string `json:"themes,omitempty"`
	Skins    []string `json:"skins,omitempty"`
	Portlets []string `json:"portlets,omitempty"`
}

// bundleStep: one step of the import, in DX Compose and in a traditional DX, for both choices of object IDs.
type bundleStep struct {
	Step              int    `json:"step"`
	Part              string `json:"part"`
	File              string `json:"file"`
	Compose           string `json:"compose"`
	ComposeNewIDs     string `json:"composeNewIds"`
	Traditional       string `json:"traditional"`
	TraditionalNewIDs string `json:"traditionalNewIds"`
}

// bundleObjectIDs: the object IDs are chosen when importing (as WCM import / import-copy); the files keep the IDs of
// the source.
type bundleObjectIDs struct {
	InFiles     string `json:"inFiles"`
	Recommended string `json:"recommended"`
	Keep        string `json:"keep"`
	New         string `json:"new"`
}

func objectIDsOf() bundleObjectIDs {
	return bundleObjectIDs{
		InFiles:     "kept: the object IDs and WCM UUIDs of the source, as DX exported them",
		Recommended: "keep",
		Keep:        "keep the IDs of the source (staging to production, portals that are syndicated later): pages sent as they are (DXXMLAccess objectIDs: Keep), WCM with mode Import (taskType=import)",
		New:         "new IDs (a copy into a portal where the IDs would collide): pages with create-oids=\"true\" (DXXMLAccess objectIDs: New), WCM with mode ImportCopy (taskType=import-copy); use the same choice for pages and Portal Site",
	}
}

type bundleManifest struct {
	Schema      string                    `json:"schema"`
	Kind        string                    `json:"kind"`
	Resource    string                    `json:"resource"`
	Created     string                    `json:"created"`
	Source      bundleSource              `json:"source"`
	RequestedBy string                    `json:"requestedBy,omitempty"`
	Reason      string                    `json:"reason,omitempty"`
	Tools       map[string]string         `json:"tools"`
	Attributes  map[string]string         `json:"attributes,omitempty"`
	Parts       []string                  `json:"parts"`
	Pages       []dxv1.DXExportedPage     `json:"pages,omitempty"`
	Libraries   []dxv1.WCMExportedLibrary `json:"libraries,omitempty"`
	References  bundleRefs                `json:"references"`
	Warnings    []string                  `json:"warnings,omitempty"`
	ObjectIDs   bundleObjectIDs           `json:"objectIds"`
	Import      []bundleStep              `json:"import"`
	Files       []bundleFileEntry         `json:"files"`
}

func bundleOf(conn *Connection, o *dxv1.DXExport) bundleManifest {
	ann := o.Annotations
	tools := map[string]string{"operator": "hcl-dx-scriptapp-operator " + source.OperatorVersion()}
	if cw := strings.TrimSpace(ann[source.ResCreatedWith]); cw != "" {
		tools["createdWith"] = cw
	}
	var parts []string
	if len(o.Spec.Parts.Pages) > 0 {
		parts = append(parts, "pages")
	}
	if o.Spec.Parts.PortalSite {
		parts = append(parts, "portal-site")
	}
	if len(o.Spec.Parts.Libraries) > 0 {
		parts = append(parts, "libraries")
	}
	return bundleManifest{
		Schema: BundleSchema, Kind: source.KindSite, Resource: o.Namespace + "/" + o.Name,
		Source:      bundleSource{Server: sourceNameOf(conn), URL: conn.Config.BaseURL, Connection: o.Namespace + "/" + o.Spec.ConnectionRef.Name, VirtualPortal: o.Spec.VirtualPortal},
		RequestedBy: firstNonEmpty(o.Spec.Destination.Authors, strings.TrimPrefix(strings.TrimPrefix(ann[source.ResChangedBy], "user:"), "default/")),
		Reason:      firstNonEmpty(o.Spec.Destination.Reason, strings.TrimSpace(ann[source.ResReason]), o.Spec.Destination.Attributes["reason"]), Tools: tools, Attributes: o.Spec.Destination.Attributes, Parts: parts,
		ObjectIDs: objectIDsOf(),
	}
}

func fileEntry(f source.ArtifactFile) (bundleFileEntry, error) {
	fh, err := os.Open(f.Path)
	if err != nil {
		return bundleFileEntry{}, err
	}
	defer fh.Close() //nolint:errcheck
	h := sha256.New()
	n, err := io.Copy(h, fh)
	if err != nil {
		return bundleFileEntry{}, err
	}
	return bundleFileEntry{Path: f.Title, MediaType: f.MediaType, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// importSteps: the order of the import, with the command of each step in DX Compose and in a traditional DX.
func importSteps(o *dxv1.DXExport, pages bool, titles []string) []bundleStep {
	vp := o.Spec.VirtualPortal
	cfg := "https://<host>:<port>/wps/config"
	vpProp := ""
	if vp != "" {
		cfg += "/" + vp
		vpProp = " -DVirtualPortalContext=" + vp
	}
	var steps []bundleStep
	vpPath := ""
	if vp != "" {
		vpPath = "/" + vp
	}
	if pages {
		xa := "<wp_profile>/PortalServer/bin/xmlaccess.sh -in " + bundlePagesFile + " -user <admin> -password <pwd> -url " + cfg + " -out result.xml"
		steps = append(steps, bundleStep{
			Step: len(steps) + 1, Part: "pages", File: bundlePagesFile,
			Compose:           "DXXMLAccess with source.image.ref=<this artifact>, files=[" + bundlePagesFile + "] and objectIDs: Keep (Backstage: Run XMLAccess), dry run first",
			ComposeNewIDs:     "the same with objectIDs: New",
			Traditional:       xa,
			TraditionalNewIDs: "sed -i '0,/<request /s//<request create-oids=\"true\" /' " + bundlePagesFile + " && " + xa,
		})
	}
	if len(titles) > 0 {
		steps = append(steps, bundleStep{
			Step: len(steps) + 1, Part: "wcm", File: "wcm/wcm-export-" + unsafeChars.ReplaceAllString(strings.ToLower(o.Name), "-") + ".zip",
			Compose:           "DXWCMLibraryImport with source.image.ref=<this artifact> and mode: Import (Backstage: Import WCM libraries)" + portalSiteNote(o.Spec.Parts.PortalSite) + ", dry run first",
			ComposeNewIDs:     "the same with mode: ImportCopy",
			Traditional:       "unzip it into <folder> on the DX server, then <wp_profile>/ConfigEngine/ConfigEngine.sh import-wcm-data -Dimport.directory=<folder>" + vpProp + " -DWasPassword=<pwd> -DPortalAdminPwd=<pwd>",
			TraditionalNewIDs: "unzip it into <folder> on the DX server and, signed in to the portal as an administrator, open /wps/wcm/myconnect" + vpPath + "?MOD=data&processLibraries=false&taskType=import-copy&input.dir=<folder> (import-wcm-data has no copy mode)",
		})
	}
	return steps
}

// themeSteps: the themes go in first (the pages refer to them).
func themeSteps(themes []dxv1.DXExportedTheme) []bundleStep {
	var steps []bundleStep
	for _, t := range themes {
		slug := unsafeChars.ReplaceAllString(strings.ToLower(strings.TrimPrefix(t.UniqueName, "custom.portal.theme.")), "-")
		files := ""
		if t.Folder != "" {
			files = "upload themes/theme-" + slug + "-webdav.zip, unzipped, to the WebDAV folder fs-type1/themes/" + t.Folder + "/, then "
		}
		steps = append(steps, bundleStep{
			Part: "theme", File: "themes/theme-" + slug + ".xml",
			Compose:           files + "DXXMLAccess with source.image.ref=<this artifact>, files=[themes/theme-" + slug + ".xml] and objectIDs: Keep; or DXTheme with source.image.ref=<this artifact>, source.image.path=themes/theme-" + slug + "-webdav.zip, copyFiles: false (new registration from a base theme)",
			ComposeNewIDs:     "the same with objectIDs: New",
			Traditional:       files + "<wp_profile>/PortalServer/bin/xmlaccess.sh -in themes/theme-" + slug + ".xml -user <admin> -password <pwd> -url https://<host>:<port>/wps/config -out result.xml (or dxclient deploy-theme with the WebDAV zip)",
			TraditionalNewIDs: "the same, with create-oids=\"true\" in <request>",
		})
	}
	return steps
}

func portalSiteNote(on bool) string {
	if on {
		return " with portalSiteLibraryOverwrite: true (Portal Site replaces the content of the managed pages)"
	}
	return ""
}

func bundleMeta(o *dxv1.DXExport, b bundleManifest, when time.Time) source.ArtifactMeta {
	var titles, pages []string
	for _, l := range b.Libraries {
		titles = append(titles, l.Title)
	}
	for _, p := range b.Pages {
		pages = append(pages, p.UniqueName)
	}
	return source.ArtifactMeta{
		Kind: source.KindSite, Title: "DX bundle " + o.Name,
		Description: fmt.Sprintf("DX bundle (%s) from %s", strings.Join(b.Parts, ", "), b.Source.Server),
		Resource:    o.Namespace + "/" + o.Name, SourceServer: b.Source.Server, SourceURL: b.Source.URL, SourceVersion: b.Source.Version,
		Authors: o.Spec.Destination.Authors, Reason: o.Spec.Destination.Reason,
		SourceConnection: b.Source.Connection, SourcePortal: o.Spec.VirtualPortal, Libraries: titles,
		ResourceAnnotations: o.Annotations, Attributes: o.Spec.Destination.Attributes, Created: when,
		Extra: map[string]string{source.AnnBundleSchema: BundleSchema, source.AnnBundleParts: strings.Join(b.Parts, ","), source.AnnPages: strings.Join(pages, " | ")},
	}
}

// bundleInfo is EXPORT-INFO.txt of a bundle: what it holds and how to import it, step by step, in DX Compose and in a
// traditional DX on WebSphere.
func bundleInfo(o *dxv1.DXExport, b bundleManifest) string {
	var w strings.Builder
	vp := b.Source.VirtualPortal
	if vp == "" {
		vp = "(base portal)"
	}
	val := func(s, empty string) string {
		if strings.TrimSpace(s) == "" {
			return empty
		}
		return s
	}
	fmt.Fprintf(&w, "DX bundle (%s)\n\n", BundleSchema)
	fmt.Fprintf(&w, "Source server:   %s (%s)\n", b.Source.Server, b.Source.URL)
	fmt.Fprintf(&w, "Source DX:       %s\n", val(b.Source.Version, "(not read)"))
	fmt.Fprintf(&w, "Connection:      %s\n", b.Source.Connection)
	fmt.Fprintf(&w, "Virtual portal:  %s\n", vp)
	fmt.Fprintf(&w, "Exported at:     %s (UTC)\n", strings.Replace(strings.TrimSuffix(b.Created, "Z"), "T", " ", 1))
	fmt.Fprintf(&w, "Exported by:     DXExport %s\n", b.Resource)
	fmt.Fprintf(&w, "Requested by:    %s\n", val(b.RequestedBy, "(not recorded)"))
	fmt.Fprintf(&w, "Reason:          %s\n", val(b.Reason, "(none given)"))
	fmt.Fprintf(&w, "Operator:        %s\n", b.Tools["operator"])
	fmt.Fprintf(&w, "Created with:    %s\n", val(b.Tools["createdWith"], "the resource itself (kubectl, Git), no Backstage"))
	if len(b.Attributes) > 0 {
		keys := make([]string, 0, len(b.Attributes))
		for k := range b.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + b.Attributes[k]
		}
		fmt.Fprintf(&w, "Attributes:      %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&w, "\nContents\n--------\n")
	if len(b.Pages) > 0 {
		fmt.Fprintf(&w, "Pages (%s):\n", bundlePagesFile)
		for _, p := range b.Pages {
			fmt.Fprintf(&w, "- %s (%d content nodes)\n", p.UniqueName, p.Nodes)
		}
	}
	if len(b.Libraries) > 0 {
		fmt.Fprintf(&w, "WCM libraries (wcm/):\n")
		for _, l := range b.Libraries {
			fmt.Fprintf(&w, "- %s (%d files, %d bytes)\n", l.Title, l.Files, l.Bytes)
		}
	}
	if r := b.References; len(r.Themes)+len(r.Skins)+len(r.Portlets) > 0 {
		fmt.Fprintf(&w, "The pages need in the target: themes %s; skins %s", val(strings.Join(r.Themes, ", "), "none"), val(strings.Join(r.Skins, ", "), "none"))
		if len(r.Portlets) > 0 {
			fmt.Fprintf(&w, "; %d portlet(s): %s", len(r.Portlets), strings.Join(firstN(r.Portlets, 15), ", "))
		}
		w.WriteString("\n")
	}
	for _, x := range b.Warnings {
		fmt.Fprintf(&w, "WARNING: %s\n", x)
	}
	fmt.Fprintf(&w, "Files: see bundle.json (path, size and sha256 of each).\n")

	fmt.Fprintf(&w, `
How to import
-------------
Every file is in the native format of HCL DX: XMLAccess requests and the JCR export of the WCM data module (the
format of ConfigEngine export-wcm-data). Do not rename the folders or files of the zip. Import into a DX of the same
CF level as the source (%s) or a newer one, in this order. The target must already have the themes, skins and
portlets listed above (themes travel in a later version of the bundle).

`, val(b.Source.Version, "see above"))
	fmt.Fprintf(&w, `Object IDs: the files keep the object IDs and WCM UUIDs of the source. Choose when importing, and use the same
choice for the pages and for Portal Site:
  - KEEP (recommended): the IDs of the source. For staging -> production and for portals that are syndicated later
    (syndication matches items by their ID). Importing again updates what exists.
  - NEW: DX gives new IDs. For a copy into a portal where the IDs would collide. The association of managed pages
    with their Portal Site content is by ID: test a copy with Portal Site in a test portal first.

`)
	fmt.Fprintf(&w, "A. HCL DX Compose (DX Script App Operator, or Backstage):\n")
	for _, s := range b.Import {
		fmt.Fprintf(&w, "   %d. %s (%s): %s\n      New IDs: %s\n", s.Step, s.Part, s.File, s.Compose, s.ComposeNewIDs)
	}
	fmt.Fprintf(&w, "   The source of both resources is this artifact:\n     source:\n       image:\n         ref: %s\n", o.Spec.Destination.Image)
	fmt.Fprintf(&w, "\nB. Traditional HCL DX on WebSphere (8.5 / 9.0 / 9.5), with its own tools:\n")
	fmt.Fprintf(&w, "   Download the bundle (oras pull %s) and copy it to the DX server.\n", o.Spec.Destination.Image)
	for _, s := range b.Import {
		fmt.Fprintf(&w, "   %d. %s:\n        Keep IDs: %s\n        New IDs:  %s\n", s.Step, s.Part, s.Traditional, s.TraditionalNewIDs)
	}
	fmt.Fprintf(&w, `   Notes: xmlaccess.sh is in <wp_profile>/PortalServer/bin (xmlaccess.bat on Windows); the request keeps the
   object IDs of the source (create-oids="true" on <request> makes DX give new ones). The owners of access control
   were removed (users of the source registry need not exist): the importing administrator owns the pages; the
   roles are kept. import-wcm-data imports every library of the folder (it has
   no library name property); Portal Site, when it is in the zip, goes after the pages so the managed pages find
   their content. After it, HCL asks to reset the web content event log and restart the server. Or, signed in to the
   portal, the WCM data module:
     /wps/wcm/myconnect[/<virtual portal>]?MOD=data&processLibraries=false&taskType=import&input.dir=<folder>
`)
	return w.String()
}

func dxExportRefs(list *dxv1.DXExportList) map[client.ObjectKey][]refersTo {
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
func (r *DXExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	newList := func() *dxv1.DXExportList { return &dxv1.DXExportList{} }
	return ctrl.NewControllerManagedBy(mgr).
		For(&dxv1.DXExport{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&dxv1.DXConnection{}, handler.EnqueueRequestsFromMapFunc(mapReferrers(r.Client, newList, dxExportRefs, "DXConnection"))).
		Named("dxexport").
		Complete(r)
}
