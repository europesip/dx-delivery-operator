// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

// DXConnect of DX Core (the traditional DX 9.5 on WebSphere Application Server).
//
// DX Core has no dxFileTransfer (it answers 404 on CF239) and no WCM data module export to a folder the operator can
// download: the WCM libraries go through DXConnect, the servlet of the Config Wizard profile (cw_profile) that
// DXClient uses (/hcl/dxconnect/processHandler; port 10202 on a server installed on a VM, routed by HAProxy on 443 in
// the Kubernetes deployment of the HCL chart). The calls are the ones of DXClient 239 (wcm-library-export and
// wcm-library-import):
//
//   - export: GET  ?requestMethod=exportWCMLibrary&DXCoreProfileName=<p>&libraries='<a>';'<b>'[&virtualPortalContext=<vp>]
//     -> {"exportWCMLibraryPath": ".../<x>/<y>/<file>"}; then GET ?requestMethod=download&sourcepath=/<x>/<y>/<file>
//   - import: POST ?requestMethod=upload (multipart "file") -> {"filePath": "..."}; then POST ?requestMethod=importWCMLibrary
//     with {"DXCoreProfileName","filePath","virtualPortalContext"}
//
// Every call carries three basic credentials: Authorization (the DXConnect / Config Wizard administrator), DXAuth (the
// DX administrator) and DXWASAuth (the WebSphere administrator). DXConnect runs ConfigEngine export-wcm-data /
// import-wcm-data on the server, so the format is the JCR export of ConfigEngine: the same as the WCM data module.
package dx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DXConnectConfig: how to reach DXConnect of a DX Core. Empty URL: DXConnect is not used.
type DXConnectConfig struct {
	// URL of DXConnect, for example https://dx.example.com (Kubernetes, through HAProxy) or https://host:10202 (VM).
	URL string
	// Username and Password of the Config Wizard (cw_profile) administrator.
	Username, Password string
	// WASUsername and WASPassword of the WebSphere administrator.
	WASUsername, WASPassword string
	// ProfileName of the DX profile (default wp_profile).
	ProfileName string
}

const dxConnectPath = "/hcl/dxconnect/processHandler"

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// UsesDXConnect tells whether the WCM libraries of this DX go through DXConnect (DX Core) instead of the WCM data
// module and dxFileTransfer (DX Compose).
func (c *Client) UsesDXConnect() bool { return c.cfg.DXConnect.URL != "" }

func (c *Client) dxConnectDo(ctx context.Context, method, query string, body io.Reader, contentType string, timeout time.Duration) (int, []byte, error) {
	d := c.cfg.DXConnect
	u := strings.TrimRight(d.URL, "/") + dxConnectPath + "?" + query
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", basicHeader(d.Username, d.Password))
	req.Header.Set("DXAuth", basicHeader(c.cfg.Username, c.cfg.Password))
	req.Header.Set("DXWASAuth", basicHeader(d.WASUsername, d.WASPassword))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	hc := *c.http
	hc.Jar = nil
	hc.Timeout = timeout
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("DXConnect is not reachable: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

// errNoDXConnect: what answered is not DXConnect (an HTML page of the portal: on a traditional DX installed on a VM,
// DXConnect runs in the Config Wizard server of cw_profile, port 10202, which is often stopped).
func errNoDXConnect(url string) error {
	return fmt.Errorf("DXConnect did not answer at %s (an HTML page came back): start the Config Wizard server of cw_profile (port 10202 on a VM) and set spec.dxConnect.url, or export the libraries with ConfigEngine export-wcm-data on the server", url)
}

func isHTML(data []byte) bool {
	h := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 200)])))
	return strings.HasPrefix(h, "<!doctype html") || strings.HasPrefix(h, "<html")
}

func dxConnectMessage(data []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &m) == nil && m.Message != "" {
		return m.Message
	}
	return shortLog(string(data))
}

// DXConnectExportLibraries exports WCM libraries (by title) of the base portal (vp "") or a virtual portal through
// DXConnect and writes the archive DXConnect returns to zipPath. It returns its size.
func (c *Client) DXConnectExportLibraries(ctx context.Context, vp string, titles []string, zipPath string, timeout time.Duration) (int64, error) {
	d := c.cfg.DXConnect
	q := []string{"requestMethod=exportWCMLibrary", "DXCoreProfileName=" + url.QueryEscape(d.ProfileName)}
	quoted := make([]string, len(titles))
	for i, t := range titles {
		quoted[i] = "'" + t + "'"
	}
	q = append(q, "libraries="+url.QueryEscape(strings.Join(quoted, ";")))
	if vp != "" {
		q = append(q, "virtualPortalContext="+url.QueryEscape(vp))
	}
	st, data, err := c.dxConnectDo(ctx, http.MethodGet, strings.Join(q, "&"), nil, "application/json", timeout)
	if err != nil {
		return 0, err
	}
	if isHTML(data) {
		return 0, errNoDXConnect(d.URL)
	}
	if st != http.StatusOK {
		return 0, fmt.Errorf("DXConnect exportWCMLibrary answered HTTP %d: %s", st, dxConnectMessage(data))
	}
	var r struct {
		Path string `json:"exportWCMLibraryPath"`
	}
	if err := json.Unmarshal(data, &r); err != nil || r.Path == "" {
		return 0, fmt.Errorf("DXConnect exportWCMLibrary answered without a path: %s", shortLog(string(data)))
	}
	src := dxConnectSourcePath(r.Path)
	st, data, err = c.dxConnectDo(ctx, http.MethodGet, "requestMethod=download&sourcepath="+src, nil, "", timeout)
	if err != nil {
		return 0, err
	}
	if st != http.StatusOK {
		return 0, fmt.Errorf("DXConnect download of %s answered HTTP %d: %s", src, st, dxConnectMessage(data))
	}
	if err := os.WriteFile(zipPath, data, 0o600); err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

// dxConnectSourcePath: DXClient downloads the last three segments of the export path (relative to the DXConnect
// store): /<a>/<b>/<file>.
func dxConnectSourcePath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	return "/" + strings.Join(parts, "/")
}

// DXConnectImportLibraries uploads an export archive (the JCR export of export-wcm-data, zipped) and imports it into
// the base portal (vp "") or a virtual portal through DXConnect.
func (c *Client) DXConnectImportLibraries(ctx context.Context, vp, zipPath string, timeout time.Duration) (string, error) {
	f, err := os.Open(zipPath)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(zipPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	st, data, err := c.dxConnectDo(ctx, http.MethodPost, "requestMethod=upload", &buf, mw.FormDataContentType(), timeout)
	if err != nil {
		return "", err
	}
	if isHTML(data) {
		return "", errNoDXConnect(c.cfg.DXConnect.URL)
	}
	if st != http.StatusOK {
		return "", fmt.Errorf("DXConnect upload answered HTTP %d: %s", st, dxConnectMessage(data))
	}
	var up struct {
		FilePath string `json:"filePath"`
	}
	if err := json.Unmarshal(data, &up); err != nil || up.FilePath == "" {
		return "", fmt.Errorf("DXConnect upload answered without a file path: %s", shortLog(string(data)))
	}
	body, _ := json.Marshal(map[string]string{"DXCoreProfileName": c.cfg.DXConnect.ProfileName,
		"filePath": strings.ReplaceAll(up.FilePath, `\`, "/"), "virtualPortalContext": vp})
	st, data, err = c.dxConnectDo(ctx, http.MethodPost, "requestMethod=importWCMLibrary", bytes.NewReader(body), "application/json", timeout)
	if err != nil {
		return "", err
	}
	if st != http.StatusOK {
		return "", fmt.Errorf("DXConnect importWCMLibrary answered HTTP %d: %s", st, dxConnectMessage(data))
	}
	return dxConnectMessage(data), nil
}
