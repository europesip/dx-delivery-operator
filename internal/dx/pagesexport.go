// SPDX-License-Identifier: MIT
// Copyright (c) 2026 EUROPESIP

package dx

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// PageSelection is a page to export with XMLAccess (and, with Descendants, the pages under it).
type PageSelection struct {
	UniqueName  string
	Descendants bool
}

// PagesExportXML builds one XMLAccess export request for several pages: DX answers one update request with all of
// them, the themes, skins and portlets they reference located by unique name.
func PagesExportXML(pages []PageSelection) string {
	var b strings.Builder
	fmt.Fprintf(&b, xmlHead, "export", "")
	for _, p := range pages {
		fmt.Fprintf(&b, "    <content-node action=\"export\" uniquename=\"%s\" export-descendants=\"%t\"/>\n", esc(p.UniqueName), p.Descendants)
	}
	b.WriteString(xmlTail)
	return b.String()
}

// PagesInventory is what an exported pages request needs in the target portal, and what it holds.
type PagesInventory struct {
	// Nodes: content nodes updated by the request; PerPage: of them, under each requested page (itself included).
	Nodes   int
	PerPage map[string]int
	// Themes, Skins and Portlets located by the request: the target must have them (unique names, portlet names).
	Themes, Skins, Portlets []string
	// ManagedPages: content nodes with a WCM content mapping (their content is in the Portal Site library).
	ManagedPages int
	// UnnamedParents: parents located only by object ID (no unique name): they do not exist in another portal.
	UnnamedParents []string
}

var (
	reXMLComment   = regexp.MustCompile(`(?s)<!--.*?-->\s*`)
	reRequestOpen  = regexp.MustCompile(`<request ([^>]*)>`)
	reOwner        = regexp.MustCompile(`(<access-control [^>]*?) owner="[^"]*"`)
	reCreateOIDs   = regexp.MustCompile(` ?create-oids="[^"]*"`)
	reRequestTag   = regexp.MustCompile(`<request(\s[^>]*)?>`)
	reNodeUpdate   = regexp.MustCompile(`<content-node action="update"([^>]*)>`)
	reNodeLocate   = regexp.MustCompile(`<content-node action="locate"([^>]*?)/?>`)
	reAttr         = func(name string) *regexp.Regexp { return regexp.MustCompile(` ` + name + `="([^"]*)"`) }
	reObjectID     = reAttr("objectid")
	reUniqueName   = reAttr("uniquename")
	reParentRef    = reAttr("content-parentref")
	reLocateTheme  = regexp.MustCompile(`<theme action="locate"[^>]*? uniquename="([^"]*)"`)
	reLocateSkin   = regexp.MustCompile(`<skin action="locate"[^>]*? uniquename="([^"]*)"`)
	reLocatePortlt = regexp.MustCompile(`<portlet action="locate"[^>]*? name="([^"]*)"`)
	reMapping      = regexp.MustCompile(`<content-mapping-info>`)
)

// PagesImportXML turns DX's answer to PagesExportXML into a request that imports the pages into a DX (DX Compose
// through /wps/config, or a traditional DX with xmlaccess.sh): type="update", KEEPING the object IDs of the source
// exactly as DX exported them (0.9.1: the object IDs are chosen when importing, as WCM import / import-copy: sent as
// is they are kept, which staging → production and later syndication need; with create-oids="true" on the request
// DX gives new ones: see SetCreateOIDs and DXXMLAccess spec.objectIDs). It removes the export's status, build and
// version attributes, any create-oids attribute, comments (they name the pod and its IP) and the owners of access
// control (a user of the source registry need not exist in the target, and an unknown owner makes the import fail;
// without it the importing administrator owns the pages; the access-control roles are kept). It also reads what the
// request needs.
func PagesImportXML(export string, requested []string) (string, PagesInventory, error) {
	inv := PagesInventory{PerPage: map[string]int{}}
	if ok, msg := XMLAccessOK(export); !ok {
		if strings.Contains(export, "EJPXA0240E") {
			return "", inv, fmt.Errorf("%w: %s", ErrPageNotFound, msg)
		}
		return "", inv, fmt.Errorf("XMLAccess export of the pages failed: %s", msg)
	}
	i := strings.Index(export, "<request")
	if i < 0 {
		return "", inv, fmt.Errorf("the XMLAccess answer has no request")
	}
	req := reXMLComment.ReplaceAllString(export[i:], "")
	req = reStatus.ReplaceAllString(req, "")
	req = reRequestOpen.ReplaceAllStringFunc(req, func(m string) string {
		a := reRequestOpen.FindStringSubmatch(m)[1]
		a = reBuild.ReplaceAllString(" "+a, "")
		a = strings.TrimSpace(regexp.MustCompile(` type="[^"]*"`).ReplaceAllString(a, ""))
		a = strings.TrimSpace(reCreateOIDs.ReplaceAllString(" "+a, ""))
		return `<request ` + a + ` type="update">`
	})
	req = reOwner.ReplaceAllString(req, "$1")

	// inventory: nodes and their parents, to count the nodes under each requested page
	parent := map[string]string{}
	idOf := map[string]string{}
	for _, m := range reNodeUpdate.FindAllStringSubmatch(req, -1) {
		inv.Nodes++
		id := firstGroup(reObjectID, m[1])
		parent[id] = firstGroup(reParentRef, m[1])
		if u := firstGroup(reUniqueName, m[1]); u != "" {
			idOf[u] = id
		}
	}
	for _, u := range requested {
		root, ok := idOf[u]
		if !ok {
			continue
		}
		for id := range parent {
			for cur, hops := id, 0; cur != "" && hops < 64; cur, hops = parent[cur], hops+1 {
				if cur == root {
					inv.PerPage[u]++
					break
				}
			}
		}
	}
	for _, m := range reNodeLocate.FindAllStringSubmatch(req, -1) {
		if firstGroup(reUniqueName, m[1]) == "" {
			inv.UnnamedParents = append(inv.UnnamedParents, firstGroup(reObjectID, m[1]))
		}
	}
	inv.Themes = uniqueMatches(reLocateTheme, req)
	inv.Skins = uniqueMatches(reLocateSkin, req)
	inv.Portlets = uniqueMatches(reLocatePortlt, req)
	inv.ManagedPages = len(reMapping.FindAllStringIndex(req, -1))
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" + req, inv, nil
}

// SetCreateOIDs puts create-oids="true" on every <request> element of an XMLAccess document (once, replacing any
// create-oids attribute it had): DX then gives new object IDs to the resources it creates instead of the ones in the
// XML (XMLAccess "ID generating mode"). For a traditional DX it is the same edit on the file before xmlaccess.sh.
func SetCreateOIDs(xml string) string {
	return reRequestTag.ReplaceAllStringFunc(xml, func(m string) string {
		attrs := ""
		if sm := reRequestTag.FindStringSubmatch(m); len(sm) > 1 {
			attrs = strings.TrimSpace(reCreateOIDs.ReplaceAllString(sm[1], ""))
		}
		if attrs == "" {
			return `<request create-oids="true">`
		}
		return `<request ` + attrs + ` create-oids="true">`
	})
}

// ErrPageNotFound: a page of the export does not exist in DX (EJPXA0240E).
var ErrPageNotFound = fmt.Errorf("page not found")

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func uniqueMatches(re *regexp.Regexp, s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		v := m[1]
		if d, err := url.PathUnescape(v); err == nil {
			v = d
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
