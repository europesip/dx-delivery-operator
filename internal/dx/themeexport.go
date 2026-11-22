package dx

import (
	"archive/tar"
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ThemeExport is a theme exported for another portal: its registration with its skins (XMLAccess, an update request)
// and its WebDAV folder (the static files).
type ThemeExport struct {
	UniqueName string
	// Folder of the theme in the WebDAV store fs-type1/themes (empty: the theme has no static files there, for example
	// a theme of an EAR).
	Folder string
	// XML: the XMLAccess request that registers the skins and the theme in the target (type="update").
	XML string
	// Skins: unique names (or object IDs) of the skins of the theme.
	Skins []string
}

var (
	reAllowedSkin = regexp.MustCompile(`<allowed-skin [^>]*skin="([^"]+)"`)
	reDefaultSkin = regexp.MustCompile(`<theme [^>]*defaultskinref="([^"]+)"`)
	reSkinUnique  = regexp.MustCompile(`<skin [^>]*uniquename="([^"]+)"`)
	reSkinBlock   = regexp.MustCompile(`(?s)\s*<skin [^>]*>.*?</skin>`)
	reTemplateRef = regexp.MustCompile(`(?s)<parameter name="com\.ibm\.portal\.theme\.template\.ref"[^>]*>(?:<!\[CDATA\[)?(.*?)(?:\]\]>)?</parameter>`)
)

// ExportTheme exports a theme by unique name from the base portal (vp "") or a virtual portal: one XMLAccess request
// for the theme, one for its skins (those it allows and its default skin), and the WebDAV folder named in
// com.ibm.portal.theme.template.ref. ok=false: the theme does not exist.
func (c *Client) ExportTheme(ctx context.Context, vp, uniqueName string) (ThemeExport, bool, error) {
	out, err := c.XMLAccessAt(ctx, vp, ThemeExportXML(uniqueName))
	if err != nil {
		return ThemeExport{}, false, err
	}
	e, err := ParseThemeExport(out, uniqueName)
	if err != nil {
		return ThemeExport{}, false, err
	}
	if !e.Exists {
		return ThemeExport{}, false, nil
	}
	t := ThemeExport{UniqueName: uniqueName, Folder: ThemeFolderOf(out)}
	ids := map[string]bool{}
	var order []string
	for _, m := range append(reDefaultSkin.FindAllStringSubmatch(out, -1), reAllowedSkin.FindAllStringSubmatch(out, -1)...) {
		if !ids[m[1]] {
			ids[m[1]] = true
			order = append(order, m[1])
		}
	}
	var skinsXML string
	if len(order) > 0 {
		var b strings.Builder
		for _, id := range order {
			fmt.Fprintf(&b, "    <skin action=\"export\" objectid=\"%s\"/>\n", esc(id))
		}
		sk, err := c.XMLAccessAt(ctx, vp, fmt.Sprintf(xmlHead, "export", "")+b.String()+xmlTail)
		if err != nil {
			return ThemeExport{}, false, fmt.Errorf("XMLAccess export of the skins of %s: %w", uniqueName, err)
		}
		if ok, msg := XMLAccessOK(sk); !ok {
			return ThemeExport{}, false, fmt.Errorf("XMLAccess export of the skins of %s: %s", uniqueName, msg)
		}
		// the skins of HCL (ibm.*, hcl.*, wp.*) are in every DX: only the theme's own skins go into the bundle (an
		// update of a built-in skin by object ID would fail, or change it, in the target)
		kept := reSkinBlock.ReplaceAllStringFunc(innerPortal(sk), func(b string) string {
			m := reSkinUnique.FindStringSubmatch(b)
			if m == nil || builtIn(m[1]) {
				return ""
			}
			t.Skins = append(t.Skins, m[1])
			return b
		})
		skinsXML = kept
	}
	// one update request: the skins first (the theme refers to them), then the theme
	t.XML = CleanThemeXML(fmt.Sprintf(xmlHead, "update", "") + skinsXML + innerPortal(out) + xmlTail)
	return t, true, nil
}

// builtIn: a theme or skin of HCL, present in every DX.
func builtIn(uniqueName string) bool {
	for _, p := range []string{"ibm.", "hcl.", "wp."} {
		if strings.HasPrefix(uniqueName, p) {
			return true
		}
	}
	return false
}

var (
	reEmptyLocale = regexp.MustCompile(`(?s)\s*<localedata locale="">.*?</localedata>`)
	reSkinLocate  = regexp.MustCompile(`\s*<skin action="locate"[^>]*/>`)
)

// CleanThemeXML makes a theme export of DX 9.5 importable (measured on CF230 → CF239, Core and Compose):
//   - localedata with an empty locale (locale="", written by DX 9.5 for some skins) make the import fail with
//     EJPXA0001E NullPointerException "key is null": removed;
//   - the theme export repeats its skins as <skin action="locate">; with the skins themselves in the same request that
//     is EJPXA0140E "Duplicate objectid": removed when the request also updates the skin.
func CleanThemeXML(req string) string {
	req = reEmptyLocale.ReplaceAllString(req, "")
	if strings.Contains(req, `<skin action="update"`) {
		req = reSkinLocate.ReplaceAllString(req, "")
	}
	return req
}

var reInnerPortal = regexp.MustCompile(`(?s)<portal action="locate">(.*)</portal>`)

// innerPortal: what is inside <portal action="locate"> of an XMLAccess answer, without its status elements.
func innerPortal(answer string) string {
	m := reInnerPortal.FindStringSubmatch(answer)
	if m == nil {
		return ""
	}
	return reStatus.ReplaceAllString(m[1], "") + "\n"
}

// ThemeFolderOf reads the WebDAV folder of a theme export: com.ibm.portal.theme.template.ref = dav:fs-type1/themes/<folder>/
// (DX 9.5 writes it so; dav:fs-type1:themes/<folder>/ is accepted too).
func ThemeFolderOf(export string) string {
	m := reTemplateRef.FindStringSubmatch(export)
	if m == nil {
		return ""
	}
	ref := strings.TrimSpace(m[1])
	i := strings.Index(ref, "themes/")
	if !(strings.HasPrefix(ref, "dav:fs-type1:") || strings.HasPrefix(ref, "dav:fs-type1/")) || i < 0 {
		return ""
	}
	return strings.Trim(ref[i+len("themes/"):], "/")
}

// ZipDir zips a folder (paths relative to it, forward slashes) into out. It returns the number of files.
func ZipDir(dir, out string) (int, error) {
	f, err := os.Create(out)
	if err != nil {
		return 0, err
	}
	zw := zip.NewWriter(f)
	n := 0
	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, in)
		in.Close() //nolint:errcheck
		n++
		return err
	})
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// TarDir writes a plain (uncompressed) tar of a folder: its entries are relative to dir, with forward slashes. DXConnect
// of DX Core writes and reads its WCM exports in this form (named .tar.gz, but not compressed).
func TarDir(dir, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	err = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, in)
		in.Close() //nolint:errcheck
		return err
	})
	if cerr := tw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// MergeTree copies every file of src into dst, keeping the relative paths (folders that exist in both are merged).
func MergeTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		t := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(t, 0o700)
		}
		return os.Rename(p, t)
	})
}

var (
	reClonedLocate = regexp.MustCompile(`<portlet action="locate" [^>]*name="[^"]*\.\$cloned\.[^"]*"[^>]*objectid="([^"]+)"[^>]*/>`)
	reWebAppUID    = regexp.MustCompile(`<web-app action="locate" [^>]*uid="([^"]+)"`)
	rePortletAppUI = regexp.MustCompile(`<portlet-app action="locate" [^>]*uid="([^"]+)"`)
	rePortletBlock = regexp.MustCompile(`(?s)<portlet action="update" .*?</portlet>`)
)

// InlineClonedPortlets puts the definitions of cloned portlets (name "<portlet>.$cloned.<id>", for example a Web Content
// Viewer copied with its own settings) into a pages request: XMLAccess exports pages with only a locate of each portlet,
// and a clone does not exist in another portal, so the import fails with EJPXA0240E (measured CF230 → CF239). Each clone
// is exported from the source (web-app / portlet-app / portlet) and its <portlet action="update"> replaces the locate.
// It returns the request and the names of the clones.
func (c *Client) InlineClonedPortlets(ctx context.Context, vp, req string) (string, []string, error) {
	var names []string
	for {
		loc := reClonedLocate.FindStringSubmatchIndex(req)
		if loc == nil {
			return req, names, nil
		}
		before := req[:loc[0]]
		wa := reWebAppUID.FindAllStringSubmatch(before, -1)
		pa := rePortletAppUI.FindAllStringSubmatch(before, -1)
		oid := req[loc[2]:loc[3]]
		if len(wa) == 0 || len(pa) == 0 {
			return req, names, fmt.Errorf("cloned portlet %s: no web-app / portlet-app around it in the pages export", oid)
		}
		x := fmt.Sprintf(xmlHead, "export", "") + fmt.Sprintf("    <web-app action=\"locate\" uid=\"%s\"><portlet-app action=\"locate\" uid=\"%s\"><portlet action=\"export\" objectid=\"%s\"/></portlet-app></web-app>\n",
			esc(wa[len(wa)-1][1]), esc(pa[len(pa)-1][1]), esc(oid)) + xmlTail
		out, err := c.XMLAccessAt(ctx, vp, x)
		if err != nil {
			return req, names, fmt.Errorf("export of the cloned portlet %s: %w", oid, err)
		}
		if ok, msg := XMLAccessOK(out); !ok {
			return req, names, fmt.Errorf("export of the cloned portlet %s: %s", oid, msg)
		}
		def := rePortletBlock.FindString(out)
		if def == "" {
			return req, names, fmt.Errorf("export of the cloned portlet %s: no <portlet> in the answer", oid)
		}
		if m := regexp.MustCompile(`name="([^"]+)"`).FindStringSubmatch(req[loc[0]:loc[1]]); m != nil {
			names = append(names, m[1])
		}
		req = req[:loc[0]] + reEmptyLocale.ReplaceAllString(def, "") + req[loc[1]:]
	}
}
