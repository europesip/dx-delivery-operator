// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package controller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dxv1 "github.com/europesip/dx-delivery-operator/api/v1alpha1"
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

func TestOCITag(t *testing.T) {
	at := time.Date(2026, 11, 17, 21, 5, 9, 0, time.UTC)
	if got := ociTag("", at); got != "20261117-210509" {
		t.Errorf("default: %s", got)
	}
	if got := ociTag("dx-test-${DATE}", at); got != "dx-test-20261117-210509" {
		t.Errorf("template: %s", got)
	}
}

func TestOutputKey(t *testing.T) {
	if k := outputKey("dir/sub file.xml"); k != "dir__sub_file.xml" {
		t.Errorf("got %s", k)
	}
}

func TestSaveOutputNeverCuts(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = dxv1.AddToScheme(scheme)
	o := &dxv1.DXXMLAccess{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns", UID: "u1"}}
	o.Spec.Output = &dxv1.XMLAccessOutput{ConfigMap: &dxv1.LocalObjectReference{Name: "out"}}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &DXXMLAccessReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()
	read := func() map[string]string {
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "out"}, cm); err != nil {
			t.Fatal(err)
		}
		return cm.Data
	}

	// small answers: one key per request
	size, err := r.saveOutput(ctx, o, map[string]string{"a/b.xml": "<ok/>"})
	if err != nil || size != 5 || read()["a__b.xml"] != "<ok/>" {
		t.Fatalf("small: size %d err %v data %v", size, err, read())
	}
	// too big without OCI: only README, never a cut copy
	big := strings.Repeat("x", maxOutputBytes+1)
	size, err = r.saveOutput(ctx, o, map[string]string{"big.xml": big})
	d := read()
	if err != nil || size <= maxOutputBytes || len(d) != 1 || !strings.Contains(d["README"], "set output.oci") {
		t.Fatalf("big: size %d err %v keys %d readme %q", size, err, len(d), d["README"])
	}
	// too big with the answers already in the registry: README points to them
	o.Status.OutputRef, o.Status.OutputDigest = "harbor.example.com/dx-exports/x:1", "sha256:abc"
	if _, err = r.saveOutput(ctx, o, map[string]string{"big.xml": big}); err != nil || !strings.Contains(read()["README"], "oras pull harbor.example.com/dx-exports/x:1") {
		t.Fatalf("big+oci: %v %q", err, read()["README"])
	}
}

func TestObjectIDs(t *testing.T) {
	req := `<request type="update"><portal action="locate"/></request>`
	o := &dxv1.DXXMLAccess{}
	inline := func(only string) string {
		reqs, _, err := (&DXXMLAccessReconciler{}).requests(context.Background(), o, only)
		if err != nil {
			t.Fatal(err)
		}
		return reqs[0].Body
	}
	o.Spec.XML = req
	if got := inline(""); got != req {
		t.Errorf("Keep (default) must send the request as it is: %s", got)
	}
	o.Spec.ObjectIDs = "Keep"
	if got := inline(""); got != req {
		t.Errorf("Keep: %s", got)
	}
	o.Spec.ObjectIDs = ObjectIDsNew
	if got := inline(""); got != `<request type="update" create-oids="true"><portal action="locate"/></request>` {
		t.Errorf("New: %s", got)
	}
	o.Spec.Cleanup = &dxv1.XMLAccessCleanup{XML: `<request type="update"><portal action="locate"/></request>`}
	if got := inline(xmlInlineName); strings.Contains(got, "create-oids") {
		t.Errorf("the cleanup request is sent as written: %s", got)
	}
}
