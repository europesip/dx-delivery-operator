package dx

import (
	"errors"
	"strings"
	"testing"
)

// An answer of DX Compose CF239 to PagesExportXML (orbita-site of demorbita, shortened), with one child page added.
const pagesAnswer = `<?xml version="1.0" encoding="UTF-8"?>
<!-- HCL Digital Experience Compose/9.5 build 20261103-170843 exported on Thu Nov 19 08:43:23 CET 2026 from dx-deployment-web-engine-0/10.42.4.102 virtual portal: none (default virtual portal) -->
<!-- 7 [content-node Z6_4G9I0G01110000MGNFF3VN0OL1 uniquename=orbita-site] -->
<request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" build="20261103-170843" type="update" version="9.5.0.0" xsi:noNamespaceSchemaLocation="PortalConfig_8.5.0.xsd">
    <portal action="locate">
        <skin action="locate" domain="rel" objectid="ZK_4G9I0G01110000MGNFF3VN0OL4" uniquename="custom.portal.skin.Orbita%20%28copia%20de%208%2C5%29.Standard"/>
        <theme action="locate" domain="rel" objectid="ZJ_4G9I0G01110000MGNFF3VN0O57" uniquename="custom.portal.theme.Orbita%20%28copia%20de%208%2C5%29"/>
        <web-app action="locate" domain="rel" uid="BlurbPortlet.jsr.webmod">
            <portlet-app action="locate" domain="rel" name="BlurbPortlet.jsr" uid="BlurbPortlet.jsr">
                <portlet action="locate" domain="rel" name="Information Portlet" uniquename="wps.p.Information.jsr"/>
            </portlet-app>
        </web-app>
        <content-node action="locate" domain="rel" objectid="Z6_000000000000000000000000A0" uniquename="wps.content.root"/>
        <content-node action="update" active="true" content-parentref="Z6_000000000000000000000000A0" domain="rel" objectid="Z6_ORB" ordinal="329680" themeref="ZJ_4G9I0G01110000MGNFF3VN0O57" type="label" uniquename="orbita-site">
            <localedata locale="en"><title>Orbita</title></localedata>
            <access-control externalized="false" owner="uid=wpsadmin,o=defaultwimfilebasedrealm" private="false"/>
            <content-mapping-info>
                <content-mapping content-id="c884745c-b20c-4045-994d-f3669fb036fa" default="true"/>
            </content-mapping-info>
        </content-node>
        <content-node action="update" active="true" content-parentref="Z6_ORB" domain="rel" objectid="Z6_CHILD" ordinal="100" type="staticpage">
            <access-control externalized="false" owner="uid=wpsadmin,o=defaultwimfilebasedrealm" private="false"/>
        </content-node>
    </portal>
    <status element="all" result="ok"/>
</request>
`

func TestPagesExportXML(t *testing.T) {
	x := PagesExportXML([]PageSelection{{UniqueName: "orbita-site", Descendants: true}, {UniqueName: "a&b", Descendants: false}})
	for _, want := range []string{`type="export"`, `<content-node action="export" uniquename="orbita-site" export-descendants="true"/>`, `uniquename="a&amp;b" export-descendants="false"`} {
		if !strings.Contains(x, want) {
			t.Errorf("request lacks %q:\n%s", want, x)
		}
	}
}

func TestPagesImportXML(t *testing.T) {
	req, inv, err := PagesImportXML(pagesAnswer, []string{"orbita-site"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<!--", "10.42.4.102", `build="`, `version="9.5`, "<status", `owner="`} {
		if strings.Contains(req, bad) {
			t.Errorf("import request still has %q", bad)
		}
	}
	for _, want := range []string{`<?xml version="1.0" encoding="UTF-8"?>`, `type="update">`, `xsi:noNamespaceSchemaLocation="PortalConfig_8.5.0.xsd"`, `objectid="Z6_ORB"`, `content-id="c884745c-b20c-4045-994d-f3669fb036fa"`} {
		if !strings.Contains(req, want) {
			t.Errorf("import request lacks %q", want)
		}
	}
	head := req[strings.Index(req, "<request"):strings.Index(req, "\n    <portal")]
	// 0.9.1: the object IDs of the source are kept (chosen when importing)
	if strings.Contains(head, "create-oids") || strings.Count(head, ` type="`) != 1 {
		t.Errorf("request attributes: %s", head)
	}
	if inv.Nodes != 2 || inv.PerPage["orbita-site"] != 2 || inv.ManagedPages != 1 {
		t.Errorf("inventory %+v", inv)
	}
	if len(inv.Themes) != 1 || inv.Themes[0] != "custom.portal.theme.Orbita (copia de 8,5)" || len(inv.Skins) != 1 || len(inv.Portlets) != 1 || inv.Portlets[0] != "Information Portlet" {
		t.Errorf("references %+v", inv)
	}
	if len(inv.UnnamedParents) != 0 {
		t.Errorf("unnamed parents %v", inv.UnnamedParents)
	}
}

func TestPagesImportXMLNotFound(t *testing.T) {
	ans := `<?xml version="1.0"?><request type="update"><status element="all" result="failed"><message id="EJPXA0240E">EJPXA0240E: The resource content-node with uniquename=nope was not found.</message></status></request>`
	if _, _, err := PagesImportXML(ans, []string{"nope"}); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPagesImportXMLUnnamedParent(t *testing.T) {
	ans := strings.Replace(pagesAnswer, `objectid="Z6_000000000000000000000000A0" uniquename="wps.content.root"/>`, `objectid="Z6_PARENT"/>`, 1)
	_, inv, err := PagesImportXML(ans, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.UnnamedParents) != 1 || inv.UnnamedParents[0] != "Z6_PARENT" {
		t.Errorf("unnamed parents %v", inv.UnnamedParents)
	}
}

func TestSetCreateOIDs(t *testing.T) {
	for in, want := range map[string]string{
		`<request type="update"><portal/></request>`:                                   `<request type="update" create-oids="true"><portal/></request>`,
		`<request type="update" create-oids="false" x="1"><portal/></request>`:         `<request type="update" x="1" create-oids="true"><portal/></request>`,
		`<request create-oids="true" type="update">`:                                   `<request type="update" create-oids="true">`,
		"<?xml version=\"1.0\"?>\n<request\n type=\"update\">":                    "<?xml version=\"1.0\"?>\n<request type=\"update\" create-oids=\"true\">",
		`<request>`:                                                                    `<request create-oids="true">`,
	} {
		if got := SetCreateOIDs(in); got != want {
			t.Errorf("SetCreateOIDs(%q) = %q, want %q", in, got, want)
		}
		if got := SetCreateOIDs(SetCreateOIDs(in)); strings.Count(got, "create-oids") != 1 {
			t.Errorf("twice: %q", got)
		}
	}
}
