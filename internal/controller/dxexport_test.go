package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/dx"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/source"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/wcmexport"
)

func TestValidateDXExport(t *testing.T) {
	no := false
	s := dxv1.DXExportSpec{Parts: dxv1.DXExportParts{
		Pages:      []dxv1.DXExportPage{{UniqueName: "orbita-site"}, {UniqueName: "orbita-site"}, {UniqueName: "x", Descendants: &no}},
		PortalSite: true, Libraries: []string{"orbita", "portal site"},
	}}
	pages, titles, err := validateDXExport(&s)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || !pages[0].Descendants || pages[1].Descendants {
		t.Errorf("pages %+v", pages)
	}
	if len(titles) != 2 || titles[0] != "orbita" { // Portal Site once, whatever its case
		t.Errorf("titles %v", titles)
	}
	for _, bad := range []dxv1.DXExportSpec{
		{},
		{Parts: dxv1.DXExportParts{Pages: []dxv1.DXExportPage{{UniqueName: " "}}}},
		{Parts: dxv1.DXExportParts{PortalSite: true}, Destination: dxv1.WCMExportDestination{Attributes: map[string]string{"Bad Key": "x"}}},
	} {
		if _, _, err := validateDXExport(&bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestImportStepsAndInfo(t *testing.T) {
	o := &dxv1.DXExport{}
	o.Namespace, o.Name = "dx-portals", "export-orbita"
	o.Annotations = map[string]string{source.ResChangedBy: "user:default/andres", source.ResReason: "copy to prod", source.ResCreatedWith: "Backstage 1.56.0; @europesip/backstage-plugin-hcl-dx-backend 0.6.0"}
	o.Spec.ConnectionRef.Name = "demorbita"
	o.Spec.VirtualPortal = "europesip"
	o.Spec.Parts = dxv1.DXExportParts{Pages: []dxv1.DXExportPage{{UniqueName: "orbita-site"}}, PortalSite: true}
	o.Spec.Destination.Image = "harbor.example.com/dx-artifacts/sites/orbita:1"
	o.Spec.Destination.Attributes = map[string]string{"customer": "europesip"}
	conn := &Connection{Config: dx.Config{BaseURL: "https://dx.example.com"}}
	b := bundleOf(conn, o)
	b.Created, b.Source.Version = "2026-11-19T09:00:00Z", "HCL Digital Experience Compose/9.5 build 20261103-170843"
	b.Pages = []dxv1.DXExportedPage{{UniqueName: "orbita-site", Nodes: 3}}
	b.Libraries = []dxv1.WCMExportedLibrary{{Title: "Portal Site", Files: 10, Bytes: 100}}
	b.References = bundleRefs{Themes: []string{"custom.portal.theme.Orbita"}, Portlets: []string{"Web Content Viewer (JSR 286)"}}
	b.Import = importSteps(o, true, []string{"Portal Site"})
	if len(b.Import) != 2 || b.Import[0].Part != "pages" || b.Import[1].Part != "wcm" {
		t.Fatalf("steps %+v", b.Import)
	}
	if b.RequestedBy != "andres" || b.Reason != "copy to prod" || !strings.Contains(b.Tools["createdWith"], "backstage-plugin-hcl-dx-backend 0.6.0") || strings.Join(b.Parts, ",") != "pages,portal-site" {
		t.Errorf("bundle %+v", b)
	}
	text := bundleInfo(o, b)
	for _, want := range []string{
		"DX bundle (dx-bundle/v1)", "Requested by:    andres", "Reason:          copy to prod", "Attributes:      customer=europesip",
		"- orbita-site (3 content nodes)", "- Portal Site (10 files", "themes custom.portal.theme.Orbita",
		"<wp_profile>/PortalServer/bin/xmlaccess.sh -in xmlaccess/01-pages.xml -user <admin> -password <pwd> -url https://<host>:<port>/wps/config/europesip -out result.xml",
		"ConfigEngine.sh import-wcm-data -Dimport.directory=<folder> -DVirtualPortalContext=europesip",
		"portalSiteLibraryOverwrite: true", "ref: harbor.example.com/dx-artifacts/sites/orbita:1", "oras pull harbor.example.com/dx-artifacts/sites/orbita:1",
		// 0.9.1: object IDs chosen when importing
		"KEEP (recommended)", "objectIDs: Keep", "New IDs: the same with objectIDs: New", "mode: Import", "New IDs: the same with mode: ImportCopy",
		`sed -i '0,/<request /s//<request create-oids="true" /' xmlaccess/01-pages.xml && <wp_profile>/PortalServer/bin/xmlaccess.sh`,
		"/wps/wcm/myconnect/europesip?MOD=data&processLibraries=false&taskType=import-copy&input.dir=<folder>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("EXPORT-INFO.txt lacks %q:\n%s", want, text)
		}
	}
	if b.ObjectIDs.Recommended != "keep" || b.Import[0].TraditionalNewIDs == "" || b.Import[1].ComposeNewIDs == "" {
		t.Errorf("objectIds %+v / steps %+v", b.ObjectIDs, b.Import)
	}
	ann := bundleMeta(o, b, time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)).Annotations()
	for k, v := range map[string]string{source.AnnKind: source.KindSite, source.AnnSourceVP: "europesip", source.AnnBundleSchema: BundleSchema, source.AnnBundleParts: "pages,portal-site", source.AnnPages: "orbita-site", source.AnnSourcePortal: "europesip", source.AnnAttrPrefix + "customer": "europesip"} {
		if ann[k] != v {
			t.Errorf("%s = %q, want %q", k, ann[k], v)
		}
	}
}

// A bundle (pages request + WCM zip with folders in the layer titles) is read by DXXMLAccess (the request file) and
// DXWCMLibraryImport (the only archive) as they read their image sources.
func TestBundleIsReadByTheImports(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	ref := strings.TrimPrefix(srv.URL, "http://") + "/dx-artifacts/sites/orbita:1"
	work := t.TempDir()
	pages := filepath.Join(work, "01-pages.xml")
	req, _, err := dx.PagesImportXML(`<request type="update"><portal action="locate"><content-node action="update" objectid="Z6_A" content-parentref="Z6_R" uniquename="orbita-site"/></portal><status element="all" result="ok"/></request>`, []string{"orbita-site"})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(pages, []byte(req), 0o600)
	zipPath := filepath.Join(work, "x.zip")
	exportZip(t, zipPath)
	info := filepath.Join(work, "info.txt")
	_ = os.WriteFile(info, []byte("DX bundle"), 0o600)
	bj := filepath.Join(work, "b.json")
	_ = os.WriteFile(bj, []byte(`{"schema":"dx-bundle/v1"}`), 0o600)
	f := &source.Fetcher{Client: fake.NewClientBuilder().Build(), CacheDir: t.TempDir()}
	files := []source.ArtifactFile{
		{Path: info, Title: exportInfoFile, MediaType: exportInfoMediaType}, {Path: bj, Title: bundleFile, MediaType: "application/json"},
		{Path: pages, Title: bundlePagesFile, MediaType: "application/xml"}, {Path: zipPath, Title: "wcm/wcm-export-orbita.zip", MediaType: exportZipMediaType},
	}
	if _, err := f.PushFiles(context.Background(), "ns", ref, nil, files, map[string]string{source.AnnKind: "dx-bundle"}); err != nil {
		t.Fatal(err)
	}
	r, _ := name.ParseReference(ref)
	desc, err := remote.Get(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(desc.Manifest, &m)
	if _, has := m["artifactType"]; has {
		t.Errorf("artifactType set")
	}

	tree, err := f.Fetch(context.Background(), "ns", dxv1.Source{Image: &dxv1.ImageSource{Ref: ref, Path: "/export"}})
	if err != nil {
		t.Fatal(err)
	}
	// DXXMLAccess: files [xmlaccess/01-pages.xml], or every .xml of the tree (only that one)
	for _, files := range [][]string{{bundlePagesFile}, nil} {
		names, err := requestFiles(tree.Dir, files)
		if err != nil || len(names) != 1 || names[0] != bundlePagesFile {
			t.Errorf("requestFiles(%v) = %v, %v", files, names, err)
		}
	}
	// DXWCMLibraryImport: not a JCR tree itself, one archive inside
	if _, err := wcmexport.Open(tree.Dir); err == nil {
		t.Errorf("the bundle should not read as a JCR tree before unpacking")
	}
	archive, err := singleArchive(tree.Dir)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "x")
	if _, err := source.ExtractArchive(archive, out); err != nil {
		t.Fatal(err)
	}
	exp, err := wcmexport.Open(out)
	if err != nil || len(exp.Libraries) != 2 {
		t.Fatalf("libraries %v %v", exp, err)
	}
}
