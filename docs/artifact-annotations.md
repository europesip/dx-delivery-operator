# Attributes of the artifacts

**English** · [Español](artifact-annotations.es.md)

Every artifact the operator pushes to a registry (WCM exports of `DXWCMLibraryExport`, bundles of `DXExport`, the
answers of `DXXMLAccess`)
describes itself with **manifest annotations**: the standard `org.opencontainers.image.*` ones and ours,
`com.europesip.dx.*`. Registries show them (Harbor: *Annotations* of the artifact; `oras manifest fetch <ref>`), and
a tool can list or filter artifacts by them without downloading the files. No artifact type is set: WCM exports are
recognised by the `wcm/` folder of their repository, and by `com.europesip.dx.kind`.

Schema version: `com.europesip.dx.schema: "1"`. New keys may be added; existing ones keep their meaning.

| Annotation | Value | Example |
|---|---|---|
| `org.opencontainers.image.created` | When it was pushed (RFC 3339, UTC) | `2026-11-19T08:00:00Z` |
| `org.opencontainers.image.title` | Short name | `WCM export export-orbita-20261119-0717` |
| `org.opencontainers.image.description` | One line | `WCM export of 2 libraries from https://dx.example.com` |
| `org.opencontainers.image.source` | The DX it comes from | `https://dx.example.com` |
| `org.opencontainers.image.vendor` | `EUROPESIP` | |
| `com.europesip.dx.schema` | Version of this convention | `1` |
| `com.europesip.dx.kind` | `wcm-export`, `dx-bundle` or `xmlaccess-output` | `wcm-export` |
| `com.europesip.dx.resource` | The resource that made it (namespace/name) | `dx-portals/export-orbita-20261119-0717` |
| `com.europesip.dx.source.server` | Base URL of the DX | `https://dx.example.com` |
| `com.europesip.dx.source.version` | Version DX reports | `HCL Digital Experience Compose/9.5 build 20261103-170843` |
| `com.europesip.dx.source.connection` | DXConnection (namespace/name) | `dx-portals/demorbita` |
| `com.europesip.dx.source.portal` | Virtual portal context; empty for the base portal | `europesip` |
| `com.europesip.dx.libraries` | WCM exports: the titles, separated by ` \| ` | `orbita \| ThemeDS` |
| `com.europesip.dx.libraries.count` | WCM exports: how many | `2` |
| `com.europesip.dx.bundle.schema` | DX bundles: format | `dx-bundle/v1` |
| `com.europesip.dx.bundle.parts` | DX bundles: parts, comma separated | `pages,portal-site,libraries` |
| `com.europesip.dx.pages` | DX bundles: unique names of the pages, separated by ` \| ` | `orbita-site` |
| `com.europesip.dx.requested-by` | Who asked for it (annotation `dx.europesip.com/changed-by` of the resource) | `andres` |
| `com.europesip.dx.reason` | Why (annotation `dx.europesip.com/reason`) | `copy to production` |
| `com.europesip.dx.tool.operator` | This operator and its version | `hcl-dx-scriptapp-operator 0.8.0` |
| `com.europesip.dx.tool.created-with` | What created the resource (annotation `dx.europesip.com/created-with`; Backstage writes its version and the plugin's) | `backstage-europesip 1.0.0; @europesip/backstage-plugin-hcl-dx 0.6.0` |
| `com.europesip.dx.attr.<key>` | Free attributes: `spec.destination.attributes` of a `DXWCMLibraryExport` | `com.europesip.dx.attr.customer: europesip` |

Free attributes: keys of lowercase letters, digits, dots and dashes (at most 63 characters), values of at most 256
characters, at most 32 per artifact. Empty values are left out. Suggested keys: `environment`, `customer`, `project`,
`ticket`, `release`.

The same data is in `EXPORT-INFO.txt` of a WCM export, for whoever only has the files.

`DXXMLAccess` artifacts keep the names of 0.6 too (`dx.europesip.com/resource`, `dx.europesip.com/virtual-portal`).
