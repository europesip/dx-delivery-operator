# DX Core (traditional DX 9.5 on WebSphere)

**English** · [Español](dx-core.es.md)

Since issue [#44](https://github.com/europesip/dx-delivery-operator/issues/44) the operator also works with
**DX Core**, the traditional HCL DX 9.5 server on WebSphere Application Server: the `core` pod of the HCL chart
(`applications.core`) and a traditional DX installed on a VM. DX Compose (the Web Engine) stays the default.

## What changes

| | DX Compose (Web Engine) | DX Core |
|---|---|---|
| `DXConnection.spec.runtime` | `compose` (default) | `core` |
| Context root | `/wps` | `/wps` in the HCL chart; a VM may have an **empty** one: set `contentHandlerPath: /mycontenthandler` (XMLAccess then goes to `/config`, the login to `/j_security_check`) |
| Login | Ring API, else the portal form | the portal form (a VM has no Ring API); XMLAccess and WebDAV use basic authentication |
| WCM library export / import | WCM data module + `dxFileTransfer` | **DXConnect** (the servlet of the Config Wizard profile that DXClient uses): `dxFileTransfer` does not exist on Core (404) |
| `DXWCMLibraryImport` `mode: ImportCopy` | yes | no: DXConnect runs `import-wcm-data`, which keeps the IDs |
| XMLAccess (pages, themes, virtual portals) | `/wps/config` | the same, at `<context root>/config` |
| Themes (WebDAV) | `…/mycontenthandler/dav/fs-type1/themes/` | the same |

## DXConnection for DX Core

```yaml
apiVersion: dx.europesip.com/v1alpha1
kind: DXConnection
metadata: {name: dx}
spec:
  url: https://dx-core.example.com
  internalURL: http://dx-deployment-core.<namespace>.svc:10039   # the core pod, without HAProxy
  runtime: core
  portalCredentialsSecret: {name: dx-core-wps}            # wpsadmin
  dxConnect:                                         # optional: defaults below
    url: https://dx-core.example.com                 # default spec.url (HAProxy routes /hcl/dxconnect)
    cwCredentialsSecret: {name: dx-core-configwizard} # Config Wizard administrator (default: portalCredentialsSecret)
    wasCredentialsSecret: {name: dx-core-was}        # WebSphere administrator (default: portalCredentialsSecret)
    profileName: wp_profile
```

For a traditional DX on a VM with an empty context root:

```yaml
spec:
  url: https://dx-vm.example.com:10042
  runtime: core
  contentHandlerPath: /mycontenthandler
  dxConnect: {url: "https://dx-vm.example.com:10202"}   # only if the Config Wizard server (cw_profile) runs
```

## WCM libraries through DXConnect

The calls are the ones of DXClient 239 `wcm-library-export` / `wcm-library-import`
(`/hcl/dxconnect/processHandler?requestMethod=exportWCMLibrary|download|upload|importWCMLibrary`, with the headers
`Authorization` (Config Wizard), `DXAuth` (DX) and `DXWASAuth` (WebSphere)). DXConnect runs ConfigEngine
`export-wcm-data` / `import-wcm-data` on the server.

- **Export**: DXConnect returns a tar (named `.tar.gz`, not compressed) with one folder per library
  (`WCM_Library-<time>/<library>/<JCR export>`). The operator merges them into its usual zip: one JCR export at the
  root, the same as the WCM data module of DX Compose. So an export of DX Core imports into DX Compose and the other way
  round.
- **Import**: the operator packs each chunk as DXConnect expects it (the same tar, one folder per library). A flat zip
  is rejected by DXConnect (`…/wcm/ilwwcm/system/import (No such file or directory)`, measured).
- **On a VM** DXConnect runs in the Config Wizard server (`cw_profile`, port 10202). If it is stopped, the portal answers
  with an HTML page and the operator says so; export the libraries with ConfigEngine on the server instead.

## Themes and sites between portals (DXExport `parts.themes`)

`DXExport` exports themes as well: for each unique name, its **registration with its own skins** (XMLAccess,
`themes/theme-<name>.xml`) and its **WebDAV folder** (`themes/theme-<name>-webdav.zip`). The skins of HCL (`ibm.*`,
`hcl.*`, `wp.*`) are left out: they are in every DX. The XML is cleaned for the import (measured CF230 → CF239):
empty-locale `localedata` (NullPointerException on import) and the repeated `<skin action="locate">` (EJPXA0140E
duplicate objectid) are removed.

Cloned portlets (`<portlet>.$cloned.<id>`, for example a Web Content Viewer copied with its own settings) do not exist
in another portal: the export now puts their definitions into `xmlaccess/01-pages.xml`, so the pages import.

Import, in this order:

1. `DXTheme` with `registration: FilesOnly`, `base.copyFiles: false` and the bundle as source: only the WebDAV files
   (it finds `themes/theme-<name>-webdav.zip` in the bundle and unpacks it; files the target rejects with HTTP 400 are
   reported as an event).
2. `DXXMLAccess` with `files: [themes/theme-<name>.xml, xmlaccess/01-pages.xml]`, `objectIDs: Keep`.

See sample `samples/15-dx-core`.

## Measured (22-nov-2026)

From a traditional DX 9.5 CF230 on a VM (read-only), to DX Core CF239 in Kubernetes (Oracle 21c XE) and to DX Compose
CF239: the EuropeSIP site (41 pages, theme with 467 WebDAV files, a cloned Web Content Viewer) imported in both. WCM:
Portal Site from DX Core to a new virtual portal of DX Compose (dxFileTransfer), and Web Resources v70 from DX Compose
to a new virtual portal of DX Core (DXConnect).
