//go:build e2e

package controller

// Manual end-to-end check of DXWCMLibraryExport against a real DX and registry (never in CI):
//   DX_URL=... DX_USER=... DX_PASS=... EXPORT_LIBS="orbita,Social Lists 1.0" EXPORT_IMAGE=harbor.../wcm/e2e:1 \
//   PUSH_DOCKERCONFIG=/path/to/.dockerconfigjson go test -tags e2e -run TestExportE2E ./internal/controller/
// It exports the libraries, pushes the artifact and reads it back as DXWCMLibraryImport would.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/dx"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/source"
	"github.com/europesip/hcl-dx-scriptapp-operator/internal/wcmexport"
)

type logRecorder struct{ t *testing.T }

func (l logRecorder) Eventf(_ runtime.Object, _ runtime.Object, typ, reason, action, note string, args ...interface{}) {
	l.t.Logf("event %s %s %s: %s", typ, reason, action, fmt.Sprintf(note, args...))
}
func (l logRecorder) AnnotatedEventf(o runtime.Object, rel runtime.Object, _ map[string]string, typ, reason, action, note string, args ...interface{}) {
	l.Eventf(o, rel, typ, reason, action, note, args...)
}

func TestExportE2E(t *testing.T) {
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
	o := &dxv1.DXWCMLibraryExport{}
	o.Namespace, o.Name = "ns", "e2e"
	o.Spec.ConnectionRef.Name = "dx"
	o.Spec.Libraries = strings.Split(os.Getenv("EXPORT_LIBS"), ",")
	o.Spec.Destination = dxv1.WCMExportDestination{Image: os.Getenv("EXPORT_IMAGE"), PushSecrets: []dxv1.LocalObjectReference{{Name: "push"}}}
	kc := fake.NewClientBuilder().WithScheme(s).WithObjects(sec, o).WithStatusSubresource(o).Build()
	f := &source.Fetcher{Client: kc, CacheDir: t.TempDir()}
	r := &DXWCMLibraryExportReconciler{Client: kc, Scheme: s, Shared: &Shared{Fetcher: f, WorkDir: t.TempDir(), Recorder: logRecorder{t}}}
	if err := r.exportAll(ctx, o, &Connection{Config: cfg, Client: c}); err != nil {
		t.Fatal(err)
	}
	t.Logf("status: %s; image %s; %d bytes; %+v", o.Status.Message, o.Status.Image, o.Status.TotalBytes, o.Status.ExportedLibraries)
	tree, err := f.Fetch(ctx, "ns", dxv1.Source{Image: &dxv1.ImageSource{Ref: o.Status.Image, Path: "/app", PullSecrets: []dxv1.LocalObjectReference{{Name: "push"}}}})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.ReadFile(filepath.Join(tree.Dir, exportInfoFile))
	t.Logf("EXPORT-INFO.txt:\n%s", info)
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
