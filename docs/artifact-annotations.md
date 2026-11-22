# Attributes of the artifacts

**English** · [Español](artifact-annotations.es.md)

Every artifact the operator pushes (WCM exports of `DXWCMLibraryExport`, bundles of `DXExport`, the answers of
`DXXMLAccess`) describes itself with **manifest annotations**: the standard `org.opencontainers.image.*` ones and ours,
`com.europesip.dx.*`. The same format is written by **DX Transfer Studio** and read by Backstage, so the three tools
read each other's artifacts. No artifact type is set: the folder of the repository tells the kind.

**Schema 2** (`com.europesip.dx.schema: "2"`, since the DX Core release). Readers accept schema 1 too (see the end).

## Kinds and folders

| `com.europesip.dx.kind` | Folder | What it holds |
|---|---|---|
| `wcm` | `<project>/wcm/<origin>-<what>:<date>` | WCM libraries only: `wcm-export-<name>.zip` + `EXPORT-INFO.txt` |
| `site` | `<project>/sites/<origin>-<what>:<date>` | a bundle (`dx-bundle/v1`): `EXPORT-INFO.txt`, `bundle.json`, `themes/`, `xmlaccess/01-pages.xml`, `wcm/`; what is inside in `com.europesip.dx.bundle.parts` |
| `xmlaccess` | `<project>/xmlaccess/<resource>:<time>` | answers of `DXXMLAccess` |
| `portal` | `<project>/portal/<origin>-<what>:<date>` | **reserved** for a whole portal (XMLAccess release export). Not produced yet. It must **never** carry credentials: the full `Export.xml` of XMLAccess holds administration settings and credential vault data |

## Annotations

| Annotation | Value | Example | Why |
|---|---|---|---|
| `org.opencontainers.image.title` | Short name | `WCM export cf230-aeat` | what a registry and Backstage show |
| `org.opencontainers.image.description` | One line | `WCM export of 10 libraries from wps01 (virtual portal aeat)` | |
| `org.opencontainers.image.authors` | Who exported it | `Sergio Vera` | standard key, shown by any registry |
| `org.opencontainers.image.created` | When (RFC 3339, UTC) | `2026-11-22T17:10:57Z` | |
| `org.opencontainers.image.source` | Base URL of the DX | `https://dx.example.com` | standard key |
| `org.opencontainers.image.vendor` | `EUROPESIP` | | |
| `com.europesip.dx.schema` | `2` | | version of this convention |
| `com.europesip.dx.kind` | `wcm`, `site`, `xmlaccess` (`portal` reserved) | `wcm` | human words, one per folder |
| `com.europesip.dx.bundle.schema` | `dx-bundle/v1` | | sites |
| `com.europesip.dx.bundle.parts` | `themes`, `pages`, `portal-site`, `libraries`, comma separated | `themes,pages` | sites: what is inside |
| `com.europesip.dx.source.server` | **Short name** of the DX: `DXConnection.spec.sourceName` (DX Transfer Studio: the label of the server), default the connection name | `wps01` | a name people recognise, never an internal IP |
| `com.europesip.dx.source.url` | Base URL of the DX | `https://dx.example.com` | where it comes from, separate from the name |
| `com.europesip.dx.source.version` | Version DX reports | `HCL Digital Experience/8.5 build 20250923-234953` | import into the same CF or a newer one |
| `com.europesip.dx.source.connection` | DXConnection (namespace/name) or server id | `dxop-core-sergio/sergio-cf230` | |
| `com.europesip.dx.source.virtual-portal` | Virtual portal context; empty for the base portal | `aeat` | the key of the guide and of Backstage |
| `com.europesip.dx.source.portal` | Same value (schema 1 name) | `aeat` | kept for one version for schema 1 readers |
| `com.europesip.dx.libraries` | Library titles, separated by ` \| ` | `AEAT_DS \| AEAT_Contenido` | titles may hold commas |
| `com.europesip.dx.libraries.count` | How many | `2` | |
| `com.europesip.dx.pages` | Unique names of the root pages, separated by ` \| ` | `EuropeSip_web` | sites |
| `com.europesip.dx.requested-by` | Who (`spec.destination.authors`, else the annotation `dx.europesip.com/changed-by`) | `andres` | |
| `com.europesip.dx.reason` | Why (`spec.destination.reason`, else the annotation `dx.europesip.com/reason`, else the attribute `reason`) | `Migration CF230 → Compose` | |
| `com.europesip.dx.attr.<key>` | Free attributes (`spec.destination.attributes`) | `com.europesip.dx.attr.customer: aeat` | search and filters |
| `com.europesip.dx.tool.operator` / `com.europesip.dx.tool` | The operator / DX Transfer Studio and its version | `hcl-dx-scriptapp-operator 0.11.0` | |
| `com.europesip.dx.tool.created-with` | What created the resource (Backstage) | `backstage-europesip 1.0.0; …` | |
| `com.europesip.dx.resource` | The resource that made it | `dxop-core-sergio/cf230-aeat-wcm` | |

Free attributes: keys of lowercase letters, digits, dots and dashes (at most 63), values of at most 256 characters, at
most 32. Suggested keys: `customer`, `environment`, `project`, `ticket`, `release`.

The same data is in `EXPORT-INFO.txt`. Never passwords in any annotation or file.

## Reading older artifacts

Readers (the operator, DX Transfer Studio, Backstage) accept:

- kinds of schema 1 and of the guide v1: `wcm-export` → `wcm`; `dx-bundle`, `pages`, `theme` → `site`;
  `xmlaccess-output` → `xmlaccess`;
- lists separated by ` | ` or, when there is no `|`, by commas (guide v1);
- the virtual portal from `source.virtual-portal`, else `source.portal`;
- `source.server` holding a URL (schema 1): shown as it is.

`DXXMLAccess` artifacts keep the names of 0.6 too (`dx.europesip.com/resource`, `dx.europesip.com/virtual-portal`).
