// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"archive/zip"
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

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
	"github.com/europesip/dx-delivery-operator/internal/dx"
	"github.com/europesip/dx-delivery-operator/internal/source"
	"github.com/europesip/dx-delivery-operator/internal/wcmexport"
)

func libraryNode(n int, title string) string {
	d := string(rune('0' + n))
	return `<?xml version="1.0" encoding="UTF-8"?><icm:node xmlns:icm="http://www.ibm.com/software/data/cm/icm" icm:path="/contentRoot" icm:uuid="u` + d + `">` +
		`<sv:node sv:name="icm:libraries[` + d + `]" xmlns:sv="http://www.jcp.org/jcr/sv/1.0">` +
		`<sv:property sv:name="icm:title" sv:type="String"><sv:value>` + title + `</sv:value></sv:property></sv:node></icm:node>`
}

// exportZip writes a zip laid out like the one dxFileTransfer downloads (the JCR tree at the root of the zip).
func exportZip(t *testing.T, p string) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"554ee7f5/423dc4e4/aaaa1111.node":        libraryNode(1, "orbita"),
		"554ee7f5/423dc4e4/aaaa1111/home/c.node": "<x/>",
		"554ee7f5/423dc4e4/bbbb2222.node":        libraryNode(2, "Social Lists 1.0"),
	} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// What DXWCMLibraryExport pushes (the zip and EXPORT-INFO.txt, no artifactType) is a source that DXWCMLibraryImport
// reads: one archive in the tree, unpacked into a JCR export with both libraries.
func TestExportArtifactIsImportable(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	work := t.TempDir()
	zipPath := filepath.Join(work, "wcm-export-x.zip")
	exportZip(t, zipPath)
	info := filepath.Join(work, exportInfoFile)
	conn := &Connection{Config: dx.Config{BaseURL: "https://dx.example.com"}}
	o := &dxv1.DXWCMLibraryExport{}
	o.Namespace, o.Name = "ns", "x"
	o.Spec.ConnectionRef.Name = "dx"
	ref := host + "/europesip/wcm/x:1"
	o.Spec.Destination.Image = ref
	o.Annotations = map[string]string{source.ResChangedBy: "user:default/andres", source.ResReason: "copy to prod", source.ResCreatedWith: "backstage-europesip 1.0.0; @europesip/backstage-plugin-hcl-dx 0.6.0"}
	o.Spec.Destination.Attributes = map[string]string{"customer": "europesip"}
	libs := []dxv1.WCMExportedLibrary{{Title: "orbita", Files: 2}, {Title: "Social Lists 1.0", Files: 1}}
	text := exportInfo(conn, o, "HCL Digital Experience Compose/9.5 build 20261103-170843", libs, 123, time.Date(2026, 11, 19, 7, 0, 0, 0, time.UTC))
	for _, want := range []string{"https://dx.example.com", "2026-11-19 07:00:00", "Libraries:       2", "- orbita", "import-wcm-data", "build 20261103-170843", ref,
		"Requested by:    andres", "Reason:          copy to prod", "Operator:        dx-delivery-operator", "@europesip/backstage-plugin-hcl-dx 0.6.0", "Attributes:      customer=europesip"} {
		if !strings.Contains(text, want) {
			t.Errorf("EXPORT-INFO.txt lacks %q:\n%s", want, text)
		}
	}
	if err := os.WriteFile(info, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &source.Fetcher{Client: fake.NewClientBuilder().Build(), CacheDir: t.TempDir()}
	digest, err := f.PushFiles(context.Background(), "ns", ref, nil,
		[]source.ArtifactFile{{Path: zipPath, MediaType: exportZipMediaType}, {Path: info, MediaType: exportInfoMediaType}}, map[string]string{"org.opencontainers.image.source": "https://dx.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest %s", digest)
	}

	// the manifest: empty config, no artifactType, one layer per file named by its title
	r, _ := name.ParseReference(ref)
	desc, err := remote.Get(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(desc.Manifest, &m)
	if _, has := m["artifactType"]; has {
		t.Errorf("artifactType set: %s", desc.Manifest)
	}
	if cfg := m["config"].(map[string]any); cfg["mediaType"] != source.EmptyConfigMediaType {
		t.Errorf("config: %v", cfg)
	}

	// read back as the import does
	tree, err := f.Fetch(context.Background(), "ns", dxv1.Source{Image: &dxv1.ImageSource{Ref: ref, Path: "/app"}})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(tree.Dir, exportInfoFile)); err != nil || !strings.Contains(string(b), "orbita") {
		t.Errorf("EXPORT-INFO.txt: %v", err)
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
	if err != nil {
		t.Fatal(err)
	}
	if got := wcmexport.Titles(exp.Libraries); len(got) != 2 {
		t.Errorf("libraries %v", got)
	}
}

func TestUniqueTitles(t *testing.T) {
	got, err := uniqueTitles([]string{" orbita", "Orbita", "B"})
	if err != nil || len(got) != 2 || got[0] != "orbita" {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := uniqueTitles([]string{" "}); err == nil {
		t.Fatal("empty title accepted")
	}
}
