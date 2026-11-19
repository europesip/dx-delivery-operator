# DXWCMLibraryExport: export WCM libraries to an OCI artifact

Issue: #27 · Status: Implemented · Release: 0.7.0

## Summary
Export chosen WCM libraries of a DX Compose portal (base or virtual portal) to an OCI artifact, so that they can be
imported into another DX Compose (`DXWCMLibraryImport`) or into a traditional HCL DX on WebSphere (ConfigEngine
`import-wcm-data`). The Backstage plugin offers it as "Export libraries".

## Current behaviour
Only imports exist (`DXWCMLibraryImport`, 0.4.0 to 0.6.2): an export has to be made by hand (ConfigEngine on a
traditional portal, or the data module URL and dxFileTransfer on DX Compose) and pushed with `oras push`.

## Specification
- Spec: `connectionRef`, `virtualPortal` (context, empty = base portal), `libraries` (titles, at least one; repeated
  titles, ignoring case, count once), `destination.image`, `destination.pushSecrets`, `timeout` (each library,
  default 1h), `dependsOn`, `suspend`.
- Every title must be a library of the portal (WCM REST `query?type=Library`, ignoring case); otherwise the run fails
  with `LibraryNotFound` and nothing is exported.
- One dxFileTransfer folder per resource (`dxop-export-<ns>-<name>`), removed at the end of every run.
- Each library: `GET /wps/wcm/myconnect[/<vp>]?MOD=data&processLibraries=false&taskType=export&exportLibrary=<title>&output.dir=<folder>`
  from a signed-in session, under the XMLAccess lock of the DX. Success: `The library "<title>" was exported
  successfully.` (checked on CF239). The exports of several libraries merge into one JCR tree.
- The folder is downloaded as one zip (`dft?action=download&subDirectory=<folder>&file=`), streamed to disk, unpacked
  and read: every library asked for must be in it.
- Pushed as an ORAS file artifact: OCI image manifest, empty config (`application/vnd.oci.empty.v1+json`), **no
  artifactType**, layers `wcm-export-<name>.zip` (`application/zip`) and `EXPORT-INFO.txt` (`text/plain`) named by
  `org.opencontainers.image.title`; annotations created, description, source.
- `EXPORT-INFO.txt`: source server, DX product and build (header of an XMLAccess answer), connection, virtual portal,
  date and time (UTC), resource, number of libraries, each one with files and bytes, zip size, and how to import it
  into DX Compose and into traditional DX.
- Status: `phase` (Pending, Exporting, Downloading, Pushing, Succeeded, Failed), `message`, `currentLibrary`, `image`
  (repository@digest), `digest`, `exportedLibraries[]` (title, bytes, files), `totalBytes`, `startTime`,
  `completionTime`, `conditions`.
- One-shot per spec hash; deleting the resource stops a running export within 10 s; the artifact is never deleted.
- Registry 401/403 ends the run (`PushFailed`); other errors are retried every minute.

## Acceptance criteria
- [x] The artifact is a source that `DXWCMLibraryImport` reads (unit test with an in-memory registry).
- [x] e2e against demorbita (CF239): orbita + Social Lists 1.0 exported (1.8 MB), pushed to Harbor, read back by the
  in-cluster operator 0.6.2 with a dry-run import.
- [x] A missing library: `LibraryNotFound` before anything is exported.

## Out of scope
- Exporting every library at once without listing them (the UI lists them).
- Libraries larger than the disk of the operator's work volume (8Gi by default).

## Test plan
Unit (`internal/dx`, `internal/controller`), e2e test with the build tag `e2e` (`dxwcmlibraryexport_e2e_test.go`).
