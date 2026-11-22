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
	AnnSourcePortal   = AnnPrefix + "source.portal"         // virtual portal context, "" = base portal (schema 1; kept in schema 2)
	AnnSourceVP       = AnnPrefix + "source.virtual-portal" // virtual portal context, "" = base portal (schema 2, guide v1)
	AnnSourceURL      = AnnPrefix + "source.url"            // base URL of the DX (schema 2); source.server is a short name
	AnnLibraries      = AnnPrefix + "libraries"             // titles, separated by " | " (cut at 4 KiB)
	AnnLibrariesCount = AnnPrefix + "libraries.count"
	AnnRequestedBy    = AnnPrefix + "requested-by"
	AnnReason         = AnnPrefix + "reason"
	AnnToolOperator   = AnnPrefix + "tool.operator" // hcl-dx-scriptapp-operator <version>
	AnnAuthors        = "org.opencontainers.image.authors"
	AnnToolCreatedBy  = AnnPrefix + "tool.created-with" // what created the resource (Backstage, its plugin)
	AnnAttrPrefix     = AnnPrefix + "attr."             // free attributes: com.europesip.dx.attr.<key>
	AnnBundleSchema   = AnnPrefix + "bundle.schema"     // dx-bundle/v1 (DXExport)
	AnnBundleParts    = AnnPrefix + "bundle.parts"      // pages,portal-site,libraries
	AnnPages          = AnnPrefix + "pages"             // unique names of the pages exported, separated by " | "
	AnnThemes         = AnnPrefix + "themes"            // unique names of the themes, separated by " | " (theme, site)
	AnnScriptApps     = AnnPrefix + "scriptapps"        // names of the Script Applications, separated by " | " (app)
	AnnPortlets       = AnnPrefix + "portlets"          // names of the portlets / web modules, separated by " | " (app)
	AnnPortletsJSR    = AnnPrefix + "portlets.jsr"      // 168, 286 or mixed (app)
	AnnPortletsWAR    = AnnPrefix + "portlets.war"      // included or missing (app)
	AnnSchemaVersion  = "2"

	// Kinds (com.europesip.dx.kind, schema 2). Each one has its folder in the project: wcm/, sites/, themes/, apps/,
	// xmlaccess/, portal/.
	KindWCM       = "wcm"       // WCM libraries only
	KindSite      = "site"      // a bundle: themes, pages, Portal Site and/or libraries (com.europesip.dx.bundle.parts)
	KindTheme     = "theme"     // a bundle with only themes (registration + skins + WebDAV folder), issue #49
	KindApp       = "app"       // Script Applications and/or portlets (WAR + XMLAccess), issue #50
	KindXMLAccess = "xmlaccess" // answers of DXXMLAccess
	KindPortal    = "portal"    // reserved: a whole portal (XMLAccess release export); never with credentials

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
	Kind, Title, Description string
	Resource                 string
	// SourceServer: short name of the DX (DXConnection spec.sourceName, or its name); SourceURL: its base URL.
	SourceServer, SourceURL string
	SourceVersion           string
	// Authors and Reason; empty: the annotations dx.europesip.com/changed-by and dx.europesip.com/reason of the
	// resource (then the attribute reason).
	Authors, Reason                string
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
	set("org.opencontainers.image.source", m.SourceURL)
	set(AnnKind, m.Kind)
	set(AnnResource, m.Resource)
	set(AnnSourceServer, m.SourceServer)
	set(AnnSourceURL, m.SourceURL)
	set(AnnSourceVersion, m.SourceVersion)
	set(AnnSourceConn, m.SourceConnection)
	if m.Kind != "" {
		a[AnnSourceVP] = m.SourcePortal
		a[AnnSourcePortal] = m.SourcePortal // schema 1 readers; to be removed in a later version
	}
	if len(m.Libraries) > 0 {
		set(AnnLibraries, strings.Join(m.Libraries, " | "))
		a[AnnLibrariesCount] = strconv.Itoa(len(m.Libraries))
	}
	ra := m.ResourceAnnotations
	authors := strings.TrimSpace(m.Authors)
	if authors == "" {
		authors = strings.TrimPrefix(strings.TrimPrefix(ra[ResChangedBy], "user:"), "default/")
	}
	set(AnnRequestedBy, authors)
	set(AnnAuthors, authors)
	reason := strings.TrimSpace(m.Reason)
	if reason == "" {
		reason = strings.TrimSpace(ra[ResReason])
	}
	if reason == "" {
		reason = m.Attributes["reason"]
	}
	set(AnnReason, reason)
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

// NormalizeKind turns the kinds of schema 1 and of the guide v1 into the kinds of schema 2.
func NormalizeKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "wcm-export", "wcm":
		return KindWCM
	case "dx-bundle", "site", "sites", "pages":
		return KindSite
	case "theme", "themes":
		return KindTheme
	case "app", "apps", "application", "applications":
		return KindApp
	case "xmlaccess-output", "xmlaccess":
		return KindXMLAccess
	case "portal":
		return KindPortal
	}
	return strings.TrimSpace(k)
}

// SplitList reads a list annotation (libraries, pages): " | " separated (schema 1 and 2) or comma separated (guide v1).
func SplitList(v string) []string {
	sep := ","
	if strings.Contains(v, "|") {
		sep = "|"
	}
	var out []string
	for _, x := range strings.Split(v, sep) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// VirtualPortalOf reads the virtual portal of an artifact: source.virtual-portal (schema 2, guide v1) or
// source.portal (schema 1).
func VirtualPortalOf(a map[string]string) string {
	if v, ok := a[AnnSourceVP]; ok {
		return v
	}
	return a[AnnSourcePortal]
}

// FolderOf is the folder of the project (dx-artifacts) where artifacts of a kind are pushed.
func FolderOf(kind string) string {
	switch NormalizeKind(kind) {
	case KindWCM:
		return "wcm"
	case KindTheme:
		return "themes"
	case KindApp:
		return "apps"
	case KindXMLAccess:
		return "xmlaccess"
	case KindPortal:
		return "portal"
	}
	return "sites"
}

// KindOfBundle is the kind of a dx-bundle/v1 by its parts: only themes = theme; otherwise site.
func KindOfBundle(parts []string) string {
	if len(parts) == 1 && parts[0] == "themes" {
		return KindTheme
	}
	return KindSite
}
