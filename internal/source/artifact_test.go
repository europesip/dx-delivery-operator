package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
)

func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for n, c := range files {
		_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(c))
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

// An ORAS artifact (oras push <ref> export.tar.gz): one layer that is the file itself, named by the title annotation.
func TestORASFileArtifact(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	archive := tgz(t, map[string]string{"554ee7f5/423dc4e4/aaaa.node": "<x/>", "554ee7f5/423dc4e4/aaaa/b.node": "<y/>"})
	img, err := mutate.Append(empty.Image, mutate.Addendum{
		Layer:       static.NewLayer(archive, types.MediaType("application/vnd.europesip.dx.wcm.export.v1.tar+gzip")),
		Annotations: map[string]string{"org.opencontainers.image.title": "exportLibrerias.tar.gz"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := name.ParseReference(strings.TrimPrefix(srv.URL, "http://") + "/dx-wcm/raw:1")
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	f := &Fetcher{Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(), CacheDir: t.TempDir()}
	tree, err := f.Fetch(context.Background(), "ns", dxv1.Source{Image: &dxv1.ImageSource{Ref: ref.String(), Path: "/app"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Files) != 1 || tree.Files[0].Path != "exportLibrerias.tar.gz" || !IsArchive(tree.Files[0].Path) {
		t.Fatalf("files %+v", tree.Files)
	}
	out := t.TempDir()
	n, err := ExtractArchive(filepath.Join(tree.Dir, "exportLibrerias.tar.gz"), out)
	if err != nil || n != 2 {
		t.Fatalf("extract: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(out, "554ee7f5/423dc4e4/aaaa/b.node")); err != nil {
		t.Fatal(err)
	}
}

// A folder pushed with "oras push <ref> 554ee7f5" (Sergio, CF230): one tar+gzip layer whose entries start with the
// folder name, annotation io.deis.oras.content.unpack=true. It is unpacked as "oras pull" does: dir/554ee7f5/...,
// not dir/554ee7f5/554ee7f5/... (#41).
func TestORASFolderArtifact(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	archive := tgz(t, map[string]string{"554ee7f5/423dc4e4/aaaa.node": "<x/>", "554ee7f5/423dc4e4/aaaa/b.node": "<y/>"})
	img, err := mutate.Append(empty.Image, mutate.Addendum{
		Layer: static.NewLayer(archive, types.MediaType("application/vnd.oci.image.layer.v1.tar+gzip")),
		Annotations: map[string]string{"org.opencontainers.image.title": "554ee7f5", "io.deis.oras.content.unpack": "true"},
	}, mutate.Addendum{
		Layer:       static.NewLayer([]byte("leeme"), types.MediaType("application/vnd.oci.image.layer.v1.tar")),
		Annotations: map[string]string{"org.opencontainers.image.title": "leeme.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := name.ParseReference(strings.TrimPrefix(srv.URL, "http://") + "/dx-wcm/folder:1")
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	f := &Fetcher{Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(), CacheDir: t.TempDir()}
	tree, err := f.Fetch(context.Background(), "ns", dxv1.Source{Image: &dxv1.ImageSource{Ref: ref.String(), Path: "/app"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, "554ee7f5/423dc4e4/aaaa/b.node")); err != nil {
		t.Fatalf("folder layer not unpacked as oras does: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, "554ee7f5/554ee7f5")); err == nil {
		t.Fatal("folder layer unpacked one level too deep")
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, "leeme.txt")); err != nil {
		t.Fatal(err)
	}
}
