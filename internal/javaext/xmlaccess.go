// SPDX-License-Identifier: MIT

/*
Copyright 2026 EUROPESIP.

Licensed under the MIT License (see LICENSE).
*/

package javaext

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Registration is what the operator registers in DX for a portlet extension.
type Registration struct {
	App         PortletApp
	File        string // WAR folder name in customApplications
	ContextRoot string
	DisplayName string
	Page        *Page
}

// Page of a registration.
type Page struct {
	UniqueName       string
	ParentUniqueName string
	Titles           map[string]string // locale -> title
	FriendlyName     string
	Anonymous        bool
}

func xa(s string) string { return xmlAttr(s) }

func cdata(s string) string {
	return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
}

const anonymousUser = `<role actionset="User" update="set"><mapping subjectid="anonymous portal user" subjecttype="user" update="set"/></role>`

// WebModuleUID: DX names the web module of a portlet application <uid>.webmod.
func WebModuleUID(appUID string) string { return appUID + ".webmod" }

// RegisterXML is the XMLAccess request that registers a WAR already running in Liberty (predeployed) with its
// portlets and, optionally, places the first portlet on a page. Every action is update: running it again changes
// nothing.
func RegisterXML(r Registration) string {
	anon := r.Page != nil && r.Page.Anonymous
	acl := func() string {
		if !anon {
			return ""
		}
		return `<access-control externalized="false" owner="undefined" private="false">` + anonymousUser + `</access-control>`
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="PortalConfig_8.0.0.xsd" type="update" create-oids="true">` + "\n")
	b.WriteString(`<portal action="locate">` + "\n")
	fmt.Fprintf(&b, `<web-app action="update" uid="%s" active="true" predeployed="true" objectid="jx.webmod">`+"\n", xa(WebModuleUID(r.App.UID)))
	fmt.Fprintf(&b, "<url>file://localhost%s/%s</url>\n", AppsDir, xa(r.File))
	fmt.Fprintf(&b, "<context-root>%s</context-root>\n", xa(r.ContextRoot))
	fmt.Fprintf(&b, "<display-name>%s</display-name>\n", xa(r.DisplayName))
	b.WriteString(acl() + "\n")
	fmt.Fprintf(&b, `<portlet-app action="update" active="true" name="%s" uid="%s">`+"\n", xa(r.App.UID), xa(r.App.UID))
	for i, p := range r.App.Portlets {
		fmt.Fprintf(&b, `<portlet action="update" active="true" name="%s" objectid="jx.portlet.%d">%s</portlet>`+"\n", xa(p), i, acl())
	}
	b.WriteString("</portlet-app>\n</web-app>\n")
	if pg := r.Page; pg != nil {
		parent := pg.ParentUniqueName
		if parent == "" {
			parent = "ibm.portal.Home"
		}
		fmt.Fprintf(&b, `<content-node action="locate" objectid="jx.parent" uniquename="%s"/>`+"\n", xa(parent))
		fmt.Fprintf(&b, `<content-node action="update" uniquename="%s" content-parentref="jx.parent" ordinal="last" active="true" allportletsallowed="true" create-type="explicit" type="page" domain="rel">`+"\n", xa(pg.UniqueName))
		b.WriteString(`<supported-markup markup="html" update="set"/>` + "\n")
		locales := make([]string, 0, len(pg.Titles))
		for l := range pg.Titles {
			locales = append(locales, l)
		}
		sort.Strings(locales)
		for _, l := range locales {
			fmt.Fprintf(&b, `<localedata locale="%s"><title>%s</title></localedata>`+"\n", xa(l), xa(pg.Titles[l]))
		}
		if pg.FriendlyName != "" {
			fmt.Fprintf(&b, `<parameter name="com.ibm.portal.friendly.name" type="string" update="set">%s</parameter>`+"\n", cdata(pg.FriendlyName))
		}
		b.WriteString(acl() + "\n")
		fmt.Fprintf(&b, `<component action="update" active="true" domain="rel" ordinal="100" orientation="H" type="container" uniquename="%s">`+"\n", xa(pg.UniqueName+".row"))
		b.WriteString(`<component action="update" active="true" domain="rel" ordinal="100" orientation="V" type="container">` + "\n")
		b.WriteString(`<component action="update" active="true" domain="rel" ordinal="100" type="control"><portletinstance action="update" domain="rel" portletref="jx.portlet.0"/></component>` + "\n")
		b.WriteString("</component>\n</component>\n</content-node>\n")
	}
	b.WriteString("</portal>\n</request>\n")
	return b.String()
}

// ExportXML exports the web module of a portlet application (to know whether it is registered).
func ExportXML(appUID string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="PortalConfig_8.0.0.xsd" type="export">` + "\n" +
		fmt.Sprintf(`<portal action="locate"><web-app action="export" uid="%s"/></portal>`, xa(WebModuleUID(appUID))) + "\n</request>\n"
}

// UnregisterXML deletes the web module (and its portlets) and, when given, the page.
func UnregisterXML(appUID, pageUniqueName string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="PortalConfig_8.0.0.xsd" type="update">` + "\n<portal action=\"locate\">\n")
	if pageUniqueName != "" {
		fmt.Fprintf(&b, `<content-node action="delete" uniquename="%s"/>`+"\n", xa(pageUniqueName))
	}
	fmt.Fprintf(&b, `<web-app action="delete" uid="%s"/>`+"\n", xa(WebModuleUID(appUID)))
	b.WriteString("</portal>\n</request>\n")
	return b.String()
}

var (
	exportedPortlet = regexp.MustCompile(`<portlet\s[^>]*\bname="([^"]+)"`)
	statusOK        = regexp.MustCompile(`<status\s+element="all"\s+result="ok"`)
	failMessage     = regexp.MustCompile(`(?s)<message[^>]*>(.*?)</message>`)
	notFoundCode    = regexp.MustCompile(`EJPXA0240E|EJPXA0034E|could not be found|not found`)
)

// ParseExport reads the result of ExportXML: whether the web module exists and the names of its portlets. A
// failure that is not "not found" is an error (never take a temporary failure for a missing module).
func ParseExport(result string) (exists bool, portlets []string, err error) {
	if !statusOK.MatchString(result) {
		msg := "XMLAccess did not report success"
		if m := failMessage.FindStringSubmatch(result); m != nil {
			msg = strings.TrimSpace(m[1])
		}
		if notFoundCode.MatchString(result) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("XMLAccess export: %s", msg)
	}
	for _, m := range exportedPortlet.FindAllStringSubmatch(result, -1) {
		portlets = append(portlets, m[1])
	}
	sort.Strings(portlets)
	return true, portlets, nil
}

// SameSet compares two sorted lists.
func SameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
