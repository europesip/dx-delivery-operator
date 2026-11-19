# Restrictions and limitations

[Español](limitations.es.md) · [README](https://github.com/europesip/hcl-dx-scriptapp-operator#readme)

## Script Applications

- **The main HTML file must be a complete HTML document** (`<!DOCTYPE html><html><head>…</head><body>…</body></html>`).
  DXClient keeps the `<link>` and `<script>` elements of the `<head>` (rewriting their URLs to the files it
  stored in WCM) and the content of the `<body>`. With a bare fragment the outer element and the `<link>`
  elements are lost. Builds of Vite, webpack or HCL's `create-dx-script-app` already produce complete documents.
- **Scripts**: both inline scripts and files referenced from the `<head>` run. Files in the `<head>` run
  before the markup of the body exists: use `defer`, or wait for your root element.
- **Several instances on one page** share the page: select your root element by class or use DX's namespace
  token in IDs.
- **Each file is limited to 4.5 MB**: WCM stores each file in one element of at most 5,000,000 bytes, and a
  bigger file makes DX reject the push and leave an incomplete item. The operator checks the sizes before
  touching DX. Split the bundle (code splitting) or serve large files from the DAM.
- **Global CSS restyles the whole portal**: scope every rule to your application.
- **The WCM name is the key** (`spec.wcm.name`, default: the name of the resource): renaming it creates a
  second application; the old one stays in DX.
- **A restored application has a new content ID** (after being deleted in DX). Its pages are rebuilt
  automatically, with default viewer settings.
- **Edits in DX are overwritten** with `driftPolicy: Restore` (the default): the operator compares the last
  modification date of the content. Use `Report` where authors work on the application in DX.
- **Push or syndication, per environment**: if an environment receives the application by syndication, a push
  there creates a second, different item. Use `mode: Verify` in that environment.
- **WCM projects** (`wcm.projectContext`): the project must exist; publishing it is up to you.
- **Mutable tags** (`:latest`, `:main`): the operator resolves the digest on every pass, so a new image under
  the same tag is pushed at the next resync. Pin a digest (`repo@sha256:...`) for immutable deployments.
- **Substitutions** only apply to text files with the extensions listed in [how it works](how-it-works.md), and
  their values end up in files every visitor can download.
- **ConfigMap sources** are limited to 1 MiB in total, and keys cannot contain `/` (`assets__app.js` →
  `assets/app.js`).

## Pages

- **The user of the connection must be a DX administrator** (XMLAccess).
- **Changes made in the DX UI: only a deleted page is detected (for now).** A page deleted in DX, from the UI
  (Site Manager → *Delete Page*) or by script, is recreated at the next resync (by default every 5 minutes), with
  its titles and applications. A page that is still there but was **changed** in the UI (renamed, friendly URL,
  applications removed or added, access, theme, layout or position) is **not** detected yet: the operator compares
  a hash it stored on the page, which the UI does not change, so the page stays `InSync`. To apply the declared page
  again, force a reconciliation:
  `kubectl -n <namespace> annotate dxpage <name> dx.europesip.com/reconcile="$(date +%s)" --overwrite`.
  Fixing it (comparing the real state of the page) is
  [issue #2](https://github.com/europesip/hcl-dx-scriptapp-operator/issues/2).
- **Changing the list of applications recreates the page** (XMLAccess cannot replace the components of a page
  in place): settings made on the viewers and changes to the layout are lost. Titles, access, theme, parent and
  position are updated in place.
- **Anonymous pages**: `access.anonymous: true` opens the page, but the Script Applications are WCM content and
  need anonymous users to have the User role on the **Script Application Library** (set it once in DX, *Web
  Content Libraries* → library → access). The operator cannot set WCM item permissions (DX offers no API for it).
- **Deleted pages are only marked** by DX and removed later by its cleanup task: until then, DX still lists
  references to them.
- One layout: the applications are stacked in one column (the `1Column` layout template by default).
- The URL in the status is only computed for pages under `ibm.portal.Home`.

## DAM

- Every file of the source becomes an asset named after its path (`img/logo.png` → `img-logo.png`).
- A file replaced in the DAM by someone (a new version) is not detected: only deleted assets and collections are.
- Collection access: the operator grants; it does not revoke permissions given in DX.
- Deleting a collection (policy Delete) ignores media references: pages and applications using its assets show
  broken links.

## Themes

- **WebDAV themes only** (the theme files in `dav:fs-type1/themes/`). Themes packaged as a WAR/EAR are not
  supported.
- **The registration is modelled on a theme of the same DX** (the base, default Portal 8.5: skins and settings), so the
  base must exist in that DX. With `base.copyFiles: true` (the default) its files are copied too, and the theme does not
  follow later changes of the base: force a reconciliation (or change the resource) to copy it again. A theme of its
  own (`base.copyFiles: false`) needs a source with every file of the theme: see the
  [EUROPESIP sample theme](https://github.com/europesip/hcl-dx-theme-sample).
- **The folder and the unique name are the keys**: renaming them creates a second theme; the old one stays in DX
  (unless the policy is Delete).
- **Applying a change uploads the whole folder** (about 430 files for Portal 8.5, a minute or two).
- **Edits made in DX to the files of the theme are not detected**; only a deleted registration or folder is.
- **Pages** pick the theme with `spec.theme` (its unique name); for the layouts of the new theme, point
  `layoutTemplate` at `dav:fs-type1/themes/<folder>/layout-templates/...`.

## DXDatabase

- Engines: **PostgreSQL, Oracle and SQL Server** (tested with PostgreSQL 17 / CloudNativePG, Oracle Database 23ai Free
  and SQL Server 2022). **DB2 is not supported yet**: its Go driver needs the native IBM client library (cgo), which
  the static image of the operator does not include.
- An existing server and an admin Secret readable by the operator (it may be in another namespace with
  `adminSecretNamespace`). The operator generates the password of the owner; if your DBA must keep the credentials,
  skip the DXDatabase and give the backend the Secret they provide (for example an ExternalSecret from your vault).
- Names: PostgreSQL lower-case identifiers; Oracle a lower-case schema name of up to 30 characters (the user is
  created in upper case); SQL Server letters, digits and `_`.
- The operator does not migrate schemas: that belongs to the backend (Flyway, Liquibase, its own start-up).

## Operator

- **Not an HCL product** and not supported by HCL. DXClient is used unmodified, as HCL distributes it.
- One active replica; DXClient runs are serialised, so many pushes queue up.
- Tested with HCL DX Compose CF239 and DXClient `v95_CF239_20261103-1404`. Use the DXClient of your DX level by
  building the image with `--build-arg DXCLIENT_IMAGE=hclcr.io/dx-public/dxclient:<tag>`.
- **DX with OpenID Connect (Keycloak, Entra ID...)**: supported from 0.1.2 (`dx.oidcCompatibility=false` in the chart turns it off, 0.1.3). With the OIDC client of Liberty the SSO
  cookie gets a generated name (`WAS_n...`, `WAS_p...`: the letter depends on the DX, instead of `LtpaToken2`) and the DX REST login answers without a cookie, so the
  operator falls back to the portal form login. The OIDC `authFilter` of DX must leave out `/wps/config` (XMLAccess,
  documented by HCL), `j_security_check` and requests with `Authorization: Basic` (DXClient).
- API `v1alpha1`: fields may still change before `v1`.

## WCM library imports

- **Uploads**: dxFileTransfer accepts at most 500 MB per upload (the operator chunks by library; a single library over
  500 MB compressed cannot be imported) and rejects file names with spaces. Such names cannot be renamed (DX finds the
  values of an item by its name): the library is reported as not importable; rename the item in the source portal.
- **20-minute requests**: the HAProxy of DX Compose cuts requests after 1200 s (Traefik or another proxy in front may
  cut earlier). Updating existing items is ~7 files/s (new items ~60): keep `upload.maxChunkFiles` low enough, or set
  `internalURL` in the DXConnection (0.9.2) so the long requests reach the Web Engine without proxies.
- **A cut chunk** (0.9.2): DX gives no "done" signal over HTTP, so after a cut the operator waits an estimate before
  the next chunk; if DX is slower than the estimate (a very busy Derby) two imports may still overlap. A library that
  already existed in the target and whose `updated` did not change is reported failed: check it in DX.
- **Resume after a restart** (0.9.1): DX does not say whether it is still importing a chunk whose request was cut, so
  the operator waits an estimate (1.5 times the longest chunk, 3 to 30 minutes) before importing it again. A chunk DX
  takes longer to import than that may still overlap with the new attempt; check the DX log if the resumed chunk
  fails. Better: do not upgrade or restart the operator while an import runs (`kubectl get dxwcmlibraryimports -A`).
- **Access control**: imported libraries keep the ACLs of the source portal, whose users and groups usually do not
  exist in the target: only administrators see them until roles are given (`access`).
- **Versions**: the export must come from the same or an older DX (tested CF230 → CF239).
- **Virtual portals** are addressed by URL context, not by host name; XMLAccess cannot delete them (DX answers ok and
  does nothing), and the operator never deletes them nor libraries.
- **Several Web Engine replicas** are not tested yet: the upload and the import may reach different pods.
- **Trivy** in Harbor scans images, not ORAS file artifacts.

## WCM library exports

- **One Web Engine replica**: the export writes into a dxFileTransfer folder of one pod and the download must reach
  the same pod. With several Web Engine replicas (non-Derby database) the requests may land on different pods: not
  supported yet.
- **Size**: the zip is downloaded to the work volume of the operator (8Gi by default) and unpacked there to check it:
  an export needs about 2.5 times its zip size free.
- **20-minute requests**: each library is one request to the data module; a library that takes longer than the
  HAProxy of DX Compose allows (1200 s) fails the run.
- **Versions**: import into a DX of the same CF level as the source or newer (`EXPORT-INFO.txt` names the source
  build). Traditional DX: `import-wcm-data` imports every library of the folder (there is no library name property).
- **Access control** goes with the libraries, as in imports: the target may not have the same users and groups.

## Portable bundles (DXExport)

- **Themes are not in the bundle yet**: the pages locate their theme and skins by unique name, so the target must have
  them (listed in `EXPORT-INFO.txt` and `status.warnings`).
- **Parents**: a page whose parent has no unique name cannot be placed in another portal; export from a parent that has
  one, or give it one. Pages under the content root are fine.
- **Owners** of access control are dropped (users of the source registry need not exist in the target); the roles and
  their mappings are kept, so groups and users of the mappings must exist in the target directory.
- **Portlets of your own** (JSR 286) do not run on DX Compose without the Java Transition Module.
- The **WCM** part has the limits of WCM library exports above (one Web Engine replica, work volume, 20 minutes per
  library).
- The **traditional import** commands are written from the HCL documentation; to be validated on DX 9.5 CF230.

