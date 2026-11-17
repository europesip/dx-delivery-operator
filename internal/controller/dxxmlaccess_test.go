package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	dxv1 "github.com/europesip/hcl-dx-scriptapp-operator/api/v1alpha1"
)

func TestSubstitute(t *testing.T) {
	got := substitute(`<url-mapping-context label="${LABEL}"/><x a="${UNKNOWN}"/>`, map[string]string{"LABEL": "home"})
	if got != `<url-mapping-context label="home"/><x a="${UNKNOWN}"/>` {
		t.Errorf("got %s", got)
	}
}

func TestCheckRequest(t *testing.T) {
	typ, err := checkRequest(`<?xml version="1.0"?><request type="export" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><portal action="locate"/></request>`)
	if err != nil || typ != "export" {
		t.Errorf("valid request: %s %v", typ, err)
	}
	if _, err := checkRequest(`<request type="update"><portal action="locate"></request>`); err == nil {
		t.Error("a broken request must be rejected")
	}
	if _, err := checkRequest(`<portal/>`); err == nil {
		t.Error("a file without <request type=...> must be rejected")
	}
}

func TestRequestFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"b/02.xml", "01.xml", "readme.md"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		_ = os.WriteFile(filepath.Join(dir, f), []byte("<request type=\"update\"/>"), 0o600)
	}
	all, err := requestFiles(dir, nil)
	if err != nil || strings.Join(all, ",") != "01.xml,b/02.xml" {
		t.Errorf("every xml sorted: %v %v", all, err)
	}
	some, err := requestFiles(dir, []string{"b/02.xml", "01.xml"})
	if err != nil || strings.Join(some, ",") != "b/02.xml,01.xml" {
		t.Errorf("given order: %v %v", some, err)
	}
	if _, err := requestFiles(dir, []string{"missing.xml"}); err == nil {
		t.Error("a missing file must be an error")
	}
	if got, err := requestFiles(dir, []string{"../../etc/passwd"}); err == nil {
		t.Errorf("a path outside the source must not be read: %v", got)
	}
}

func TestXMLHash(t *testing.T) {
	o := &dxv1.DXXMLAccess{Spec: dxv1.DXXMLAccessSpec{XML: "<request type=\"update\"/>"}}
	h := xmlHash(o, "")
	o.Spec.Suspend = true
	o.Spec.Cleanup = &dxv1.XMLAccessCleanup{XML: "x"}
	if xmlHash(o, "") != h {
		t.Error("suspend and cleanup must not start a new run")
	}
	if xmlHash(o, "rev2") == h {
		t.Error("a new source revision must start a new run")
	}
	o.Annotations = map[string]string{dxv1.AnnotationReconcile: "now"}
	if xmlHash(o, "") == h {
		t.Error("the reconcile annotation must start a new run")
	}
}

func TestOutputKey(t *testing.T) {
	if k := outputKey("dir/sub file.xml"); k != "dir__sub_file.xml" {
		t.Errorf("got %s", k)
	}
}
