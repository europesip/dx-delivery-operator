# Release history

**English** · [Español](CHANGELOG.es.md)

To go back to an earlier release: `helm upgrade dxop oci://ghcr.io/europesip/charts/dx-delivery-operator --version <version> --reuse-values`
for 0.13.0 and later, `oci://ghcr.io/europesip/charts/hcl-dx-scriptapp-operator` for 0.12.x and earlier
(0.3.0 renamed the resources: see its notes).

## 0.14.1 · 2026-11-23 · DXJavaExtension: no longer stuck in RollingOut

**Fixed** (#57): after a version change a `DXJavaExtension` could stay `RollingOut` although the Web Engine pod was
already Ready and serving the new version, until something else reconciled it.
- The controller now **watches the Web Engine pods and their StatefulSet** (only the ones the webhook changed), so a
  pod that becomes Ready (or whose init container fails) reconciles its extensions at once.
- While rolling out it still polls every **10 s** (was 15 s), and every reconcile is bounded (4 min), so a hung
  registry or DX call can no longer block the controller.
- New chart value **`javaExtensions.rolloutTimeout`** (Go duration, default 30m): the default time a rollout may take
  before the operator rolls back; `spec.rolloutTimeout` still overrides it.

Upgrading from 0.14.0: apply the CRDs (unchanged) and `helm upgrade` with your values file (`--reuse-values` does
not pick up new chart values).

## 0.14.0 · 2026-11-23 · Java extensions (DXJavaExtension)

New resource **`DXJavaExtension`** (#54): custom Java on DX Compose **without a custom image**. A JSR 168/286
**portlet** (WAR) or **shared-library JARs** from an OCI artifact are loaded onto the **official** Web Engine image.
See [Java extensions](docs/java-extensions.md).

- **Admission webhook** (chart value `javaExtensions.enabled`, off by default; certificate by cert-manager): when a
  Web Engine pod is created in a namespace labelled `dx.europesip.com/java-extensions=enabled`, it adds an init
  container, `emptyDir` volumes and mounts on `resources/customApplications/`, `libraries/customSharedLib/` and
  `configDropins/defaults/`. It never changes the HCL chart, the Helm release or the image.
- **Init container image** `ghcr.io/europesip/dx-delivery-operator/dx-extensions-init`: pulls every extension by
  digest, verifies it, unpacks WARs and JARs, writes the Liberty `<webApplication>`; fails clearly when a required
  extension is missing (`optional: true` skips it).
- **Controller**: resolves the tag to a digest **before** any restart (a missing tag never restarts the portal:
  `ArtifactNotFound`), rolls the Web Engine out through its StatefulSet one pod at a time, waits for every pod Ready
  with the new digest, registers the portlets in DX with XMLAccess `predeployed` **only when missing or changed**,
  optional page, rollback to the previous digest when the rollout fails (`RolledBack`), unregisters on delete.
- Chart values `enableControllers` and `leaderElectionID` (a second instance of the operator, for example a test one
  limited to one namespace and one controller).
- Sample [16-java-portlet](samples/16-java-portlet): the EUROPESIP Showcase portlet and its build script.

## 0.13.0 · 2026-11-23 · Renamed: DX Delivery Operator

The product is now called **DX Delivery Operator** (by EUROPESIP), formerly *HCL DX Script Application operator*
(#52). It started as a Script Application deployer and today also moves WCM libraries, pages, themes, XMLAccess, DAM
collections, WCM admin tasks, databases and portal bundles; the focus stays on delivering applications. No functional
changes.

**Renamed**
- Repository `europesip/hcl-dx-scriptapp-operator` → `europesip/dx-delivery-operator` (GitHub redirects the old
  URLs); Go module `github.com/europesip/dx-delivery-operator`.
- Image `ghcr.io/europesip/dx-delivery-operator`. 0.13.x is also published as
  `ghcr.io/europesip/hcl-dx-scriptapp-operator` (deprecated, removed in 0.15.0).
- Helm chart `oci://ghcr.io/europesip/charts/dx-delivery-operator`. 0.13.x is also published under the old name
  `hcl-dx-scriptapp-operator` (deprecated alias, removed in 0.15.0), identical except that it keeps the 0.12.x
  resource names.
- Values `nameOverride` / `fullnameOverride` (new).
- The `com.europesip.dx.tool.operator` annotation, the `Operator:` line of `EXPORT-INFO.txt` and the event source now
  say `dx-delivery-operator <version>`.

**Not changed:** API group `dx.europesip.com`, every kind and CRD, finalizers, labels, the leader-election lease
(`hcl-dx-scriptapp-operator.europesip.com`, so an old and a new pod never run at the same time), the release name
`dxop` and the namespace `dxop-system` of the documentation. Existing manifests and artifacts keep working.

**Upgrading from 0.12.x** (apply the CRDs first, as for every release):
- Keep the resource names: `helm upgrade dxop oci://ghcr.io/europesip/charts/dx-delivery-operator --version 0.13.0
  -n dxop-system --reuse-values --set nameOverride=hcl-dx-scriptapp-operator --set image.repository=ghcr.io/europesip/dx-delivery-operator`
  (or the deprecated chart `hcl-dx-scriptapp-operator` 0.13.0, which sets the override itself).
- Or take the new names (Deployment, ServiceAccount, ClusterRole and Binding `dxop-dx-delivery-operator`): the same
  command without `nameOverride`. Helm replaces the old objects; the shared lease hands over to the new pod.
- With `--reuse-values`, set `image.repository` as shown: an old release keeps its stored image repository otherwise.
- As with any operator restart, check that no WCM import or export is running.

## 0.12.0 · 2026-11-23 · Artifact kinds `theme` and `app`

- **Kind `theme`** (#49): a `DXExport` with only `parts.themes` pushes an artifact of kind `theme` (push it to
  `<project>/themes/<origin>-<theme>`); every bundle with themes carries `com.europesip.dx.themes`, and
  `bundle.parts` now lists `themes` (it was left out before). Readers map `theme`/`themes` to `theme` (not `site`).
- **Kind `app`** (#50): Script Applications and portlets, folder `apps/`, written by DX Transfer Studio 0.2.5;
  recognised and documented (annotations `scriptapps`, `portlets`, `portlets.jsr`, `portlets.war`), not imported by
  the operator yet.
- Docs: artifact-annotations, portable-bundles, spec docs/specs/49-50-theme-app-kinds.md.

## 0.11.0 · 2026-11-22 · DX Core (traditional DX 9.5 on WebSphere)

- **Secret fields that say whose credentials they hold** (#45, docs/secret-fields.md): new `DXConnection.spec.portalCredentialsSecret`
  and `tlsCASecret`, `DXDatabase.spec.dbAdminCredentialsSecret` (+ `dbAdminCredentialsSecretNamespace`) and `outputSecretName`.
- **Deprecated** (still accepted, with a `DeprecatedField` warning event and a note in the `Ready` condition; removal
  announced later): `credentialsSecret` → `portalCredentialsSecret`, `caSecret` → `tlsCASecret`, `adminSecret` →
  `dbAdminCredentialsSecret`, `adminSecretNamespace` → `dbAdminCredentialsSecretNamespace`, `secretName` → `outputSecretName`.
  Setting the new and the old name with different values is rejected by the API server.
- **Artifact format, schema 2** (shared with DX Transfer Studio and Backstage, `docs/artifact-annotations.md`):
  `com.europesip.dx.kind` is `wcm`, `site` or `xmlaccess` (`portal` reserved for whole-portal exports, never with
  credentials); `source.server` is a short name (`DXConnection.spec.sourceName`, default the connection name) and the
  URL goes to the new `source.url`; `source.virtual-portal` is written next to `source.portal`; `org.opencontainers.image.authors`
  and `com.europesip.dx.reason` come from the new `spec.destination.authors` and `spec.destination.reason` (else the
  annotations of the resource). Readers accept schema 1 and the guide v1.
- **DX Core** (#44): `DXConnection.spec.runtime` (`compose` default, `core`) and `spec.dxConnect` (URL, `cwCredentialsSecret` for the Config Wizard
  and `wasCredentialsSecret` for WebSphere, profile). On `core` the WCM library exports and imports go through **DXConnect** (the calls
  of DXClient `wcm-library-export` / `wcm-library-import`), because DX Core has no `dxFileTransfer`; the operator turns
  DXConnect's tar (one folder per library) into its usual zip and back, so exports move between DX Core and DX Compose.
  `mode: ImportCopy` is refused on Core (import-wcm-data keeps the IDs).
- **Context root** taken from `contentHandlerPath` everywhere (XMLAccess, page URLs): a traditional DX with an empty
  context root works with `contentHandlerPath: /mycontenthandler`.
- **`DXExport` `parts.themes`**: the registration of each theme with its own skins (`themes/theme-<name>.xml`, cleaned for
  the import: empty-locale data and repeated skin locates removed) and its WebDAV folder (`themes/theme-<name>-webdav.zip`).
  **Cloned portlets** (`$cloned`) used by the exported pages are defined in `xmlaccess/01-pages.xml`.
- **`DXTheme` `registration: FilesOnly`**: upload only the WebDAV files (from the `-webdav.zip` of a bundle) and leave the
  registration to a `DXXMLAccess` of the theme XML; files the target rejects with HTTP 400 are reported as an event.
- **XMLAccess** honours the timeout of the resource (it was cut at 90 s, too short for a site import) and goes through
  `internalURL` when set.
- Docs *DX Core*, sample 15.

## 0.10.1 · 2026-11-19 · Imports of folders pushed with oras; virtual portals with LDAP

- **Folders pushed with `oras push <ref> <folder>`** (#41) are unpacked as `oras pull` does (they were one level too
  deep and DX failed: SER3009E), and the chunk zips always start at the export folder DX expects (two levels above the
  `<library>.node` files), whatever extra folders the source has.
- **Virtual portals on DX Compose with LDAP** (#42): the realm is the one of the spec, of an existing virtual portal,
  else `FederatedRealm` and then `defaultWIMFileBasedRealm`; each try is checked in the list of virtual portals
  (XMLAccess answers `ok` also when DX rejects it), so a failed creation stops the run instead of importing into the
  base portal.

## 0.10.0 · 2026-11-19 · WCM maintenance modules

- **New resource `DXWCMAdminTask`** (#39): the member fixer and the workflow checker of WCM on DX Compose over HTTP
  (`/wps/wcm/myconnect?MOD=MemberFixer|WorkflowControlChecker`), the `ConfigEngine run-wcm-admin-task-*` of traditional
  DX: libraries or all libraries, one request each, **report mode unless `fix: true`**, only the documented options
  (`memberFixer`: altDn, invalidDn, mismatchedId, realm, noRealmDn; `workflowChecker`: updateDocSecurity,
  rescheduleActions, repairInvalidWorkflow; restrictOn, preserveDates). Summary per library in `status.libraries`, DX's
  answer in the ConfigMap `<name>-report`, optionally an OCI artifact. Holds the lock of the DX (never with an import).
  Docs *WCM maintenance modules*, sample 14.
  With `DXConnection.spec.internalURL` (0.9.2) the requests go to the in-cluster URL.

## 0.9.2 · 2026-11-19 · WCM imports never overlap after a proxy cut; internalURL

- **No overlapping chunks** (#37): when a proxy cuts the data-module request of a chunk (HTTP 502/503/504) DX goes on
  importing it; the operator now waits, before the next chunk, the time DX needs for it (1.5 × its files at the speed of
  the uncut chunks of the run, or 4 files/s; 3 to 90 minutes; event `ChunkCut`). Two imports at once deadlocked Derby.
- **The libraries of a cut chunk are checked in DX** at the end instead of being reported failed: imported when DX lists
  them and they are new or updated (WCM REST `updated`) after their chunk started, with a warning.
- **`DXConnection.spec.internalURL`**: in-cluster URL of the Web Engine (`http://dx-deployment-web-engine.<ns>.svc:9080`)
  for the long requests (WCM data module import and export, dxFileTransfer): no Traefik or HAProxy timeout cuts them.
  Signed in with the form login (LtpaToken2 has no domain); the connection check tries it and an unreachable one falls
  back to `url`.
## 0.9.1 · 2026-11-19 · Imports resume after a restart; object IDs chosen when importing

- **`DXWCMLibraryImport` resumes at the chunk it was on** after a restart of the operator (#33): the chunk plan and the
  progress are kept in `status.resume`; finished chunks are not imported again. The interrupted chunk, whose import DX
  may still be running, is imported again after a wait (1.5 times the longest chunk, 3 to 30 minutes) into a new
  dxFileTransfer folder (the names carry the attempt; the old folders are deleted when the run ends: deleting the folder
  DX was reading made its import fail). Imports as a copy report the interrupted chunk as failed instead of duplicating
  it. A changed spec or export starts from scratch.
- **Object IDs are chosen when importing** (like WCM import / import-copy), also for XML exported with
  `xmlaccess.sh` of a traditional DX: **`DXXMLAccess.spec.objectIDs`**: `Keep` (default, as before: the requests are
  sent as they are, so the IDs in the XML are kept) or `New` (`create-oids="true"` on every `<request>`, once: DX gives
  new IDs). The cleanup request is sent as written.
- **DXExport keeps the object IDs** of the pages in `xmlaccess/01-pages.xml` (0.9.0 set `create-oids="true"`): the
  choice is made at import. The owners of access control are still removed (an unknown owner makes the import fail).
  `EXPORT-INFO.txt` and `bundle.json` (`objectIds`, and `composeNewIds` / `traditionalNewIds` per step) give both
  ways: keep (recommended) or new IDs, with the traditional commands (a `sed` for `create-oids`, the data module
  `taskType=import-copy` for WCM). Same choice for pages and Portal Site (Keep ↔ `mode: Import`, New ↔
  `mode: ImportCopy`); docs *Portable bundles*.

## 0.9.0 · 2026-11-19 · Portable bundles (DXExport)

- **New resource `DXExport`** (#31): pages (XMLAccess), the Portal Site library and WCM libraries of a DX Compose
  portal (base or virtual) in one OCI artifact, a **DX bundle** (`dx-bundle/v1`): `EXPORT-INFO.txt` (source,
  versions, who and why, contents, warnings and **how to import into DX Compose and into a traditional DX on
  WebSphere**, with the `xmlaccess.sh` and `ConfigEngine.sh import-wcm-data` commands), `bundle.json` (ordered import
  steps, size and sha256 of every file), `xmlaccess/01-pages.xml` (the XMLAccess export turned into an update request
  with `create-oids="true"`, without status, build, comments and owners) and `wcm/wcm-export-<name>.zip` (the data
  module export). Status: pages with their content nodes, libraries, files, warnings (themes, skins and portlets the
  target needs). Artifact attributes `com.europesip.dx.kind: dx-bundle`, `bundle.schema`, `bundle.parts`, `pages`.
  DXXMLAccess and DXWCMLibraryImport read a bundle as their image source (checked on DX Compose CF239). Docs:
  *Portable bundles*. Sample 13.
- The WCM part of `DXWCMLibraryExport` is shared with `DXExport` (no change of behaviour).

## 0.8.0 · 2026-11-19 · Attributes of the artifacts

- **Attributes of the artifacts** (docs/artifact-annotations.md): every artifact pushed (WCM exports, XMLAccess answers)
  carries manifest annotations `org.opencontainers.image.*` and `com.europesip.dx.*` (kind, source server, version,
  connection and portal, libraries, requested by, reason, operator version, what created the resource), so a later
  version can list and filter by them. **`DXWCMLibraryExport.spec.destination.attributes`**: free key/value
  attributes, written as `com.europesip.dx.attr.<key>`.
- **EXPORT-INFO.txt** also says who asked for it and why, the operator version, what created the resource (Backstage
  and its plugin, from the annotation `dx.europesip.com/created-with`) and the attributes.
- The chart passes `OPERATOR_VERSION` (its appVersion) to the operator.

## 0.7.0 · 2026-11-19 · Export WCM libraries

- **New resource `DXWCMLibraryExport`** (#27): exports chosen WCM libraries of a DX Compose portal (base or virtual
  portal) with the WCM data module and pushes them to a registry as an ORAS file artifact (no artifactType):
  `wcm-export-<name>.zip`, exactly as DX writes it (the JCR format of ConfigEngine `export-wcm-data`), and
  `EXPORT-INFO.txt` (source server, DX build, virtual portal, date and time, libraries with sizes, how to import).
  `DXWCMLibraryImport` reads it as an image source; a traditional DX imports the unzipped folder with
  `import-wcm-data`. Status: `phase`, `currentLibrary`, `exportedLibraries`, `totalBytes`, `image`, `digest`.
  Deleting it stops a running export. Sample 12, docs (how it works, reference). Chart: the new CRD and RBAC rules.

## 0.6.2 · 2026-11-18 · Deleting a running import stops it

- `DXWCMLibraryImport` (#25): while a run goes on, the operator checks every 10 s whether its resource was deleted;
  if so it stops at once: the unpack of the export, the download of an image and the chunks still to upload. Before,
  it went on (CPU, disk) until the step ended.

## 0.6.1 · 2026-11-18 · Big exports do not restart the operator

- **A WCM export of hundreds of MB no longer restarts the operator in a loop** (#23). Seen with a 760 MB `.tar.bz2`:
  the health probes timed out (1 s) while it unpacked on a busy control-plane node with 50m of CPU, the pod was
  restarted 7 times, and every restart started the import again. Chart: requests `500m` / `512Mi`, limit `2Gi`;
  prefers nodes that are not control plane (preferred: a one-node cluster keeps working); work volume `8Gi` (was
  `2Gi`: an archive and its unpacked tree did not fit); probes with `timeoutSeconds: 5` and `failureThreshold: 6`,
  configurable (`probes`).
- **The unpack shows its progress** in `status.message` every 15 s: `unpacking <file>: 45% (340 of 760 MB read)`.

## 0.6.0 · 2026-11-18 · The libraries of a WCM export, to choose from

- `DXWCMLibraryImport` (#21): new **`status.exportLibraries`**, every library of the export with its `title`,
  **`description`** (`icm:description` of the source portal, up to 300 characters), `bytes`, `files`,
  `existsInTarget` and **`plan`**: `Import`, `Skip` (Portal Site, or it exists and `overwrite` is false),
  `CannotUpload` (names DX rejects) or `NotSelected` (left out by `include`, `exclude` or `excludeHCLDefaults`), with
  its `reason`. Every run writes it, dry run included, so a UI can list the libraries and import only the chosen ones
  (`libraries.include`).

## 0.5.2 · 2026-11-18 · The folder of an image source is found

- **Image sources without files under `/app`** (the default folder): if the image has only one top-level folder with
  files that is not part of an operating system (for example `/export` of a WCM export image), it is used. Otherwise
  the error names the folders that have files.
- **A wrong folder is no longer retried forever**: `DXWCMLibraryImport` and `DXXMLAccess` end as **Failed** with
  reason `SourceError` ("… has no files under /x; folders with files: /export"), nothing imported or sent. Before,
  they stayed Pending and retried every few minutes.

## 0.5.1 · 2026-11-18 · XMLAccess answers are never cut

- `DXXMLAccess` `output.configMap` (#17): answers over 900 KiB in all (a ConfigMap holds at most 1 MiB) are **no longer
  cut**. The ConfigMap then only holds `README`, which says where they are. Without `output.oci` the run is Failed
  with the new reason **`OutputTooLarge`**, which says to use `output.oci`. With `output.oci` the registry is written
  first and keeps them all, so the run keeps its result. Use `output.oci` for exports.

## 0.5.0 · 2026-11-17 · XMLAccess requests

- **New resource `DXXMLAccess`** (#15): runs XMLAccess requests of your own against the base portal or a virtual
  portal (`/wps/config/<context>`). The requests come inline (`xml`) or as the `.xml` files of a source: a URL archive
  (for example a Git folder through its archive URL, or an S3 presigned URL), an OCI image or ORAS artifact, or a
  ConfigMap. Options:
  - `files` sets the order and `variables` fills `${NAME}`;
  - every file is checked before anything is sent; `dryRun` only checks and lists them;
  - it stops at the first failure unless `continueOnError`;
  - `output.configMap` keeps DX's answers (exports), and `output.oci` pushes them to a registry (Harbor) as an OCI
    artifact with a new tag every run (`status.outputRef`). The artifact can be the source of another resource;
  - `cleanup` sends a request when the resource is deleted.

  It runs **once per change**, like a Job: no drift detection. Sample 11.
- `DXXMLAccess` can be a `dependsOn` kind.
- Docs: a [guide](docs/migration-guide.md) to migrating a portal and running XMLAccess, as YAML or from the HCL DX
  plugin of Backstage (also in TechDocs).

## 0.4.0 · 2026-11-17 · WCM library imports

- **New resource `DXWCMLibraryImport`** (#11): imports WCM libraries from a library export into DX Compose, without
  ConfigEngine (which DX Compose does not have) — the base portal or a virtual portal (`virtualPortal.create` creates
  it). Library selection (include, exclude, `excludeHCLDefaults`), `overwrite`, **Portal Site protected** unless
  `portalSiteLibraryOverwrite: true`, `mode: Import|ImportCopy`, `dryRun` inventory, `access` roles on the imported
  libraries, automatic chunks (`upload.maxChunkSize`, `upload.maxChunkFiles`), one-shot runs. Sample 10.
  Tested on DX Compose CF239 with a CF230 export of 48 libraries, from a URL and from Harbor, and used in production.
- **Sources**: URL archives are streamed to disk (up to 8 GiB, also `.tar.bz2` and `.tar`); **ORAS file artifacts**
  (`oras push <ref> file.tar.bz2`) are read as files; an archive inside a source is unpacked by the WCM import.
- **Login**: when DX has no Ring API (`/dx/api/core` answers 404/502/503, e.g. a DX with only the Web Engine), the
  operator signs in with the form login.
- **Chart**: `logFormat` (default `json`) and `logEvents` (default `false`: every Kubernetes event also as a log line,
  for clusters that do not ship events — off by default so events are not stored twice). `DXWCMLibraryImport` can be a
  `dependsOn` kind.
- **TechDocs for Backstage** (#6): `mkdocs.yml` and a docs home page.

## 0.3.2 · 2026-11-12 · OIDC session cookie WAS_p...

- **Fix:** with OpenID Connect, Liberty names the DX SSO cookie `WAS_<letter><hash>`, and the letter depends on the DX
  (`WAS_n...` on the lab DX, `WAS_p...` on another DX Compose CF239). The operator only accepted `WAS_n...`: the
  `DXConnection` stayed `LoginFailed` ("DX did not open a session ... form login HTTP 302"). Any `WAS_` cookie is the
  session now (`WASReqURL...` and `WASOidc...` are not). Tested with DX Compose CF239 + Keycloak 26.3: `DXConnection`
  Ready, sample 01 pushed and its page created.

## 0.3.1 · 2026-11-08 · XMLAccess, one call at a time

- **Fix:** DX answers concurrent XMLAccess requests with an empty HTTP 200 now and then; the resource went to `DXError`
  (and ArgoCD showed it Degraded) until the next check. The operator now sends one XMLAccess request at a time to each
  DX, and retries an empty answer twice (after 2 and 4 seconds). Seen on DX Compose CF239 when a page and a theme were
  checked at the same moment.

## 0.3.0 · 2026-11-08 · DX prefix, DXDatabase for every engine, themes of their own

**Breaking: the resources were renamed** so that every one has the DX prefix:

| Before | Now | Short name |
|---|---|---|
| `ScriptApplication` | `DXScriptApplication` | `dxapp`, `dxsa` |
| `DAMCollection` | `DXDamCollection` | `dxdam` |
| `PostgresDatabase` | `DXDatabase` (`superuserSecret` → `adminSecret`) | `dxdb` |

`DXConnection`, `DXPage` and `DXTheme` keep their names. **Migrating from 0.2:** apply the CRDs of 0.3.0, change the
`kind` of your resources (and `superuserSecret` / `superuserSecretNamespace` → `adminSecret` / `adminSecretNamespace`)
and apply them again: the operator finds the existing content in DX by its name (WCM name, collection name, database)
and adopts it. Then delete the old resources (their finalizers are only removed by an operator that knows them: with
0.3.0 running, patch them out with `kubectl patch ... --type merge -p '{"metadata":{"finalizers":null}}'`; nothing is
removed from DX) and the old CRDs.

- **DXDatabase**: PostgreSQL, **Oracle** (`oracle.service`; the database is a schema) and **SQL Server**
  (`sqlServer.encrypt`, `trustServerCertificate`), with `engine`. Same Secret for every engine (`engine`, `username`,
  `password`, `host`, `port`, `database`, `uri`, `jdbcUrl`). Tested with PostgreSQL 17, Oracle Database 23ai Free and SQL
  Server 2022: create, sign in as the owner, reconcile again, delete. DB2 is not supported yet (its Go driver needs cgo).
- **DXTheme**: `base.copyFiles: false` uploads only the files of the source (a theme of its own, nothing copied from the
  themes of HCL); `profile` and `layout` set the resource aggregation profile and the default layout. Sample 09 now
  installs the [EUROPESIP sample theme](https://github.com/europesip/hcl-dx-theme-sample) from its release.

 2026-11-08 · Themes

(0.2.0 was tagged but never published: its CI failed on a generated file header.)

- New resource **DXTheme** (short name `dxtheme`): a WebDAV theme of its own. The operator copies a base theme of the
  same DX (default: Portal 8.5) over WebDAV, adds the files of its `source` (ConfigMap, image or URL, as the Script
  Applications) and the HTML of `headHTML` in the `<head>` of its templates, uploads the folder and registers the
  theme with XMLAccess (copy of the registration of the base). Drift (registration or folder deleted in DX) is
  restored or reported; `deletionPolicy: Delete` removes the registration and the folder.
- Pages use it with `spec.theme` (its unique name, default `dxop.<namespace>.<name>`), and can wait for it with
  `dependsOn: [{kind: DXTheme, name: ...}]` (new dependency kind).
- New sample [09-theme](samples/09-theme/theme.yaml) and ArgoCD health check for DXTheme.
- Works with and without OpenID Connect (basic auth for WebDAV and XMLAccess). Tested with DX Compose CF239, with
  and without Keycloak.
- **Fix:** after a restart of DX (for example a `helm upgrade` of DX) the old SSO cookie stayed in the operator, the new
  login skipped the form login and every call answered 401 until the operator was restarted (resources in `DXError`).
  Every login now starts with an empty cookie jar.
- New CRD: Helm only installs CRDs on the first install, so with an existing release apply
  `charts/hcl-dx-scriptapp-operator/crds/` before `helm upgrade`. The other CRDs only gain the `DXTheme` dependency kind.

## 0.1.3 · 2026-11-08 · A switch for the OpenID Connect compatibility

- New chart value **`dx.oidcCompatibility`** (default `true`). `false` turns off the form login fallback of 0.1.2 and
  restores the behaviour of 0.1.1: only the REST login of DX. It sets the environment variable `DX_FORM_LOGIN`
  (`auto` / `false`) of the operator.

## 0.1.2 · 2026-11-08 · DX with OpenID Connect (Keycloak, Entra ID...)

- **What changed:** when the DX REST login (`/dx/api/core/v1/auth/login`) answers without a session cookie, the operator
  signs in with the portal login form (`<context root>/j_security_check`, without following the redirect) and uses that
  session for the Ring API and DAM calls.
- **Why:** when DX signs users in through OpenID Connect (the `openidConnectClient-1.0` feature of Liberty), Liberty names
  the SSO cookie `WAS_n<hash>` instead of `LtpaToken2`, and this cannot be configured (Open Liberty issue 18803). The DX
  REST login then answers 200 without a cookie, and every REST call of the operator got 401: the resources stayed in
  `DXError` and the DXConnection was never Ready. The Ring API and the DAM API accept the new cookie. Tested with DX
  Compose CF239 and Keycloak 26.8.
- **DX side:** the OIDC `authFilter` must leave out `/wps/config` (XMLAccess, as HCL documents), `j_security_check` and
  requests with `Authorization: Basic` (DXClient). See [docs/limitations.md](docs/limitations.md).
- **Without OIDC nothing changes:** the REST login returns `LtpaToken2` and no form login happens.
- **Going back:** chart 0.1.3 with `dx.oidcCompatibility=false`, or `--version 0.1.1`.

## 0.1.1 · 2026-11-08 · Adopted pages

- A DXPage that was already in DX exactly as declared (adopted) now records its applied state. Before, an adopted page
  ignored `deletionPolicy: Delete` (it stayed in DX when the resource was deleted) and later layout changes.

## 0.1.0 · 2026-11-08 · First release

- ScriptApplication, DXPage, DAMCollection, PostgresDatabase and DXConnection: Script Applications pushed with HCL
  DXClient (inside the operator image), pages with XMLAccess, DAM files, backend databases, drift detection and restore,
  Argo CD health checks.
