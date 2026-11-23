//go:build e2e

package controller

// Manual end-to-end check of DXExport against a real DX and registry (never in CI):
//   DX_URL=... DX_USER=... DX_PASS=... BUNDLE_PAGES=orbita-site BUNDLE_LIBS=orbita BUNDLE_PORTAL_SITE=true \
//   BUNDLE_IMAGE=harbor.../sites/e2e:1 PUSH_DOCKERCONFIG=/path/to/.dockerconfigjson \
//   go test -tags e2e -run TestDXExportE2E ./internal/controller/
// It exports, pushes the bundle and reads it back as DXXMLAccess and DXWCMLibraryImport would.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/dx"
	"github.com/europesip/dx-delivery-operator/internal/source"
	"github.com/europesip/dx-delivery-operator/internal/wcmexport"
)

func TestDXExportE2E(t *testing.T) {
	if os.Getenv("DX_URL") == "" {
		t.Skip("DX_URL not set")
	}
	ctx := context.Background()
	cfg := dx.Config{BaseURL: os.Getenv("DX_URL"), Username: os.Getenv("DX_USER"), Password: os.Getenv("DX_PASS"), ContentHandlerPath: "/wps/mycontenthandler"}
	c, err := dx.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := os.ReadFile(os.Getenv("PUSH_DOCKERCONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	sec := &corev1.Secret{Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: dc}}
	sec.Namespace, sec.Name = "ns", "push"
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = dxv1.AddToScheme(s)
	o := &dxv1.DXExport{}
	o.Namespace, o.Name = "ns", "e2e-bundle"
	o.Annotations = map[string]string{source.ResChangedBy: "user:default/e2e", source.ResReason: "e2e test of DXExport"}
	o.Spec.ConnectionRef.Name = "dx"
	for _, p := range strings.Split(os.Getenv("BUNDLE_PAGES"), ",") {
		if p != "" {
			o.Spec.Parts.Pages = append(o.Spec.Parts.Pages, dxv1.DXExportPage{UniqueName: p})
		}
	}
	for _, l := range strings.Split(os.Getenv("BUNDLE_LIBS"), ",") {
		if l != "" {
			o.Spec.Parts.Libraries = append(o.Spec.Parts.Libraries, l)
		}
	}
	o.Spec.Parts.PortalSite = os.Getenv("BUNDLE_PORTAL_SITE") == "true"
	o.Spec.Destination = dxv1.WCMExportDestination{Image: os.Getenv("BUNDLE_IMAGE"), PushSecrets: []dxv1.LocalObjectReference{{Name: "push"}}, Attributes: map[string]string{"purpose": "e2e"}}
	kc := fake.NewClientBuilder().WithScheme(s).WithObjects(sec, o).WithStatusSubresource(o).Build()
	f := &source.Fetcher{Client: kc, CacheDir: t.TempDir()}
	r := &DXExportReconciler{Client: kc, Scheme: s, Shared: &Shared{Fetcher: f, WorkDir: t.TempDir(), Recorder: logRecorder{t}}}
	if err := r.export(ctx, o, &Connection{Config: cfg, Client: c}); err != nil {
		t.Fatal(err)
	}
	t.Logf("status: %s; image %s; %d bytes; pages %+v; libraries %+v; warnings %v; files %+v", o.Status.Message, o.Status.Image, o.Status.TotalBytes,
		o.Status.Pages, o.Status.ExportedLibraries, o.Status.Warnings, o.Status.Files)
	tree, err := f.Fetch(ctx, "ns", dxv1.Source{Image: &dxv1.ImageSource{Ref: o.Status.Image, Path: "/export", PullSecrets: []dxv1.LocalObjectReference{{Name: "push"}}}})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.ReadFile(filepath.Join(tree.Dir, exportInfoFile))
	t.Logf("EXPORT-INFO.txt:\n%s", info)
	if len(o.Spec.Parts.Pages) > 0 {
		names, err := requestFiles(tree.Dir, []string{bundlePagesFile})
		if err != nil {
			t.Fatal(err)
		}
		x, _ := os.ReadFile(filepath.Join(tree.Dir, names[0]))
		t.Logf("%s: %d bytes, starts %.300s", names[0], len(x), x)
	}
	if len(o.Spec.Parts.Libraries) > 0 || o.Spec.Parts.PortalSite {
		a, err := singleArchive(tree.Dir)
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "x")
		if _, err := source.ExtractArchive(a, out); err != nil {
			t.Fatal(err)
		}
		exp, err := wcmexport.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("read back: %v", wcmexport.Titles(exp.Libraries))
	}
}
