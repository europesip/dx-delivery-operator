package source

import (
	"archive/tar"
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func tarOf(t *testing.T, names ...string) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, n := range names {
		typ := byte(tar.TypeReg)
		if strings.HasSuffix(n, "/") {
			typ = tar.TypeDir
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: typ, Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &b
}

func TestFoldersWithFilesSkipsOperatingSystem(t *testing.T) {
	got, err := foldersWithFiles(tarOf(t, "etc/passwd", "usr/bin/sh", "export/", "export/lib1.tar.bz2", "export/sub/x.xml", "app/", "root.txt", "data/a"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"data", "export"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNoFilesErrorNamesTheFolders(t *testing.T) {
	e := &NoFilesError{Source: "image x:1", Folder: "/app", Candidates: []string{"export"}}
	if !strings.Contains(e.Error(), "folders with files: /export") {
		t.Fatalf("message: %s", e.Error())
	}
}
