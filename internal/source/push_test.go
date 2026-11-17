package source

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
)

// An artifact pushed by the operator is read back as files by the operator itself (as "oras pull" would).
func TestPushAndReadBackFileArtifact(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	f := &Fetcher{Client: fake.NewClientBuilder().Build(), CacheDir: t.TempDir()}
	ref := host + "/dx-wcm/xmlaccess-out:t1"
	got, err := f.Push(context.Background(), "ns", ref, nil, map[string][]byte{"a.xml": []byte("<request/>"), "dir__b.xml": []byte("<b/>")}, "application/xml", map[string]string{"dx.europesip.com/resource": "ns/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, host+"/dx-wcm/xmlaccess-out@sha256:") {
		t.Errorf("digest ref: %s", got)
	}
	tree, err := f.Fetch(context.Background(), "ns", dxv1.Source{Image: &dxv1.ImageSource{Ref: ref}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(tree.Dir, "a.xml"))
	if err != nil || string(b) != "<request/>" {
		t.Errorf("a.xml: %q %v", b, err)
	}
}
