package source

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The attributes of the artifacts the operator pushes (19-nov-2026, docs/artifact-annotations.md): manifest
// annotations, the standard org.opencontainers.image.* ones plus com.europesip.dx.*, so registries (Harbor, ORAS) can
// show them and a later version can list or filter artifacts by them without opening the files.
const (
	AnnPrefix         = "com.europesip.dx."
	AnnKind           = AnnPrefix + "kind"           // wcm-export, xmlaccess-output
	AnnSchema         = AnnPrefix + "schema"         // version of this convention: 1
	AnnResource       = AnnPrefix + "resource"       // namespace/name of the resource that made it
	AnnSourceServer   = AnnPrefix + "source.server"  // base URL of the DX
	AnnSourceVersion  = AnnPrefix + "source.version" // HCL Digital Experience Compose/9.5 build ...
	AnnSourceConn     = AnnPrefix + "source.connection"
	AnnSourcePortal   = AnnPrefix + "source.portal" // virtual portal context, "" = base portal
	AnnLibraries      = AnnPrefix + "libraries"     // titles, separated by " | " (cut at 4 KiB)
	AnnLibrariesCount = AnnPrefix + "libraries.count"
	AnnRequestedBy    = AnnPrefix + "requested-by"
	AnnReason         = AnnPrefix + "reason"
	AnnToolOperator   = AnnPrefix + "tool.operator"     // hcl-dx-scriptapp-operator <version>
	AnnToolCreatedBy  = AnnPrefix + "tool.created-with" // what created the resource (Backstage, its plugin)
	AnnAttrPrefix     = AnnPrefix + "attr."             // free attributes: com.europesip.dx.attr.<key>
	AnnBundleSchema   = AnnPrefix + "bundle.schema"     // dx-bundle/v1 (DXExport)
	AnnBundleParts    = AnnPrefix + "bundle.parts"      // pages,portal-site,libraries
	AnnPages          = AnnPrefix + "pages"             // unique names of the pages exported, separated by " | "
	AnnSchemaVersion  = "1"

	// Annotations of the resource read for the artifact (Backstage writes them).
	ResChangedBy   = "dx.europesip.com/changed-by"
	ResReason      = "dx.europesip.com/reason"
	ResCreatedWith = "dx.europesip.com/created-with"
)

// AttributeKeyRe is what a free attribute key may be (lowercase, digits, dots, dashes).
var AttributeKeyRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$`)

// OperatorVersion is the version of this operator (OPERATOR_VERSION, set by the chart), "dev" without it.
func OperatorVersion() string {
	if v := strings.TrimSpace(os.Getenv("OPERATOR_VERSION")); v != "" {
		return v
	}
	return "dev"
}

// ArtifactMeta is what an artifact says about itself.
type ArtifactMeta struct {
	Kind, Title, Description       string
	Resource                       string
	SourceServer, SourceVersion    string
	SourceConnection, SourcePortal string
	Libraries                      []string
	ResourceAnnotations            map[string]string
	Attributes                     map[string]string
	// Extra annotations (com.europesip.dx.* keys of this file), written as they are.
	Extra   map[string]string
	Created time.Time
}

// Annotations builds the manifest annotations of an artifact. Empty values are left out.
func (m ArtifactMeta) Annotations() map[string]string {
	a := map[string]string{
		"org.opencontainers.image.created": m.Created.UTC().Format(time.RFC3339),
		"org.opencontainers.image.vendor":  "EUROPESIP",
		AnnSchema:                          AnnSchemaVersion,
		AnnToolOperator:                    "hcl-dx-scriptapp-operator " + OperatorVersion(),
	}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			if len(v) > 4096 {
				v = v[:4096]
			}
			a[k] = v
		}
	}
	set("org.opencontainers.image.title", m.Title)
	set("org.opencontainers.image.description", m.Description)
	set("org.opencontainers.image.source", m.SourceServer)
	set(AnnKind, m.Kind)
	set(AnnResource, m.Resource)
	set(AnnSourceServer, m.SourceServer)
	set(AnnSourceVersion, m.SourceVersion)
	set(AnnSourceConn, m.SourceConnection)
	if m.Kind != "" {
		a[AnnSourcePortal] = m.SourcePortal
	}
	if len(m.Libraries) > 0 {
		set(AnnLibraries, strings.Join(m.Libraries, " | "))
		a[AnnLibrariesCount] = strconv.Itoa(len(m.Libraries))
	}
	ra := m.ResourceAnnotations
	set(AnnRequestedBy, strings.TrimPrefix(strings.TrimPrefix(ra[ResChangedBy], "user:"), "default/"))
	set(AnnReason, ra[ResReason])
	set(AnnToolCreatedBy, ra[ResCreatedWith])
	for k, v := range m.Extra {
		set(k, v)
	}
	keys := make([]string, 0, len(m.Attributes))
	for k := range m.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if AttributeKeyRe.MatchString(k) {
			set(AnnAttrPrefix+k, m.Attributes[k])
		}
	}
	return a
}
