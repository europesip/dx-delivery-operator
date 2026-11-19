# Portable bundles (DXExport)

**English** · [Español](portable-bundles.es.md)

A **DX bundle** is what `DXExport` pushes to a registry: one OCI artifact (ORAS, no artifact type) with pages, the
Portal Site library and WCM libraries of a DX Compose portal, **every file in the native format of HCL DX**. The same
bundle imports into another **DX Compose** (with the operator, or from Backstage) and into a **traditional HCL DX on
WebSphere** (8.5, 9.0, 9.5) with its own tools, `xmlaccess.sh` and `ConfigEngine.sh`.

## Format: `dx-bundle/v1`

```
EXPORT-INFO.txt            for people: source, versions, who and why, contents, warnings, HOW TO IMPORT (both ways)
bundle.json                for tools: the same, with the import steps and the size and sha256 of every file
xmlaccess/01-pages.xml     the pages: an XMLAccess update request, create-oids="true"
wcm/wcm-export-<name>.zip  the WCM libraries and Portal Site: the JCR export of the WCM data module
```

Files are layers named by `org.opencontainers.image.title` (with their folder). The manifest carries the
[attributes of the artifacts](artifact-annotations.md) with `com.europesip.dx.kind: dx-bundle`,
`com.europesip.dx.bundle.schema: dx-bundle/v1`, `com.europesip.dx.bundle.parts` and `com.europesip.dx.pages`.

`bundle.json`:

| Field | What |
|---|---|
| `schema`, `kind` | `dx-bundle/v1`, `dx-bundle` |
| `resource`, `created` | The DXExport (namespace/name), when (RFC 3339, UTC) |
| `source` | `server`, `version` (as DX reports it), `connection`, `virtualPortal` |
| `requestedBy`, `reason` | From the annotations `dx.europesip.com/changed-by` and `dx.europesip.com/reason` |
| `tools` | `operator` (with its version), `createdWith` (Backstage and its plugin, annotation `dx.europesip.com/created-with`) |
| `attributes` | `spec.destination.attributes` |
| `parts` | `pages`, `portal-site`, `libraries` |
| `pages`, `libraries` | Pages with their content nodes; libraries with files and bytes |
| `references` | Themes, skins and portlets the pages locate in the target |
| `warnings` | What the target must have or what will not work there |
| `import` | Ordered steps: `step`, `part`, `file`, `compose`, `traditional` (the command) |
| `files` | `path`, `mediaType`, `bytes`, `sha256` of every content file |

## How each part is made

**Pages** (`parts.pages`): one XMLAccess export against `/wps/config[/<vp>]` with
`<content-node action="export" uniquename="…" export-descendants="true|false"/>` per page. DX answers an update
request with the pages, and the themes, skins and portlets they use **located by unique name**. The operator turns it
into a request for **another** portal:

- `type="update" create-oids="true"`: the object IDs of the source become placeholders and DX gives new ones;
- without the `<status>` of the export, the `build`/`version` attributes and the comments (they name the pod and IP);
- without the `owner` of the access control: users of the source registry need not exist in the target (the
  administrator who imports owns them);
- everything else as DX wrote it: titles, parameters, layout, portlet instances and preferences, access, friendly
  names and the WCM content mapping of managed pages.

It also lists what the target must have (themes, skins, portlets) and warns about parents without a unique name (they
do not exist in another portal) and about managed pages without `parts.portalSite`.

**Portal Site and libraries** (`parts.portalSite`, `parts.libraries`): the WCM data module exports each one into one
dxFileTransfer folder, downloaded as one zip, as [DXWCMLibraryExport](how-it-works.md#wcm-library-exports-dxwcmlibraryexport-070) does.

## Importing a bundle

Order: **pages first, then the WCM zip** (Portal Site after the pages, so the managed pages find their content). The
target must already have the themes, skins and portlets listed in `EXPORT-INFO.txt`. Same CF level as the source or
newer.

### Into DX Compose

1. `DXXMLAccess` with `source.image.ref: <bundle>`, `files: [xmlaccess/01-pages.xml]`, `dryRun: true` first, then
   without it (Backstage: *Run XMLAccess*).
2. `DXWCMLibraryImport` with `source.image.ref: <bundle>` (the operator finds the zip and unpacks it), with
   `portalSiteLibraryOverwrite: true` when Portal Site is in the bundle; dry run first (Backstage: *Import WCM
   libraries*).

### Into a traditional DX on WebSphere

`oras pull <bundle>` and copy the files to the DX server, then:

1. Pages:
   ```
   <wp_profile>/PortalServer/bin/xmlaccess.sh -in xmlaccess/01-pages.xml -user <admin> -password <pwd> \
     -url https://<host>:<port>/wps/config[/<virtual portal>] -out result.xml
   ```
2. WCM libraries and Portal Site: unzip `wcm/wcm-export-<name>.zip` into a folder, then
   ```
   <wp_profile>/ConfigEngine/ConfigEngine.sh import-wcm-data -Dimport.directory=<folder> \
     [-DVirtualPortalContext=<context>] -DWasPassword=<pwd> -DPortalAdminPwd=<pwd>
   ```
   It imports every library of the folder (there is no library name property). HCL then asks to reset the web
   content event log and restart the server. Or, signed in, the data module:
   `/wps/wcm/myconnect[/<vp>]?MOD=data&processLibraries=false&taskType=import&input.dir=<folder>`.

`EXPORT-INFO.txt` of each bundle has these commands with its own file names and virtual portal.

## Not in a bundle (yet)

- **Themes** (registration and WebDAV files): a later version (`parts.theme`). Until then the target needs the theme.
- **Portlets of your own** (JSR 286): do not run on DX Compose without the Java Transition Module; the pages keep
  their place.
- **Personalization (PZN)** rules, **DAM**, virtual portal definitions, URL mappings: not yet.
- The traditional import is written from the HCL documentation; it is being validated on DX 9.5 CF230 on WebSphere.
