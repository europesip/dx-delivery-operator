package dx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDX answers like DX Compose CF239 for the calls of the WCM library import.
func fakeDX(t *testing.T, importAnswer string) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		user, pass, basic := r.BasicAuth()
		switch {
		case r.URL.Path == "/dx/api/core/v1/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "LtpaToken2", Value: "t", Path: "/"})
		case r.URL.Path == "/wps/dxFileTransfer/dft":
			if !basic || user != "wpsadmin" || pass != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			switch r.URL.Query().Get("action") {
			case "createDir", "deleteSubDir":
				_, _ = w.Write([]byte(`{"message":"ok","results":[{"absolutePath":"/x","status":true,"message":"ok"}]}`))
			case "upload":
				f, h, err := r.FormFile("file")
				if err != nil {
					t.Errorf("upload without a file: %v", err)
					return
				}
				b, _ := io.ReadAll(f)
				if h.Filename != "chunk-01.zip" || string(b) != "PK-zip" {
					t.Errorf("uploaded %s %q", h.Filename, b)
				}
				_, _ = w.Write([]byte(`{"message":"Files uploaded","results":[{"absolutePath":"/x/chunk-01","status":true,"message":"Unzipped successfully: chunk-01.zip"}]}`))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		case strings.HasPrefix(r.URL.Path, "/wps/wcm/myconnect"):
			if _, err := r.Cookie("LtpaToken2"); err != nil {
				t.Error("the data module needs the session cookie")
			}
			_, _ = w.Write([]byte(importAnswer))
		case r.URL.Path == "/wps/mycontenthandler/europesip/!ut/p/wcmrest/query":
			_, _ = w.Write([]byte(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><id>wcmrest:query?page=1&pagesize=200&type=Library</id><total>2</total>
<entry><id>wcmrest:aaa</id><title>Portal Site</title><link rel="access-control" href="/wps/mycontenthandler/europesip/!ut/p/digest!x/ac/access:oid:Z6QAAA"/></entry>
<entry><id>wcmrest:bbb</id><title>EuropeSip_ES</title></entry></feed>`))
		case r.URL.Path == "/wps/mycontenthandler/nope/!ut/p/wcmrest/query":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/wps/mycontenthandler/europesip/!ut/p/ac/member:Editor@oid:Z6QAAA":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `ac:DN="cn=dxadmins,ou=groups,dc=example,dc=com"`) || !strings.Contains(string(b), `ac:type="group"`) {
				t.Errorf("member body %s", b)
			}
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/wps/config":
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), `action="export"`) {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><request><virtual-portal action="import"><name>EuropeSip</name><oid>Z18_1</oid><realm>r1</realm><description/><context>europesip</context><hostname/></virtual-portal><status element="all" result="ok"/></request>`))
				return
			}
			if !strings.Contains(string(b), "<context>newvp</context>") || !strings.Contains(string(b), "<realm>r1</realm>") {
				t.Errorf("create request %s", b)
			}
			_, _ = w.Write([]byte(`<?xml version="1.0"?><request><status element="all" result="ok"/></request>`))
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestFileTransferAndImport(t *testing.T) {
	ok := `<html><body>IBM Web Content Manager Utility: DataModule<br/>The directory "/x/chunk-01" was imported successfully.<br/>Utility completed.</body></html>`
	srv, calls := fakeDX(t, ok)
	c, _ := NewClient(Config{BaseURL: srv.URL, Username: "wpsadmin", Password: "secret"})
	ctx := context.Background()
	if err := c.FileTransferCreateDir(ctx, "dxop-ns-x"); err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(t.TempDir(), "chunk-01.zip")
	_ = os.WriteFile(zip, []byte("PK-zip"), 0o600)
	dir, err := c.FileTransferUpload(ctx, "dxop-ns-x", zip)
	if err != nil || dir != FileTransferRoot+"/dxop-ns-x/chunk-01" {
		t.Fatalf("upload: %q %v", dir, err)
	}
	if _, err := c.DataModuleImport(ctx, "europesip", dir, false, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.FileTransferDeleteDir(ctx, "dxop-ns-x"); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(*calls, "\n")
	for _, want := range []string{
		"GET /wps/wcm/myconnect/europesip?MOD=data&input.dir=%2Fopt%2Fopenliberty%2Fwlp%2Fusr%2Fservers%2FdefaultServer%2FdxFileTransfers%2Fdxop-ns-x%2Fchunk-01&processLibraries=false&taskType=import",
		"DELETE /wps/dxFileTransfer/dft?action=deleteSubDir&subDirectory=dxop-ns-x",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing call %s in\n%s", want, all)
		}
	}
}

func TestDataModuleImportFailure(t *testing.T) {
	srv, _ := fakeDX(t, `<html>Starting utility: DataModule<br/>Failed to complete: com.aptrix.pluto.util.RepositoryException: SER3009E: Import of one or more files failed.</html>`)
	c, _ := NewClient(Config{BaseURL: srv.URL, Username: "wpsadmin", Password: "secret"})
	_, err := c.DataModuleImport(context.Background(), "", "/x", true, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "SER3009E") {
		t.Fatalf("want the DX error, got %v", err)
	}
}

func TestUploadLimit(t *testing.T) {
	c, _ := NewClient(Config{BaseURL: "http://127.0.0.1:1", Username: "u", Password: "p"})
	big := filepath.Join(t.TempDir(), "big.zip")
	f, _ := os.Create(big)
	_ = f.Truncate(MaxUploadBytes + 1)
	_ = f.Close()
	if _, err := c.FileTransferUpload(context.Background(), "s", big); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("want the size error, got %v", err)
	}
}

func TestLibrariesAccessAndVirtualPortals(t *testing.T) {
	srv, _ := fakeDX(t, "")
	c, _ := NewClient(Config{BaseURL: srv.URL, Username: "wpsadmin", Password: "secret"})
	ctx := context.Background()
	libs, err := c.ListLibraries(ctx, "europesip")
	if err != nil || len(libs) != 2 || libs[0].Title != "Portal Site" || libs[0].ID != "aaa" {
		t.Fatalf("libraries: %+v %v", libs, err)
	}
	if err := c.GrantLibraryRole(ctx, "europesip", libs[0].AccessControl, "cn=dxadmins,ou=groups,dc=example,dc=com", "", "Editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListLibraries(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("unknown virtual portal: %v", err)
	}
	vps, err := c.VirtualPortals(ctx)
	if err != nil || len(vps) != 1 || vps[0].Context != "europesip" || vps[0].Realm != "r1" {
		t.Fatalf("virtual portals: %+v %v", vps, err)
	}
	if err := c.CreateVirtualPortal(ctx, VirtualPortal{Name: "New", Realm: "r1", Context: "newvp"}); err != nil {
		t.Fatal(err)
	}
}
