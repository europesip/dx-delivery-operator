# How the operator works

[Español](how-it-works.es.md) · [README](https://github.com/europesip/hcl-dx-scriptapp-operator#readme)

## The pieces

```
                          Kubernetes                                         HCL DX
 ┌───────────────────────────────────────────────────────┐        ┌──────────────────────────────┐
 │ DXConnection ──┐                                      │        │                              │
 │ DXScriptApplication ─┐  ┌──────────────────────────┐   │ push   │ WCM: Script Application      │
 │ DXPage ────────────┼─►│ operator (one pod)       │───┼───────►│      Library (DXClient)      │
 │ DXDamCollection ─────┤  │  · controllers           │   │ XML    │ Pages (XMLAccess /wps/config)│
 │ DXDatabase ──┘  │  · DXClient (/dxclient)  │───┼───────►│ DAM (REST /dx/api/dam/v1)    │
 │                       │  · source cache          │   │ check  │ Ring API (/dx/api/core/v1)   │
 │ Deployments, Jobs ───►│  (dependsOn)             │───┼───────►│                              │
 │                       └──────────┬───────────────┘   │        └──────────────────────────────┘
 │                                  │ SQL                │
 │                                  ▼                    │
 │                         PostgreSQL (existing)         │
 └───────────────────────────────────────────────────────┘
```

- **One image**: HCL's public DXClient image plus the operator binary. DXClient runs inside the operator for
  pushes; no Job or extra image is created.
- **Sources** are read by the operator itself: OCI images straight from the registry (their layers, with
  `go-containerregistry`; nothing runs), ConfigMaps, or archives over HTTP(S). They are cached by revision
  (image digest, ConfigMap hash, archive SHA256).
- **DX APIs**: Ring API (sign-in, WCM content by ID and by name), WCM REST v2 (delete), XMLAccess endpoint
  `/wps/config` (pages), DAM REST API (collections, assets, renditions), Ring access API (DAM permissions).

## The reconciliation loop

Every resource is reconciled when it changes, when an object it refers to changes (its ConfigMap, the DAM
collection of a substitution, the Deployment it depends on, its connection...), and every `resyncInterval` of
its DXConnection (default 10 minutes). Each pass:

1. **Deletion** (if the resource is being deleted): with `deletionPolicy: Delete`, remove the object from DX;
   then release the finalizer. `Retain` (the default) leaves DX as it is.
2. **Suspend**: `spec.suspend: true` stops here.
3. **Dependencies**: every `dependsOn`, plus the implicit ones (the DAM collections of substitutions, the
   Script Applications of a page). If one is not ready: `Ready=False, reason Waiting`, retry in 20 seconds.
4. **Connection**: the DXConnection must be Ready.
5. **Desired state**: what the resource declares, as a hash.
6. **Actual state** in DX, and the decision: create, update, restore, report, or nothing.
7. **Status and events**: conditions `Ready`, `Drifted` and `DependenciesReady`, plus Kubernetes events.

Writes to the status are merge patches, so concurrent passes do not lose what was done in DX.

### DXScriptApplication

- **Prepare**: get the source; copy it to a working folder; replace the `${NAME}` placeholders of
  `substitutions` in the text files (`.html .htm .js .mjs .css .json .txt .svg .xml`; other `${...}` are left
  alone); check that the main HTML file exists and that no file is over 4.5 MB.
- **Hash** of every prepared file plus the WCM settings (name, title, site area, main file, project) and the
  force annotation.
- **Decide**:
  - no content ID yet → push (`Created`);
  - hash changed → push (`Updated`);
  - otherwise read the content in DX by ID (Ring API): 404 → it was **deleted** in DX; a different `updated`
    date than the one recorded after the last push → it was **modified** in DX. With `driftPolicy: Restore` push
    again (`Restored`; after a deletion WCM creates a new item, with a new content ID); with `Report` mark
    `Drifted` and do nothing.
- **Push**: `dxclient deploy-scriptapplication push`, with the connection in DXClient's configuration file
  (removed afterwards). The push is accepted only if DXClient exits 0 **and** DX's answer has
  `"status":"success"` (DXClient prints "successful" even when DX rejects the content). Temporary errors of a DX
  that is starting (Service Unavailable, Bad Gateway, connection reset...) are retried 5 times, 20 s apart.
- **Record** the content ID, the hash, the source revision, the `updated` date, the size and a history of the
  last 10 pushes.
- **Mode Verify** never pushes: it finds the content by its WCM name and reports Ready or `NotFound`.
- **Delete** (policy Delete): WCM REST v2 `DELETE /wcmrest-v2/contents/{id}` (DXClient's
  `undeploy-scriptapplication` refuses while a deleted page still references the application: DX removes
  deleted pages later, with its cleanup task).

### DXPage

- **Desired**: the content IDs of its Script Applications (in order), titles, parent, friendly name, layout
  template, skin, theme, access and position, as a hash. A second hash covers only the list of applications
  (the layout).
- **Actual**: an XMLAccess export by unique name. `EJPXA0240E` means the page does not exist; any other error
  is an error (a temporary failure must not be taken for a deleted page, or the page would be created twice).
  The hash applied by the operator is stored in the page itself, as the parameter `dxop.hash`.
- **Decide**: missing and never created → create with its layout; missing after being created → deleted in DX
  (restore or report); list of applications changed → delete and create again (XMLAccess cannot replace the
  components of a page in place); other settings changed → update titles, parameters and access without
  touching the layout (so the settings people make on the page survive); `dxop.hash` in DX different from the
  one applied → someone changed the settings in DX (restore or report).
- A Script Application that gets a new content ID (restored after a deletion) triggers its pages: the layout
  hash changes and the page is rebuilt.

### DXTheme

- **Desired**: the spec (folder, unique name, titles, base, profile, layout, head HTML) and the revision of its
  source, as a hash.
- **Actual**: an XMLAccess export of the theme by unique name (the applied hash is kept in the theme as the parameter
  `dxop.hash`) and a WebDAV listing of its folder (`dav:fs-type1/themes/<folder>/`).
- **Apply**: with `base.copyFiles: false` (a theme of its own) only the files of the source; otherwise download every
  file of the base theme over WebDAV first and put the files of the source on top (same paths), then
  insert the head HTML before `</head>` in `theme.html`, `theme_sidenav.html` and their language versions, upload
  the folder (MKCOL + PUT), and register the theme with a copy of the registration of the base theme (new unique
  name, templates pointing at the new folder, `default="false"`, only the declared titles, and `profile` / `layout`
  when they are set).
- **Drift**: registration or folder missing in DX → upload and register again (`Restore`) or report.
- **Delete** (policy Delete): delete the registration (XMLAccess) and the folder (WebDAV). Pages that use it fall
  back to the default theme.
- WebDAV and XMLAccess use basic auth, so the theme works the same with DX behind OpenID Connect.
- DXClient `deploy-theme` is not used: its registration step fails on DX Compose CF239.

### DXDamCollection

- Find the collection by the ID in the status, or by name (so an existing one is adopted), or create it; give
  the User role to anonymous and/or all authenticated users.
- For each file of the source (assets named after their path, `/` → `-`): unchanged (same SHA256 as recorded) →
  nothing; changed → `PUT` a new version of the same asset (same ID and URL); missing in DX → upload again
  (restore or report); an asset with that name already there → adopt it. Then wait for its original rendition
  and record its URL.
- `prune: true` deletes the assets that are no longer in the source.
- Delete (policy Delete): the collection, ignoring media references.

### DXDatabase

- A Secret `<name>-db` (or `secretName`) with the connection details; the password is generated once and kept,
  and the Secret is owned by the resource.
- With the admin Secret, by engine (checked every 30 minutes):
  - **PostgreSQL**: create or alter the role (login, password), create the database owned by it if missing, create
    the extensions.
  - **Oracle** (`oracle.service`, the PDB): the database is a schema: create the user (default tablespace and quota)
    with the privileges to create tables, views, sequences, procedures, triggers, types and synonyms; an existing user
    gets the password again.
  - **SQL Server**: create or alter the login, create the database if missing, its user for the login and the
    `db_owner` membership.
- Delete (policy Delete): PostgreSQL `DROP DATABASE ... WITH (FORCE)` and `DROP ROLE`; Oracle `DROP USER ... CASCADE`;
  SQL Server `DROP DATABASE` (single user first) and `DROP LOGIN`.
- The Secret has the same keys for every engine: `engine`, `username`, `password`, `host`, `port`, `database`,
  `uri` and `jdbcUrl` (plus `service` with Oracle).

### DXConnection

Signs in to DX with a fresh session (`/dx/api/core/v1/auth/login`) and checks that DXClient can run (licence
accepted, files present). Changes in its Secrets trigger a new check.

## Concurrency and performance

- One active replica (leader election). DXClient runs are **serialised** (one at a time): DXClient keeps its
  settings in its own folder, and DX does not like parallel pushes either. REST calls run in parallel.
- A push takes a few seconds; a page about one second; a check (resync) is one or two REST calls per resource.
- REST sessions are reused per connection while its Secrets do not change.

## Forcing and pausing

- `metadata.annotations["dx.europesip.com/reconcile"]`: change its value to reconcile now; for a Script
  Application it also forces a push (the value is part of the hash).
- `spec.suspend: true`: nothing is done until it is false again.

## Status and events

```bash
kubectl get dxscriptapplication time-off -o yaml        # status.contentId, history, conditions
kubectl get events --field-selector involvedObject.name=time-off
```

Events: `Pushed` (Created / Updated / Restored), `Drifted`, `PushFailed`, `Created`, `Rebuilt`, `Synced`,
`Deleted`, `Undeployed`, `Dropped`, `Connected`, `LoginFailed`...

## WCM library imports (DXWCMLibraryImport, 0.4.0)

DX Compose has no ConfigEngine, and DXClient `wcm-library-import` relies on it. The operator uses what DX Compose
offers over HTTP:

1. **Virtual portal**: listed (and created if `create: true`) with XMLAccess and the `PortalVPHandler.xsd` schema. A
   missing portal ends the run before anything is downloaded.
2. **Export**: fetched like any source (URL streamed to disk, OCI image, or an ORAS file artifact); an archive inside
   the source is unpacked. Libraries are the `<library>.node` files of the export, titled by `icm:title`.
3. **Selection**: include, exclude, `excludeHCLDefaults`; libraries that already exist in the target (WCM REST
   `query?type=Library`) are skipped unless `overwrite`; **Portal Site** (fixed ID `TWFuYWdlZCBQYWdlcyBSb290IExpYnJhcnk`)
   only with `portalSiteLibraryOverwrite`. Libraries with file names dxFileTransfer rejects are reported, not uploaded.
   Since 0.6.0 `status.exportLibraries` lists every library of the export with its description (`icm:description`),
   size, whether it exists in the target and its plan (`Import`, `Skip`, `CannotUpload`, `NotSelected`): a dry run is
   the catalogue a UI offers to choose from, and the real run imports the chosen titles (`libraries.include`).
4. **Chunks** of at most `upload.maxChunkSize` (450Mi) and `upload.maxChunkFiles` (4000): each one is uploaded with
   `dxFileTransfer` (`unzip=true`), imported with the WCM data module
   (`/wps/wcm/myconnect[/<vp>]?MOD=data&taskType=import|import-copy&input.dir=...`) and its folder deleted. If a proxy
   cuts the request (502/503/504), the folder is kept: DX goes on importing.
   **Resume after a restart (0.9.1)**: the chunk plan (titles of each chunk) and the progress are kept in
   `status.resume` while the run goes on. If the operator restarts (an upgrade, a node), the next run with the same
   spec and export goes on at the chunk it was on: finished chunks are not imported again, and their libraries count
   as planned even if they now exist in the target. If the import request of the interrupted chunk had been sent, DX
   may still be importing it, so the operator waits 1.5 times the longest chunk of the run (3 to 30 minutes; 5 without
   a finished chunk), counted from when it was sent, and imports it again into a **new folder**: the folder names carry
   the attempt (`dxop-<ns>-<name>-<chunk>-<attempt>`), and the folders of the earlier attempt are deleted only when the
   run ends. An import **as a copy** (`mode: ImportCopy`) does not import the interrupted chunk again (it would
   duplicate what DX copied): its libraries are reported as failed with the reason. A changed spec or export, or a
   dry run, starts from scratch.
5. **Check and roles**: the libraries are listed again; `access` roles are added (WCM access control REST).

The run is **one-shot**: the status keeps a hash of the spec, and the import runs again only when the spec or the
`dx.europesip.com/reconcile` annotation changes. Imports to the same DX never overlap with its XMLAccess calls.

## WCM library exports (DXWCMLibraryExport, 0.7.0)

The other way round: chosen libraries of a DX Compose portal go to an **OCI artifact** in a registry (Harbor), to be
imported into another DX Compose or into a **traditional HCL DX on WebSphere**. Everything is HTTP, as for imports:

1. **Check**: every title of `libraries` must be a library of the portal (WCM REST); otherwise `LibraryNotFound` and
   nothing is exported.
2. **Export**: one dxFileTransfer folder; for each library the WCM data module
   (`/wps/wcm/myconnect[/<vp>]?MOD=data&processLibraries=false&taskType=export&exportLibrary=<title>&output.dir=<folder>`,
   answer `The library "<title>" was exported successfully.`). The libraries merge into one JCR tree.
3. **Download**: the folder as one zip (`dft?action=download&file=`), streamed to the work volume, unpacked and
   checked (every library must be in it). The folder in DX is removed.
4. **Push**: an ORAS file artifact (empty config, no artifactType): `wcm-export-<name>.zip`, **exactly as DX writes
   it** (the same JCR folder format as ConfigEngine `export-wcm-data`), and `EXPORT-INFO.txt`: source server, DX
   product and build, virtual portal, date and time (UTC), libraries with their size, and how to import it.

**Importing the artifact**

- **DX Compose**: a `DXWCMLibraryImport` with `source.image.ref` = the artifact (the operator unpacks the zip).
- **Traditional DX** (8.5, 9.0, 9.5 on WebSphere): `oras pull` the artifact, unzip it on the DX server (for example
  into `/tmp/wcm-import`) and run `./ConfigEngine.sh import-wcm-data -Dimport.directory=/tmp/wcm-import
  -DWasPassword=... -DPortalAdminPwd=...` (`-DVirtualPortalContext=<context>` for a virtual portal).
  `import-wcm-data` has no library name property: it imports every library of the folder. Or, signed in, the data
  module URL with `taskType=import&input.dir=/tmp/wcm-import`. Do not rename folders or files of the export.
- The target should be at the **same CF level as the source or newer**: `EXPORT-INFO.txt` names the source build.

One-shot per spec, like the import; deleting the resource stops a running export within 10 s and keeps the artifact.

## Portable bundles (DXExport, 0.9.0)

`DXExport` puts pages, the Portal Site library and WCM libraries of a portal into one OCI artifact, a **DX bundle**
(`dx-bundle/v1`), every file in the native format of DX, so it imports into another DX Compose or into a traditional
DX on WebSphere (see [Portable bundles](portable-bundles.md)):

1. **Check**: the parts (at least one), the library titles (with Portal Site when `parts.portalSite`) must exist.
2. **Pages**: one XMLAccess export of every page (`export-descendants` per page) against `/wps/config[/<vp>]`, turned
   into an update request that keeps the object IDs of the source (0.9.1), without status, build, comments and owners:
   `xmlaccess/01-pages.xml`. Kept or new IDs are chosen when importing (`DXXMLAccess` `objectIDs`). A page that does not exist fails the run (`PageNotFound`); the themes, skins and portlets
   the pages need, parents without a unique name and managed pages without Portal Site become `status.warnings`.
3. **WCM**: the libraries and Portal Site with the data module into one zip (as DXWCMLibraryExport):
   `wcm/wcm-export-<name>.zip`.
4. **Push**: `EXPORT-INFO.txt` (with the import commands for DX Compose and for `xmlaccess.sh` / `ConfigEngine.sh`),
   `bundle.json` (ordered import steps, size and sha256 of every file) and the files above, with the attributes of the
   artifact (`com.europesip.dx.kind: dx-bundle`).

Status: `phase`, `currentStep`, `pages` (content nodes of each), `pagesExported`, `exportedLibraries`, `files`,
`warnings`, `totalBytes`, `image` (repository@digest). One-shot per spec; deleting it stops a running export.

## XMLAccess requests (DXXMLAccess, 0.5.0)

`DXXMLAccess` runs XMLAccess requests of your own: the way to do anything the typed resources do not cover yet (URL
mappings, policies, credential vault slots, WSRP, tags, exports...). It works **like a Job**, not like `DXPage`:

1. **Requests**: `spec.xml` (one, inline) or the `.xml` files of a source (URL archive, OCI image or ORAS artifact,
   ConfigMap), in the order of `files` or sorted by path. `${NAME}` is replaced with `variables`. `objectIDs`
   (0.9.1): `Keep` (default) sends them as they are, so the object IDs in the XML are kept; `New` sets
   `create-oids="true"` on every `<request>` (once), so DX gives new IDs to what it creates, like a WCM import with
   `mode: ImportCopy`. The cleanup request is sent as written.
2. **Check first**: every file must be well-formed XML with `<request type="...">`. If one is not, **nothing** is
   sent. `dryRun: true` stops here and lists the requests.
3. **Run**: each request is posted to `/wps/config` (or `/wps/config/<context>` with `virtualPortal`), one at a time
   per DX (shared lock with the other XMLAccess calls of the operator), with `timeout` (10m). The first failure stops
   the run unless `continueOnError`; later requests are `skipped`.
4. **Result**: `status.results` (file, request type, `ok` / `error` / `skipped`, the first DX message) and, with
   `output.configMap`, DX's answer to every request in a ConfigMap owned by the resource (at most 900 KiB in all: bigger answers are never cut, the run fails with `OutputTooLarge` unless `output.oci` is set too);
   with `output.oci`, the answers and a `results.json` pushed to a registry (Harbor) as an OCI artifact, one file
   per request (the way `oras push` does), with a new tag every run (`${DATE}`): versioned, no size limit, and
   readable as the `source` of another resource, for example to apply an export in another environment.

It runs **once per change**: the hash of the spec, the source revision (image digest, archive SHA256, ConfigMap) and the
`dx.europesip.com/reconcile` annotation. The operator does not know what an arbitrary request created, so it does not
look for drift. Deleting the resource changes nothing in DX, unless `cleanup` gives a request (inline or a file of the
source) to run then; a failed cleanup is retried for 10 minutes and then reported as an event, and the resource goes.
Prefer idempotent requests (`action="update"` / `"locate"`), so that running them again is harmless.

