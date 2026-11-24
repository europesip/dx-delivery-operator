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
	IDs         Identifiers
}

// Identifiers of a registration that are the same in every environment (issue #69).
//
// Fixed object IDs are not offered: tested on DX Compose CF239, DX ignores the objectid of a predeployed web module,
// its portlet application and its portlets (create-oids="true" or not) and generates its own; with action="create"
// the request fails (the deployment already created the portlet application from portlet.xml). Unique names are
// kept, can be added to an existing registration and can be used to locate a portlet in XMLAccess.
type Identifiers struct {
	// UniqueNames: register every portlet with PortletUniqueName (the same in every portal).
	UniqueNames bool
}

// PortletUniqueName is the unique name of a portlet: <portlet-app uid>.<portlet-name>, derived from portlet.xml, so
// it is the same in every environment.
func PortletUniqueName(appUID, portlet string) string { return appUID + "." + portlet }

// RegisteredIDs are the identifiers DX has for a registered web module (read from an export).
type RegisteredIDs struct {
	WebModuleOID  string
	PortletAppOID string
	Portlets      []RegisteredPortlet
}

// RegisteredPortlet is one portlet as DX has it.
type RegisteredPortlet struct {
	Name       string
	UniqueName string
	ObjectID   string
}

// Portlet returns the portlet with that name (nil when it is not there).
func (r RegisteredIDs) Portlet(name string) *RegisteredPortlet {
	for i := range r.Portlets {
		if r.Portlets[i].Name == name {
			return &r.Portlets[i]
		}
	}
	return nil
}

// portletRef is the temporary id of a portlet inside the request.
func (r Registration) portletRef(i int) string { return fmt.Sprintf("jx.portlet.%d", i) }

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
		un := ""
		if r.IDs.UniqueNames {
			un = fmt.Sprintf(` uniquename="%s"`, xa(PortletUniqueName(r.App.UID, p)))
		}
		fmt.Fprintf(&b, `<portlet action="update" active="true" name="%s" objectid="%s"%s>%s</portlet>`+"\n", xa(p), xa(r.portletRef(i)), un, acl())
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
		b.WriteString(`<component action="update" active="true" domain="rel" ordinal="100" type="control"><portletinstance action="update" domain="rel" portletref="` + xa(r.portletRef(0)) + `"/></component>` + "\n")
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
	// the schema (PortalConfig_8.0.0.xsd) wants web-app before content-node: the other order fails as a whole
	// (EJPXA0049E) and nothing is deleted (issue #69)
	fmt.Fprintf(&b, `<web-app action="delete" uid="%s"/>`+"\n", xa(WebModuleUID(appUID)))
	if pageUniqueName != "" {
		fmt.Fprintf(&b, `<content-node action="delete" uniquename="%s"/>`+"\n", xa(pageUniqueName))
	}
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

var (
	exportedWebApp     = regexp.MustCompile(`<web-app\s[^>]*>`)
	exportedPortletApp = regexp.MustCompile(`<portlet-app\s[^>]*>`)
	exportedPortletTag = regexp.MustCompile(`<portlet\s[^>]*>`)
	xmlAttribute       = regexp.MustCompile(`\b([a-zA-Z-]+)="([^"]*)"`)
)

func attrs(tag string) map[string]string {
	m := map[string]string{}
	for _, a := range xmlAttribute.FindAllStringSubmatch(tag, -1) {
		m[a[1]] = a[2]
	}
	return m
}

// ParseIDs reads the object IDs and unique names of a successful export of a web module (ExportXML).
func ParseIDs(result string) RegisteredIDs {
	var r RegisteredIDs
	if t := exportedWebApp.FindString(result); t != "" {
		r.WebModuleOID = attrs(t)["objectid"]
	}
	if t := exportedPortletApp.FindString(result); t != "" {
		r.PortletAppOID = attrs(t)["objectid"]
	}
	for _, t := range exportedPortletTag.FindAllString(result, -1) {
		a := attrs(t)
		if a["name"] == "" {
			continue
		}
		r.Portlets = append(r.Portlets, RegisteredPortlet{Name: a["name"], UniqueName: a["uniquename"], ObjectID: a["objectid"]})
	}
	sort.Slice(r.Portlets, func(i, j int) bool { return r.Portlets[i].Name < r.Portlets[j].Name })
	return r
}

// MissingUniqueNames lists the portlets registered without their unique name (or with another one).
func MissingUniqueNames(appUID string, got RegisteredIDs) []string {
	var out []string
	for _, p := range got.Portlets {
		if p.UniqueName != PortletUniqueName(appUID, p.Name) {
			out = append(out, p.Name)
		}
	}
	return out
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
