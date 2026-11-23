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
