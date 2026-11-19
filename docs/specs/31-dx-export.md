# DXExport: portable DX bundles (pages, Portal Site, WCM libraries)

Issue: #31 · Status: Implemented · Release: 0.9.0 (unreleased)

## Summary
F1 of the export proposal (notas/operador/PROPUESTA-EXPORTACIONES.md of dx-deployment, decided 19-nov-2026): one
generic resource, `DXExport`, that exports parts of a DX Compose portal into one OCI artifact whose files are all in
the native formats of DX, so the same artifact imports into another DX Compose and into a traditional DX on WebSphere
with its own tools. Themes (F2), a site export and one-step bundle import (F3) come later on the same format.

## Specification
- Spec: `connectionRef`, `virtualPortal` (context string, empty = base portal), `parts` {`pages`: [{`uniqueName`,
  `descendants` (default true)}], `portalSite` (bool), `libraries` ([]title)} with at least one part,
  `destination` {`image`, `pushSecrets`, `attributes`} (the type of DXWCMLibraryExport), `timeout` (each request to
  DX, default 1h), `dependsOn`, `suspend`.
- Libraries (and Portal Site) must exist first (`LibraryNotFound`, nothing exported). Pages: one XMLAccess export
  request with one `content-node action="export"` per page; a page missing (EJPXA0240E, or not in the answer) fails
  with `PageNotFound`.
- The answer is turned into a request for another portal: `type="update" create-oids="true"`; `<status>`, `build`,
  `version`, XML comments and `owner` of access-control removed; all else kept. Inventory: content nodes per page,
  themes/skins/portlets located, parents without unique name, managed pages (content-mapping-info).
- WCM: shared with DXWCMLibraryExport (internal/controller/wcmexport_shared.go): one dxFileTransfer folder
  (`dxop-bundle-<ns>-<name>`), the data module per library, one zip, checked.
- Artifact (ORAS file artifact, empty config, no artifactType): `EXPORT-INFO.txt`, `bundle.json` (`dx-bundle/v1`),
  `xmlaccess/01-pages.xml`, `wcm/wcm-export-<name>.zip`; layer titles keep the folders. Annotations: the 0.8.0
  convention with kind `dx-bundle`, `com.europesip.dx.bundle.schema`, `.bundle.parts`, `.pages`.
- Import order in both worlds: pages, then the WCM zip (Portal Site after the pages).
- Status: `phase`, `message`, `currentStep`, `pages` [{uniqueName, nodes}], `pagesExported`, `exportedLibraries`,
  `files` [{path, bytes, sha256}], `warnings`, `totalBytes`, `image`, `digest`, times, `appliedHash`, conditions.

## Checked
- Unit tests: the request, the transformation and inventory (fixture from demorbita CF239, and the 2.5 MB export of
  its whole content tree: 224 nodes), validation, import steps and EXPORT-INFO commands, annotations, and a bundle in
  an in-memory registry read back by the DXXMLAccess and DXWCMLibraryImport readers.
- e2e (build tag e2e) against demorbita: orbita-site + orbita + Portal Site → 1.4 MB bundle; then, on the operator in
  the cluster, a dry-run DXXMLAccess (files [xmlaccess/01-pages.xml]) and a dry-run DXWCMLibraryImport read it.
- Not yet: the import into a traditional DX (to be done on DX 9.5 CF230 on WebSphere).
