package source

import (
	"testing"
	"time"
)

func TestArtifactAnnotations(t *testing.T) {
	t.Setenv("OPERATOR_VERSION", "0.8.0")
	a := ArtifactMeta{
		Kind: "wcm-export", Title: "WCM export x", Resource: "dx-portals/x", SourceServer: "https://dx.example.com",
		SourceVersion: "HCL Digital Experience Compose/9.5", SourceConnection: "dx-portals/demo", Libraries: []string{"orbita", "ThemeDS"},
		ResourceAnnotations: map[string]string{ResChangedBy: "user:default/andres", ResReason: "move to prod", ResCreatedWith: "backstage-europesip 1.0.0; @europesip/backstage-plugin-hcl-dx 0.6.0"},
		Attributes:          map[string]string{"customer": "europesip", "Bad Key": "x", "environment": ""},
		Created:             time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC),
	}.Annotations()
	want := map[string]string{
		"org.opencontainers.image.created": "2026-11-19T08:00:00Z", "org.opencontainers.image.vendor": "EUROPESIP",
		AnnKind: "wcm-export", AnnSchema: "1", AnnToolOperator: "hcl-dx-scriptapp-operator 0.8.0", AnnRequestedBy: "andres",
		AnnReason: "move to prod", AnnLibraries: "orbita | ThemeDS", AnnLibrariesCount: "2", AnnSourcePortal: "",
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
