# DX Core (traditional DX 9.5 on WebSphere)

Issue: #44 · Status: Implemented · Release: 0.11.0

## Summary
Andrés (22-nov) wants the operator to work against DX Core (the `core` pod of the HCL chart, and traditional DX on a
VM) for imports and exports of libraries, pages, themes and virtual portals, with a test environment (dx-basic-core on
Oracle 21c XE, Sergio's CF230 VM read-only).

## Current behaviour (0.10.1)
`/wps` hard-coded for XMLAccess and page URLs; WCM import/export only through `dxFileTransfer` + the WCM data module
(404 on Core); no theme export; cloned portlets break page imports into another portal.

## Specification
1. `DXConnection.spec.runtime` compose|core (default compose); `spec.dxConnect` {url, cwCredentialsSecret,
   wasCredentialsSecret, profileName} with defaults (url, credentials of the connection, wp_profile).
2. Context root = `contentHandlerPath` without `/mycontenthandler` for XMLAccess, form login, page URLs.
3. On core, `DXWCMLibraryExport`/`DXExport` libraries and `DXWCMLibraryImport` use DXConnect
   (exportWCMLibrary + download; upload + importWCMLibrary). Export tar → flat zip; import zip → per-library tar.
   ImportCopy refused on core. An HTML answer = DXConnect not running → clear permanent error.
4. `DXExport.parts.themes`: per theme, XMLAccess registration + own skins (built-in ibm./hcl./wp. skipped, cleaned) and the
   WebDAV folder zipped. Cloned portlets of exported pages inlined into the pages request.
5. `DXTheme.registration: FilesOnly`: files only (bundle `-webdav.zip` unpacked), HTTP 400 rejects reported, Ready.
6. XMLAccess: the caller's deadline instead of the 90 s client timeout; through internalURL when set.

## Acceptance criteria
- [x] Sergio CF230 (empty context root) → DXConnection Ready, DXExport of 4 sites with themes (read-only).
- [x] Site (41 pages, theme 467 files, cloned portlet) imported into DX Core k8s and DX Compose.
- [x] WCM Core → Compose (Portal Site into a new VP) and Compose → Core (Web Resources v70 into a new VP via DXConnect).
- [ ] WCM export from the CF230 VM: needs the Config Wizard server (cw_profile, 10202) started (decision for Andrés).
- [ ] Release + Backstage cards for runtime core.

## Test plan
Unit tests TestDXConnectExportAndImport, TestDXConnectSourcePath, TestThemeFolderOfAndInnerPortal, TestCleanThemeXML,
TestInlineClonedPortlets, TestContextRootEmptyOnVM; real runs with the isolated test operator (API group
dxtest.europesip.com, namespace dxop-test) on 22-nov.
