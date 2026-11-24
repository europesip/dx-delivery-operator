// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package dx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDXConnectSourcePath(t *testing.T) {
	for in, want := range map[string]string{
		"/opt/HCL/wp_profile/dxconnect/store/wcmlib/export-123/wcm.zip": "/wcmlib/export-123/wcm.zip",
		`C:\dx\store\wcmlib\x\y.zip`:                                    "/wcmlib/x/y.zip",
		"a/b.zip":                                                       "/a/b.zip",
	} {
		if got := dxConnectSourcePath(in); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestDXConnectExportAndImport(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != dxConnectPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != basicHeader("cw", "cwpw") || r.Header.Get("DXAuth") != basicHeader("wpsadmin", "pw") ||
			r.Header.Get("DXWASAuth") != basicHeader("was", "waspw") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		m := r.URL.Query().Get("requestMethod")
		calls = append(calls, m)
		switch m {
		case "exportWCMLibrary":
			if r.URL.Query().Get("libraries") != "'Lib A';'Lib B'" || r.URL.Query().Get("virtualPortalContext") != "vp1" || r.URL.Query().Get("DXCoreProfileName") != "wp_profile" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"exportWCMLibraryPath": "/opt/x/store/wcmlib/e1/out.zip"})
		case "download":
			if r.URL.Query().Get("sourcepath") != "/wcmlib/e1/out.zip" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte("ZIPDATA"))
		case "upload":
			f, _, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			b, _ := io.ReadAll(f)
			if string(b) != "ZIPDATA" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"filePath": `C:\store\upload\in.zip`})
		case "importWCMLibrary":
			var d map[string]string
			_ = json.NewDecoder(r.Body).Decode(&d)
			if d["filePath"] != "C:/store/upload/in.zip" || d["virtualPortalContext"] != "vp1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "imported"})
		}
	}))
	defer srv.Close()
	c, err := NewClient(Config{BaseURL: srv.URL, Username: "wpsadmin", Password: "pw", Runtime: "core",
		DXConnect: DXConnectConfig{URL: srv.URL, Username: "cw", Password: "cwpw", WASUsername: "was", WASPassword: "waspw", ProfileName: "wp_profile"}})
	if err != nil {
		t.Fatal(err)
	}
	if !c.UsesDXConnect() {
		t.Fatal("UsesDXConnect should be true")
	}
	zp := filepath.Join(t.TempDir(), "x.zip")
	n, err := c.DXConnectExportLibraries(context.Background(), "vp1", []string{"Lib A", "Lib B"}, zp, time.Minute)
	if err != nil || n != 7 {
		t.Fatalf("export: %d %v", n, err)
	}
	msg, err := c.DXConnectImportLibraries(context.Background(), "vp1", zp, time.Minute)
	if err != nil || msg != "imported" {
		t.Fatalf("import: %q %v", msg, err)
	}
	if strings.Join(calls, ",") != "exportWCMLibrary,download,upload,importWCMLibrary" {
		t.Fatalf("calls %v", calls)
	}
	if _, err := os.Stat(zp); err != nil {
		t.Fatal(err)
	}
}

func TestThemeFolderOfAndInnerPortal(t *testing.T) {
	exp := `<?xml version="1.0"?><request type="update"><portal action="locate">
<status element="all" result="ok"/>
<theme action="update" uniquename="custom.portal.theme.AEAT" objectid="Z_1" defaultskinref="Z_S1">
<allowed-skin skin="Z_S2" update="set"/>
<parameter name="com.ibm.portal.theme.template.ref" type="string" update="set"><![CDATA[dav:fs-type1:themes/AEAT/]]></parameter>
</theme></portal></request>`
	if f := ThemeFolderOf(exp); f != "AEAT" {
		t.Fatalf("folder %q", f)
	}
	in := innerPortal(exp)
	if strings.Contains(in, "<status") || !strings.Contains(in, `uniquename="custom.portal.theme.AEAT"`) {
		t.Fatalf("inner %q", in)
	}
	if f := ThemeFolderOf(`<parameter name="com.ibm.portal.theme.template.ref" type="string"><![CDATA[dav:fs-type1/themes/Tema-Jordi-DEMO/]]></parameter>`); f != "Tema-Jordi-DEMO" {
		t.Fatalf("folder (CF230 form) %q", f)
	}
	if ThemeFolderOf(`<parameter name="com.ibm.portal.theme.template.ref"><![CDATA[war:/x/]]></parameter>`) != "" {
		t.Fatal("a theme outside WebDAV has no folder")
	}
}

func TestContextRootEmptyOnVM(t *testing.T) {
	c, _ := NewClient(Config{BaseURL: "https://vm:10042", ContentHandlerPath: "/mycontenthandler"})
	if c.ContextRoot() != "" {
		t.Fatalf("root %q", c.ContextRoot())
	}
	c, _ = NewClient(Config{BaseURL: "https://dx"})
	if c.ContextRoot() != "/wps" {
		t.Fatalf("root %q", c.ContextRoot())
	}
}

func TestCleanThemeXML(t *testing.T) {
	in := `<request type="update"><portal action="locate">
<skin action="update" objectid="ZK_1" uniquename="custom.skin"><localedata locale="en"><title>S</title></localedata><localedata locale=""><title>S</title></localedata></skin>
<skin action="locate" domain="rel" objectid="ZK_1" uniquename="custom.skin"/>
<theme action="update" objectid="ZJ_1"><allowed-skin skin="ZK_1" update="set"/></theme>
</portal></request>`
	out := CleanThemeXML(in)
	if strings.Contains(out, `locale=""`) || strings.Contains(out, `action="locate" domain`) || !strings.Contains(out, `locale="en"`) || !strings.Contains(out, `<theme action="update"`) {
		t.Fatalf("clean: %s", out)
	}
	// without the skins in the request, the locate stays (the theme refers to a skin that must exist)
	only := `<portal action="locate"><skin action="locate" objectid="ZK_1"/><theme action="update"/></portal>`
	if CleanThemeXML(only) != only {
		t.Fatal("locate removed without the skin update")
	}
}

func TestInlineClonedPortlets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `uid="w.webmod"`) || !strings.Contains(string(b), `objectid="Z3_C"`) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0"?><request type="update"><portal action="locate"><status element="all" result="ok"/><web-app action="locate" uid="w.webmod"><portlet-app action="locate" uid="w"><portlet action="update" name="V.$cloned.Z3_C" objectid="Z3_C"><localedata locale=""><title>x</title></localedata><localedata locale="en"><title>SEO</title></localedata></portlet></portlet-app></web-app></portal></request>`))
	}))
	defer srv.Close()
	c, _ := NewClient(Config{BaseURL: srv.URL, Username: "u", Password: "p"})
	req := `<request><portal action="locate"><web-app action="locate" uid="w.webmod"><portlet-app action="locate" uid="w"><portlet action="locate" domain="rel" name="V" objectid="Z3_A"/><portlet action="locate" domain="rel" name="V.$cloned.Z3_C" objectid="Z3_C"/></portlet-app></web-app></portal></request>`
	out, names, err := c.InlineClonedPortlets(context.Background(), "", req)
	if err != nil || len(names) != 1 || !strings.Contains(out, `<portlet action="update" name="V.$cloned.Z3_C"`) || strings.Contains(out, `locale=""`) || !strings.Contains(out, `objectid="Z3_A"/>`) {
		t.Fatalf("%v %v %s", names, err, out)
	}
}
