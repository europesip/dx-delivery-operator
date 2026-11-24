// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package javaext

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestStartupToken(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	tok := StartupToken(key, "portal", "dx-deployment-web-engine")
	if !ValidStartupToken(key, "portal", "dx-deployment-web-engine", tok) {
		t.Fatal("own token rejected")
	}
	if ValidStartupToken(key, "other", "dx-deployment-web-engine", tok) || ValidStartupToken(key, "portal", "x", tok) {
		t.Fatal("a token must be valid for one Web Engine only")
	}
	if ValidStartupToken(nil, "portal", "dx-deployment-web-engine", tok) || ValidStartupToken([]byte("other-key-000000"), "portal", "dx-deployment-web-engine", tok) {
		t.Fatal("token with another key accepted")
	}
}

func TestStartupVolume(t *testing.T) {
	const dir = "/opt/openliberty/wlp/usr/svrcfg/bin/customer/startup"
	sts := &appsv1.StatefulSet{}
	sts.Spec.Template.Spec.Containers = []corev1.Container{{Name: "web-engine", VolumeMounts: []corev1.VolumeMount{{Name: "custom-startup", MountPath: dir + "/"}}}}
	sts.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "custom-startup", VolumeSource: corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "dx-deployment-web-engine-custom-startup"}}}}
	if got := StartupVolume(sts, "web-engine", dir); got != "dx-deployment-web-engine-custom-startup" {
		t.Fatalf("claim %q", got)
	}
	if got := StartupVolume(sts, "other", dir); got != "" {
		t.Fatalf("other container: %q", got)
	}
}

func TestStartupScript(t *testing.T) {
	s := StartupScript("http://op.ns.svc:8090", "portal's", "dx-deployment-web-engine")
	for _, want := range []string{"#!/bin/bash", "SPDX-License-Identifier: MIT", "abort_container", "log_message_customer",
		StartupHookPath, StartupTokenName, "OPERATOR_URL='http://op.ns.svc:8090'", `NAMESPACE='portal'\''s'`, "configDropins/defaults", "exit 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("script without %q", want)
		}
	}
}

func TestWriteSeed(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSeed(dir, []SeedFile{{Name: StartupScriptName, Content: "#!/bin/bash\n", Mode: 0o750}, {Name: StartupTokenName, Content: "tok", Mode: 0o640}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, StartupScriptName))
	if err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("script mode %v %v", fi, err)
	}
	if err := WriteSeed(dir, []SeedFile{{Name: "../x.sh"}}); err == nil {
		t.Fatal("a path outside the folder must be refused")
	}
}

// BuildBundle: a portlet WAR from a registry ends up exploded under apps/, with the Liberty configuration and the
// summary; a missing optional extension is skipped, a missing required one is an error.
func TestBuildBundle(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	img, err := mutate.Append(empty.Image, mutate.Addendum{
		Layer:       static.NewLayer(testWAR(t), types.MediaType("application/java-archive")),
		Annotations: map[string]string{"org.opencontainers.image.title": "showcase.war"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := name.ParseReference(strings.TrimPrefix(srv.URL, "http://") + "/jx/showcase:1.0")
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	digest, repo, err := Resolve(context.Background(), ref.String(), authn.DefaultKeychain)
	if err != nil {
		t.Fatal(err)
	}
	kc := func(Entry) (authn.Keychain, error) { return authn.DefaultKeychain, nil }
	es := []Entry{{Name: "showcase", Type: "portlet", Ref: repo + "@" + digest, File: "showcase.war", ContextRoot: "/showcase"},
		{Name: "gone", Type: "portlet", Ref: repo + "@sha256:" + strings.Repeat("0", 64), File: "gone.war", Optional: true}}
	res, err := BuildBundle(context.Background(), es, kc)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Loaded["showcase"] || res.Loaded["gone"] || len(res.Skipped) != 1 || res.Summary != "showcase@"+digest {
		t.Fatalf("result %+v", res)
	}
	files := map[string]string{}
	gz, _ := gzip.NewReader(bytes.NewReader(res.Data))
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = string(b)
	}
	if !strings.Contains(files["apps/showcase.war/WEB-INF/portlet.xml"], "ShowcasePortlet") {
		t.Fatalf("WAR not exploded under apps/: %v", keys(files))
	}
	if !strings.Contains(files["config/"+LibertyFileName], `contextRoot="showcase"`) || strings.Contains(files["config/"+LibertyFileName], "gone.war") {
		t.Fatalf("liberty config %q", files["config/"+LibertyFileName])
	}
	es[1].Optional = false
	if _, err := BuildBundle(context.Background(), es, kc); err == nil {
		t.Fatal("a missing required extension must fail the bundle")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
