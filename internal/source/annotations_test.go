package source

import (
	"testing"
	"time"
)

func TestArtifactAnnotations(t *testing.T) {
	t.Setenv("OPERATOR_VERSION", "0.8.0")
	a := ArtifactMeta{
		Kind: KindWCM, Title: "WCM export x", Resource: "dx-portals/x", SourceServer: "demo", SourceURL: "https://dx.example.com",
		SourceVersion: "HCL Digital Experience Compose/9.5", SourceConnection: "dx-portals/demo", Libraries: []string{"orbita", "ThemeDS"},
		ResourceAnnotations: map[string]string{ResChangedBy: "user:default/andres", ResReason: "move to prod", ResCreatedWith: "backstage-europesip 1.0.0; @europesip/backstage-plugin-hcl-dx 0.6.0"},
		Attributes:          map[string]string{"customer": "europesip", "Bad Key": "x", "environment": ""},
		Created:             time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC),
	}.Annotations()
	want := map[string]string{
		"org.opencontainers.image.created": "2026-11-19T08:00:00Z", "org.opencontainers.image.vendor": "EUROPESIP",
		AnnKind: "wcm", AnnSchema: "2", AnnToolOperator: "hcl-dx-scriptapp-operator 0.8.0", AnnRequestedBy: "andres", AnnAuthors: "andres",
		AnnReason: "move to prod", AnnLibraries: "orbita | ThemeDS", AnnLibrariesCount: "2", AnnSourcePortal: "", AnnSourceVP: "",
		AnnSourceServer: "demo", AnnSourceURL: "https://dx.example.com", "org.opencontainers.image.source": "https://dx.example.com",
		AnnToolCreatedBy: "backstage-europesip 1.0.0; @europesip/backstage-plugin-hcl-dx 0.6.0", AnnAttrPrefix + "customer": "europesip",
	}
	for k, v := range want {
		if got, ok := a[k]; !ok || got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	for _, k := range []string{AnnAttrPrefix + "Bad Key", AnnAttrPrefix + "environment"} {
		if _, ok := a[k]; ok {
			t.Errorf("%s should be left out", k)
		}
	}
}

func TestArtifactAnnotationsSpecFieldsWin(t *testing.T) {
	a := ArtifactMeta{Kind: KindSite, Authors: "Sergio", Reason: "migration", SourcePortal: "aeat",
		ResourceAnnotations: map[string]string{ResChangedBy: "user:default/andres", ResReason: "other"}}.Annotations()
	if a[AnnAuthors] != "Sergio" || a[AnnRequestedBy] != "Sergio" || a[AnnReason] != "migration" || a[AnnSourceVP] != "aeat" || a[AnnSourcePortal] != "aeat" {
		t.Fatalf("%v", a)
	}
	b := ArtifactMeta{Kind: KindWCM, Attributes: map[string]string{"reason": "from attr"}}.Annotations()
	if b[AnnReason] != "from attr" || b[AnnAttrPrefix+"reason"] != "from attr" {
		t.Fatalf("reason from the attribute: %v", b)
	}
}

func TestReaders(t *testing.T) {
	for in, want := range map[string]string{"wcm-export": "wcm", "wcm": "wcm", "dx-bundle": "site", "pages": "site", "theme": "site", "xmlaccess-output": "xmlaccess", "portal": "portal"} {
		if got := NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
	if l := SplitList("a, b | c"); len(l) != 2 || l[0] != "a, b" {
		t.Errorf("pipe wins: %v", l)
	}
	if l := SplitList("a,b, c"); len(l) != 3 {
		t.Errorf("commas: %v", l)
	}
	if VirtualPortalOf(map[string]string{AnnSourcePortal: "x"}) != "x" || VirtualPortalOf(map[string]string{AnnSourceVP: "", AnnSourcePortal: "x"}) != "" {
		t.Error("VirtualPortalOf")
	}
}
