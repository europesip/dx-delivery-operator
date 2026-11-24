// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package javaext

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

const testPortletXML = `<?xml version="1.0" encoding="UTF-8"?>
<portlet-app xmlns="http://java.sun.com/xml/ns/portlet/portlet-app_2_0.xsd" version="2.0" id="com.europesip.showcase">
  <portlet><portlet-name>ShowcasePortlet</portlet-name></portlet>
  <portlet><portlet-name>AnotherPortlet</portlet-name></portlet>
</portlet-app>`

func testWAR(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, body := range map[string]string{"WEB-INF/portlet.xml": testPortletXML, "WEB-INF/web.xml": "<web-app/>", "css/app.css": "body{}"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return b.Bytes()
}

func testTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

func TestReadPortletXML(t *testing.T) {
	for _, data := range [][]byte{testWAR(t), testTarGz(t, map[string]string{"hello.war/WEB-INF/portlet.xml": testPortletXML})} {
		app, err := ReadPortletXML(data)
		if err != nil {
			t.Fatal(err)
		}
		if app.UID != "com.europesip.showcase" || strings.Join(app.Portlets, ",") != "AnotherPortlet,ShowcasePortlet" {
			t.Fatalf("got %+v", app)
		}
	}
	if _, err := ReadPortletXML(testTarGz(t, map[string]string{"x.txt": "no"})); err == nil {
		t.Fatal("expected an error without portlet.xml")
	}
}

func TestUnpackWAR(t *testing.T) {
	dir := t.TempDir()
	n, err := UnpackWAR(testWAR(t), filepath.Join(dir, "a.war"))
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.war", "WEB-INF", "portlet.xml")); err != nil {
		t.Fatal(err)
	}
	// a tar of an expanded WAR with a top folder: the folder is stripped
	n, err = UnpackWAR(testTarGz(t, map[string]string{"hello.war/WEB-INF/web.xml": "<web-app/>", "hello.war/index.jsp": "x"}), filepath.Join(dir, "b.war"))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.war", "WEB-INF", "web.xml")); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackWAR(testTarGz(t, map[string]string{"../../etc/passwd": "x"}), filepath.Join(dir, "c.war")); err == nil {
		t.Fatal("expected unsafe path error")
	}
}

func TestWriteJARs(t *testing.T) {
	dir := t.TempDir()
	names, err := WriteJARs(testWAR(t), "lib.jar", dir) // a zip is written as is
	if err != nil || len(names) != 1 || names[0] != "lib.jar" {
		t.Fatalf("%v %v", names, err)
	}
	names, err = WriteJARs(testTarGz(t, map[string]string{"libs/a.jar": "x", "libs/b.jar": "y", "README": "z"}), "x.jar", dir)
	if err != nil || len(names) != 2 {
		t.Fatalf("%v %v", names, err)
	}
}

func TestInject(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web-engine"}, {Name: "system-out-log"}}}}
	es := []Entry{
		{Name: "showcase", Type: "portlet", Ref: "h/r/showcase@sha256:aa", File: "showcase.war", ContextRoot: "/showcase", PullSecrets: []string{"harbor"}},
		{Name: "lib", Type: "sharedLibrary", Ref: "h/r/lib@sha256:bb", File: "lib.jar", PullSecrets: []string{"harbor"}},
	}
	ok, err := Inject(pod, es, InjectOptions{Container: "web-engine", InitImage: "init:1"})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Image != "init:1" {
		t.Fatalf("init: %+v", pod.Spec.InitContainers)
	}
	var got []Entry
	_ = json.Unmarshal([]byte(pod.Spec.InitContainers[0].Env[0].Value), &got)
	if len(got) != 2 || got[0].Name != "lib" { // sorted by name
		t.Fatalf("entries %+v", got)
	}
	mounts := map[string]string{}
	for _, m := range pod.Spec.Containers[0].VolumeMounts {
		mounts[m.MountPath] = m.Name
	}
	if mounts[AppsDir] != VolumeApps || mounts[LibsDir] != VolumeLibs || mounts[ConfigDefaults] != VolumeConfig {
		t.Fatalf("mounts %v", mounts)
	}
	if len(pod.Spec.Containers[1].VolumeMounts) != 0 {
		t.Fatal("the sidecars must not change")
	}
	if pod.Annotations["dx.europesip.com/java-extensions"] != "lib@sha256:bb,showcase@sha256:aa" {
		t.Fatalf("annotation %q", pod.Annotations["dx.europesip.com/java-extensions"])
	}
	// idempotent
	if ok, _ := Inject(pod, es, InjectOptions{Container: "web-engine", InitImage: "init:1"}); ok {
		t.Fatal("second injection must do nothing")
	}
}

func TestLibertyXML(t *testing.T) {
	x := LibertyXML([]Entry{{Name: "a", Type: "portlet", File: "a.war", ContextRoot: "/a"}, {Name: "b", Type: "sharedLibrary", File: "b.jar"}, {Name: "c", Type: "portlet", File: "c.war", ContextRoot: "/c"}}, map[string]bool{"a": true, "b": true})
	if !strings.Contains(x, `contextRoot="a"`) || strings.Contains(x, "b.jar") || strings.Contains(x, "c.war") {
		t.Fatal(x)
	}
}

func TestRegisterXMLAndExport(t *testing.T) {
	x := RegisterXML(Registration{App: PortletApp{UID: "com.x", Portlets: []string{"P1"}}, File: "x.war", ContextRoot: "/x", DisplayName: "x",
		Page: &Page{UniqueName: "com.x.page", Titles: map[string]string{"en": "X & Y"}, FriendlyName: "x", Anonymous: true}})
	for _, want := range []string{`uid="com.x.webmod"`, `predeployed="true"`, "customApplications/x.war</url>", `<context-root>/x</context-root>`,
		`uniquename="ibm.portal.Home"`, `X &amp; Y`, `portletref="jx.portlet.0"`, "anonymous portal user"} {
		if !strings.Contains(x, want) {
			t.Fatalf("missing %q in\n%s", want, x)
		}
	}
	ok := `<request><portal action="locate"><web-app uid="com.x.webmod"><portlet-app name="com.x"><portlet action="update" name="P2" objectid="a"/><portlet action="update" name="P1"/></portlet-app></web-app></portal><status element="all" result="ok"/></request>`
	exists, ps, err := ParseExport(ok)
	if err != nil || !exists || strings.Join(ps, ",") != "P1,P2" {
		t.Fatalf("%v %v %v", exists, ps, err)
	}
	missing := `<status element="all" result="failed"><message id="EJPXA0240E">EJPXA0240E: The resource could not be found</message></status>`
	if exists, _, err := ParseExport(missing); err != nil || exists {
		t.Fatalf("missing: %v %v", exists, err)
	}
	if _, _, err := ParseExport(`<status element="all" result="failed"><message>EJPXA0001E: internal error</message></status>`); err == nil {
		t.Fatal("other failures must be errors")
	}
	if !strings.Contains(UnregisterXML("com.x", "com.x.page"), `<web-app action="delete" uid="com.x.webmod"/>`) {
		t.Fatal("unregister")
	}
}

// Issue #63: the init container runs as the user of DX (pod level, or the Web Engine container when the pod has none).
func TestInjectSameUserAsWebEngine(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	es := []Entry{{Name: "p", Type: "portlet", Ref: "h/r/p@sha256:aa", File: "p.war", ContextRoot: "/p"}}
	opts := InjectOptions{Container: "web-engine", InitImage: "init:1"}

	// HCL chart: UID/GID at pod level, the init container inherits them (nothing set on it)
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		SecurityContext: &corev1.PodSecurityContext{RunAsUser: i64(1000), RunAsGroup: i64(1001)},
		Containers:      []corev1.Container{{Name: "web-engine"}}}}
	if _, err := Inject(pod, es, opts); err != nil {
		t.Fatal(err)
	}
	if sc := pod.Spec.InitContainers[0].SecurityContext; sc.RunAsUser != nil || sc.RunAsGroup != nil {
		t.Fatalf("pod level UID: the init container must inherit it, got %+v", sc)
	}

	// only the Web Engine container has a UID: the init container takes it
	pod = &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web-engine",
		SecurityContext: &corev1.SecurityContext{RunAsUser: i64(1000), RunAsGroup: i64(1001)}}}}}
	if _, err := Inject(pod, es, opts); err != nil {
		t.Fatal(err)
	}
	if sc := pod.Spec.InitContainers[0].SecurityContext; sc.RunAsUser == nil || *sc.RunAsUser != 1000 || sc.RunAsGroup == nil || *sc.RunAsGroup != 1001 {
		t.Fatalf("container level UID: want 1000/1001, got %+v", sc)
	}

	// no UID anywhere (OpenShift without the HCL annotations): nothing is set, the SCC decides
	pod = &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web-engine"}}}}
	if _, err := Inject(pod, es, opts); err != nil {
		t.Fatal(err)
	}
	if sc := pod.Spec.InitContainers[0].SecurityContext; sc.RunAsUser != nil {
		t.Fatalf("no UID: want none, got %+v", sc)
	}
}

// Issue #69: stable unique names and the IDs DX reports.
func TestStableIdentifiers(t *testing.T) {
	r := Registration{App: PortletApp{UID: "com.x", Portlets: []string{"P1", "P2"}}, File: "x.war", ContextRoot: "/x", DisplayName: "x",
		IDs: Identifiers{UniqueNames: true}}
	x := RegisterXML(r)
	for _, want := range []string{`name="P1" objectid="jx.portlet.0" uniquename="com.x.P1"`, `name="P2" objectid="jx.portlet.1" uniquename="com.x.P2"`} {
		if !strings.Contains(x, want) {
			t.Fatalf("missing %q in\n%s", want, x)
		}
	}
	r.IDs.UniqueNames = false
	if strings.Contains(RegisterXML(r), "uniquename=") {
		t.Fatal("no unique names when disabled")
	}
	export := `<request><portal action="locate">
<web-app action="update" active="true" domain="rel" objectid="Z1_AAA" predeployed="true" uid="com.x.webmod">
<portlet-app action="update" active="true" name="com.x" objectid="Z2_BBB" uid="com.x">
<portlet action="update" active="true" name="P2" objectid="Z3_DDD" servletref="ZV_1">
<portlet action="update" active="true" name="P1" objectid="Z3_CCC" uniquename="com.x.P1" provided="false">
</portlet-app></web-app></portal><status element="all" result="ok"/></request>`
	ids := ParseIDs(export)
	if ids.WebModuleOID != "Z1_AAA" || ids.PortletAppOID != "Z2_BBB" || len(ids.Portlets) != 2 ||
		ids.Portlets[0] != (RegisteredPortlet{Name: "P1", UniqueName: "com.x.P1", ObjectID: "Z3_CCC"}) ||
		ids.Portlet("P2").ObjectID != "Z3_DDD" {
		t.Fatalf("ids %+v", ids)
	}
	if m := MissingUniqueNames("com.x", ids); len(m) != 1 || m[0] != "P2" {
		t.Fatalf("missing unique names %v", m)
	}
}

// Issue #69: the schema wants web-app before content-node, or nothing is deleted.
func TestUnregisterOrder(t *testing.T) {
	x := UnregisterXML("com.x", "com.x.page")
	if strings.Index(x, "<web-app") > strings.Index(x, "<content-node") {
		t.Fatalf("web-app must come first:\n%s", x)
	}
}
