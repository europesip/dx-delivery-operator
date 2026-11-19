package dx

// WCM library import on DX Compose (checked on CF239, 2026-11-17):
//   - files reach the server through dxFileTransfer (/wps/dxFileTransfer/dft, basic authentication): POST
//     action=createDir|upload (unzip=true extracts the zip into <subDirectory>/<zip name without .zip>), DELETE
//     action=deleteSubDir. At most 500 MB per upload; zip entries with a space are rejected.
//   - the import is the WCM data module, started from a signed-in session:
//     GET /wps/wcm/myconnect[/<vp context>]?MOD=data&processLibraries=false&taskType=import|import-copy&input.dir=...
//     It answers an HTML log; "was imported successfully" on success, "Failed to complete" otherwise.
//   - libraries of a portal: GET /wps/mycontenthandler[/<vp context>/!ut/p]/wcmrest/query?type=Library (Atom).
//   - virtual portals: XMLAccess with the PortalVPHandler.xsd schema (<virtual-portal action="export"/> lists them,
//     action="import" creates one). action="delete" answers ok but does nothing on CF239.

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// FileTransferRoot is the folder of the DX container where dxFileTransfer writes.
const FileTransferRoot = "/opt/openliberty/wlp/usr/servers/defaultServer/dxFileTransfers"

// MaxUploadBytes is the upload limit of dxFileTransfer (it answers "Uploaded file size exceeds the maximum allowed
// size of 500 MB").
const MaxUploadBytes = 500 * 1000 * 1000

func (c *Client) wpsRoot() string {
	return strings.TrimSuffix(c.cfg.ContentHandlerPath, "/mycontenthandler")
}

// basicDo sends a request with basic authentication and no session cookie (like XMLAccess), following redirects.
// timeout 0 = only the context limits it.
func (c *Client) basicDo(ctx context.Context, method, path string, body io.Reader, contentType string, timeout time.Duration) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.SetBasicAuth(c.cfg.Username, c.cfg.Password)
	if strings.Contains(path, "/wcmrest/") || strings.Contains(path, "/ac/") {
		// without an Accept header WCM REST answers JSON; the operator reads Atom
		req.Header.Set("Accept", "application/atom+xml")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	hc := *c.http
	hc.Jar = nil
	hc.Timeout = timeout
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return resp.StatusCode, data, nil
}

// ---------------------------------------------------------------------------------------------- dxFileTransfer

type dftAnswer struct {
	Message string `json:"message"`
	Results []struct {
		AbsolutePath string `json:"absolutePath"`
		Status       bool   `json:"status"`
		Message      string `json:"message"`
	} `json:"results"`
}

func parseDFT(status int, data []byte) (*dftAnswer, error) {
	var a dftAnswer
	if err := (&Response{Status: status, Body: data}).json(&a); err != nil {
		return nil, fmt.Errorf("dxFileTransfer: %w", err)
	}
	for _, r := range a.Results {
		if !r.Status {
			return &a, fmt.Errorf("dxFileTransfer: %s", r.Message)
		}
	}
	if status >= 300 {
		return &a, fmt.Errorf("dxFileTransfer answered HTTP %d: %s", status, a.Message)
	}
	return &a, nil
}

func dftPath(c *Client, q url.Values) string {
	return c.wpsRoot() + "/dxFileTransfer/dft?" + q.Encode()
}

// FileTransferCreateDir creates a sub folder of the dxFileTransfer root (no error if it exists).
func (c *Client) FileTransferCreateDir(ctx context.Context, sub string) error {
	if c.long != nil {
		if err := c.long.FileTransferCreateDir(ctx, sub); !unreachable(err) { // in-cluster URL: no proxy timeout
			return err
		}
	}
	st, data, err := c.basicDo(ctx, http.MethodPost, dftPath(c, url.Values{"action": {"createDir"}, "subDirectory": {sub}}), nil, "", 2*time.Minute)
	if err != nil {
		return err
	}
	_, err = parseDFT(st, data)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "exist") {
		return nil
	}
	return err
}

// FileTransferUpload uploads a zip into sub and extracts it there; it returns the absolute folder in the DX container
// where the files are (<root>/<sub>/<zip name without .zip>). The file is streamed, not read into memory.
func (c *Client) FileTransferUpload(ctx context.Context, sub, zipPath string) (string, error) {
	if c.long != nil {
		if v, err := c.long.FileTransferUpload(ctx, sub, zipPath); !unreachable(err) { // in-cluster URL: no proxy timeout
			return v, err
		}
	}
	fi, err := os.Stat(zipPath)
	if err != nil {
		return "", err
	}
	if fi.Size() > MaxUploadBytes {
		return "", fmt.Errorf("%s is %d bytes: dxFileTransfer accepts at most %d", filepath.Base(zipPath), fi.Size(), MaxUploadBytes)
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("file", filepath.Base(zipPath))
		if err == nil {
			var f *os.File
			if f, err = os.Open(zipPath); err == nil {
				_, err = io.Copy(part, f)
				_ = f.Close()
			}
		}
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	q := url.Values{"action": {"upload"}, "unzip": {"true"}, "deleteZip": {"true"}, "subDirectory": {sub}}
	st, data, err := c.basicDo(ctx, http.MethodPost, dftPath(c, q), pr, mw.FormDataContentType(), 0)
	if err != nil {
		return "", fmt.Errorf("upload of %s: %w", filepath.Base(zipPath), err)
	}
	if _, err := parseDFT(st, data); err != nil {
		return "", err
	}
	return FileTransferRoot + "/" + sub + "/" + strings.TrimSuffix(filepath.Base(zipPath), ".zip"), nil
}

// FileTransferDeleteDir deletes a sub folder of the dxFileTransfer root and everything in it.
func (c *Client) FileTransferDeleteDir(ctx context.Context, sub string) error {
	if c.long != nil {
		if err := c.long.FileTransferDeleteDir(ctx, sub); !unreachable(err) { // in-cluster URL: no proxy timeout
			return err
		}
	}
	st, data, err := c.basicDo(ctx, http.MethodDelete, dftPath(c, url.Values{"action": {"deleteSubDir"}, "subDirectory": {sub}}), nil, "", 5*time.Minute)
	if err != nil {
		return err
	}
	_, err = parseDFT(st, data)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not exist") {
		return nil
	}
	return err
}

// ---------------------------------------------------------------------------------------------- libraries

// WCMLibrary is a library of a portal.
type WCMLibrary struct {
	ID            string
	Title         string
	AccessControl string // path of its access-control entry (ac/access:oid:...)
	// Updated: last modification of the library as WCM REST reports it (zero when it does not).
	Updated time.Time
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type libFeed struct {
	Total   int `xml:"total"`
	Entries []struct {
		ID      string     `xml:"id"`
		Title   string     `xml:"title"`
		Updated string     `xml:"updated"`
		Links   []atomLink `xml:"link"`
	} `xml:"entry"`
}

// lenientXML decodes the Atom of WCM REST, which is not always well formed: the query feeds of CF239 carry a raw "&"
// (`<id>wcmrest:query?page=1<![CDATA[&pagesize=200&type=Library]]></id>` is fine, but links of other entries are not).
func lenientXML(data []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	return d.Decode(v)
}

// handlerPath is the content handler path of the base portal or of a virtual portal.
func (c *Client) handlerPath(vp string) string {
	if vp == "" {
		return c.cfg.ContentHandlerPath
	}
	return c.cfg.ContentHandlerPath + "/" + url.PathEscape(vp) + "/!ut/p"
}

// ListLibraries lists the WCM libraries of the base portal (vp "") or of a virtual portal.
func (c *Client) ListLibraries(ctx context.Context, vp string) ([]WCMLibrary, error) {
	var out []WCMLibrary
	for page := 1; page <= 50; page++ {
		p := fmt.Sprintf("%s/wcmrest/query?type=Library&pagesize=200&page=%d", c.handlerPath(vp), page)
		st, data, err := c.basicDo(ctx, http.MethodGet, p, nil, "", 2*time.Minute)
		if err != nil {
			return nil, err
		}
		if st == http.StatusNotFound {
			return nil, ErrNotFound
		}
		if st != http.StatusOK {
			return nil, fmt.Errorf("WCM REST query of libraries answered HTTP %d: %.200s", st, data)
		}
		var f libFeed
		if err := lenientXML(data, &f); err != nil {
			return nil, fmt.Errorf("WCM REST query of libraries: %w", err)
		}
		for _, e := range f.Entries {
			l := WCMLibrary{ID: strings.TrimPrefix(e.ID, "wcmrest:"), Title: strings.TrimSpace(e.Title)}
			if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(e.Updated)); perr == nil {
				l.Updated = t
			}
			for _, k := range e.Links {
				if k.Rel == "access-control" {
					l.AccessControl = k.Href
				}
			}
			out = append(out, l)
		}
		if len(f.Entries) < 200 || (f.Total > 0 && len(out) >= f.Total) {
			break
		}
	}
	return out, nil
}

// LibraryAccessControl reads the access-control link of a library (ListLibraries does not always carry it).
func (c *Client) LibraryAccessControl(ctx context.Context, vp, id string) (string, error) {
	st, data, err := c.basicDo(ctx, http.MethodGet, c.handlerPath(vp)+"/wcmrest/Library/"+url.PathEscape(id), nil, "", 2*time.Minute)
	if err != nil {
		return "", err
	}
	if st != http.StatusOK {
		return "", fmt.Errorf("library %s answered HTTP %d", id, st)
	}
	var e struct {
		Links []atomLink `xml:"link"`
	}
	if err := lenientXML(data, &e); err != nil {
		return "", err
	}
	for _, k := range e.Links {
		if k.Rel == "access-control" {
			return k.Href, nil
		}
	}
	return "", fmt.Errorf("library %s has no access-control link", id)
}

var acOID = regexp.MustCompile(`ac/access:(oid:[A-Za-z0-9_-]+)`)

// GrantLibraryRole adds a user or group (by DN) to a role of a library (WCM access control REST, as wpsadmin).
func (c *Client) GrantLibraryRole(ctx context.Context, vp, accessControlHref, dn, principalType, role string) error {
	m := acOID.FindStringSubmatch(accessControlHref)
	if m == nil {
		return fmt.Errorf("unexpected access-control link %q", accessControlHref)
	}
	if principalType == "" {
		principalType = "group"
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><ac:member xmlns:ac="http://www.ibm.com/xmlns/prod/lotus/access-control/v1.0" ac:DN=%q ac:type=%q/>`,
		html.EscapeString(dn), principalType)
	p := c.handlerPath(vp) + "/ac/member:" + url.PathEscape(role) + "@" + m[1]
	st, data, err := c.basicDo(ctx, http.MethodPost, p, strings.NewReader(body), "application/atom+xml", 2*time.Minute)
	if err != nil {
		return err
	}
	if st != http.StatusCreated && st != http.StatusOK && st != http.StatusNoContent {
		return fmt.Errorf("role %s for %s answered HTTP %d: %.200s", role, dn, st, data)
	}
	return nil
}

// ---------------------------------------------------------------------------------------------- virtual portals

// VirtualPortal is a virtual portal of DX.
type VirtualPortal struct {
	Name        string `xml:"name"`
	OID         string `xml:"oid"`
	Realm       string `xml:"realm"`
	Description string `xml:"description"`
	Context     string `xml:"context"`
	Hostname    string `xml:"hostname"`
}

const vpRequest = `<?xml version="1.0" encoding="UTF-8"?>
<request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="PortalVPHandler.xsd">
%s
</request>`

// VirtualPortals lists the virtual portals (XMLAccess, PortalVPHandler).
func (c *Client) VirtualPortals(ctx context.Context) ([]VirtualPortal, error) {
	out, err := c.XMLAccess(ctx, fmt.Sprintf(vpRequest, `<virtual-portal action="export"/>`))
	if err != nil {
		return nil, err
	}
	return parseVirtualPortals(out)
}

func parseVirtualPortals(out string) ([]VirtualPortal, error) {
	var r struct {
		VPs    []VirtualPortal `xml:"virtual-portal"`
		Status []struct {
			Result string `xml:"result,attr"`
		} `xml:"status"`
	}
	if err := xml.Unmarshal([]byte(out), &r); err != nil {
		return nil, fmt.Errorf("XMLAccess virtual portals: %w", err)
	}
	for _, s := range r.Status {
		if s.Result != "ok" {
			_, msg := XMLAccessOK(out)
			return nil, fmt.Errorf("XMLAccess virtual portals: %s", msg)
		}
	}
	return r.VPs, nil
}

// CreateVirtualPortal creates a virtual portal with its default content (XMLAccess, PortalVPHandler import). It takes
// about a minute on DX Compose.
func (c *Client) CreateVirtualPortal(ctx context.Context, vp VirtualPortal) error {
	e := func(s string) string { return html.EscapeString(s) }
	body := fmt.Sprintf(`<virtual-portal action="import"><name>%s</name><realm>%s</realm><description>%s</description><context>%s</context><hostname/></virtual-portal>`,
		e(vp.Name), e(vp.Realm), e(vp.Description), e(vp.Context))
	out, err := c.XMLAccess(ctx, fmt.Sprintf(vpRequest, body))
	if err != nil {
		return err
	}
	if ok, msg := XMLAccessOK(out); !ok {
		return fmt.Errorf("XMLAccess create virtual portal %s: %s", vp.Context, msg)
	}
	return nil
}

// ---------------------------------------------------------------------------------------------- data module

var tagRe = regexp.MustCompile(`<[^>]*>`)

// ErrCutByProxy: the import request was cut by a proxy in front of DX (HTTP 502/503/504) or timed out. DX may still be
// importing: the uploaded files must not be deleted yet.
var ErrCutByProxy = errors.New("the import request was cut before DX answered; DX may still be importing")

// DataModuleImport imports the export folder dir (absolute, in the DX container) with the WCM data module, into the
// base portal (vp "") or a virtual portal. copy=true uses taskType=import-copy (new IDs). It returns the log of the
// module as text. It holds the XMLAccess lock of this DX while it runs: concurrent XMLAccess calls made the import fail
// on Derby (transaction timeout).
func (c *Client) DataModuleImport(ctx context.Context, vp, dir string, copyIDs bool, timeout time.Duration) (string, error) {
	if c.long != nil {
		if v, err := c.long.DataModuleImport(ctx, vp, dir, copyIDs, timeout); !unreachable(err) { // in-cluster URL: no proxy timeout
			return v, err
		}
	}
	mu, _ := xmlAccessLocks.LoadOrStore(c.lockID(), &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	task := "import"
	if copyIDs {
		task = "import-copy"
	}
	p := c.wpsRoot() + "/wcm/myconnect"
	if vp != "" {
		p += "/" + url.PathEscape(vp)
	}
	q := url.Values{"MOD": {"data"}, "processLibraries": {"false"}, "taskType": {task}, "input.dir": {dir}}
	full := p + "?" + q.Encode()
	for attempt := 0; attempt < 2; attempt++ {
		c.mu.Lock()
		err := c.loginLocked(ctx, attempt > 0)
		c.mu.Unlock()
		if err != nil {
			return "", err
		}
		rctx, cancel := context.WithTimeout(ctx, timeout)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, c.cfg.BaseURL+full, nil)
		hc := *c.http
		hc.Timeout = 0
		resp, err := hc.Do(req)
		if err != nil {
			cancel()
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "EOF") {
				return "", fmt.Errorf("%w (%v)", ErrCutByProxy, err)
			}
			return "", fmt.Errorf("WCM data module import: %w", err)
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		cancel()
		text := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(string(data), " "))), " ")
		switch {
		case strings.Contains(text, "was imported successfully"):
			return text, nil
		case strings.Contains(text, "Failed to complete") || strings.Contains(text, "RepositoryException"):
			return text, fmt.Errorf("DX import failed: %s", shortLog(text))
		case resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout:
			return text, fmt.Errorf("%w (HTTP %d)", ErrCutByProxy, resp.StatusCode)
		case attempt == 0 && (resp.StatusCode == http.StatusUnauthorized || strings.Contains(strings.ToLower(text), "log in")):
			continue // the session expired: sign in again once
		default:
			return text, fmt.Errorf("unexpected answer of the WCM data module (HTTP %d): %s", resp.StatusCode, shortLog(text))
		}
	}
	return "", errors.New("DX session could not be renewed")
}

func shortLog(s string) string {
	if i := strings.Index(s, "Failed to complete"); i >= 0 {
		s = s[i:]
	}
	if len(s) > 600 {
		s = s[:600] + "..."
	}
	return s
}
