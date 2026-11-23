# Guide: move a portal to DX Compose, and run XMLAccess

**English** · [Español](migration-guide.es.md)

How to use the import resources of the operator, step by step: WCM libraries (`DXWCMLibraryImport`), the content of
the managed pages (Portal Site) and XMLAccess requests of your own (`DXXMLAccess`). Each step can be written as YAML
(GitOps) or started from **DX Backstage** (our Backstage plugin), which writes the same YAML for you.

## Before you start

1. **A `DXConnection`** to the target DX, with a DX administrator. From Backstage: tab **Connections** → *New
   connection* (it lands in the namespace `dx-portals`, password in OpenBao).
2. **The exports in a registry** (Harbor), as delivered by the source portal:
   ```bash
   oras login harbor.example.com -u <user>              # password: your CLI secret of Harbor
   oras push harbor.example.com/dx-wcm/libraries-cf230:2026-11-18 export-europesip.tar.bz2 README.txt
   ```
   Or a URL (`.tar.bz2`, `.tar.gz`, `.zip`) with its SHA256.
3. **A pull secret** (`kubernetes.io/dockerconfigjson`) for the registry in the namespace of the resources.

Order of a migration: virtual portal → themes → WCM libraries (not Portal Site) → pages → **Portal Site** → access →
URL mappings and the rest. Always a **dry run** first.

## 1. WCM libraries

**From Backstage:** tab **Web content**, choose the environment and portal in the header → **Import WCM libraries**.
The wizard asks the source (the list of the Harbor project, newest first), the options (default: only your own
libraries, skip those that exist, Portal Site untouched, dxadmins Administrator), runs a **dry run** and shows the
inventory; **Import** runs it for real. Progress and history stay on the tab.

**As YAML:** see [sample 10](https://github.com/europesip/dx-delivery-operator/tree/main/samples/10-wcm-library-import).

```bash
kubectl get dxwcmimport -n <ns>                         # Phase, Ready, Reason
kubectl get dxwcmimport <name> -o jsonpath='{.status.message}'
```

Then set `dryRun: false` (or remove it): the import runs once. Deleting the resource never deletes libraries.

## 2. Content of the managed pages (Portal Site)

Import it **after the pages**. From Backstage: tab **Pages** → **Import page content (Portal Site)** (red warning:
it replaces the content of the managed pages of that portal). As YAML: a `DXWCMLibraryImport` with
`libraries.include: [Portal Site]`, `overwrite: true` and `portalSiteLibraryOverwrite: true`.

## 3. Pages, themes

- **New page:** tab **Pages** → *New page* (a `DXPage`: titles, friendly URL, parent, theme, anonymous access).
- **New theme:** tab **Themes** → *New theme* (a `DXTheme` copied from Portal 8.5) or *Import theme files* (a zip or an
  ORAS artifact with the files of a theme, on top of the copy).
- Importing the page tree of a traditional portal is `DXPageImport` (issue #13, next).

## 4. XMLAccess requests of your own (`DXXMLAccess`)

For anything without a resource of its own. Examples:

**Export something and keep the answer** (read-only, safe):
```yaml
apiVersion: dx.europesip.com/v1alpha1
kind: DXXMLAccess
metadata: { name: export-url-mappings }
spec:
  connectionRef: { name: dx }
  xml: |
    <request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="PortalConfig_8.5.0.xsd" type="export">
      <portal action="locate"><url-mapping-context action="export" objectid="*"/></portal>
    </request>
  output: { configMap: { name: url-mappings-export } }
```
```bash
kubectl get configmap url-mappings-export -o jsonpath='{.data.request\.xml}'
```

**Keep the export in Harbor and apply it in another environment.** An OCI artifact is the standard format of
container registries: besides images, it can hold any files, with a version (tag), date and author. With `output.oci`
the answers of DX are pushed there:
```yaml
  output:
    oci:
      repository: harbor.example.com/dx-exports/url-mappings
      tag: dev-${DATE}                         # default ${DATE}: yyyymmdd-hhmmss of the run
      pushSecrets: [{ name: harbor-push }]     # dockerconfigjson that may push
```
`status.outputRef` says where it went (`oras pull <ref>` gets the files). The answer of an `export` request is itself
an `update` request, so another `DXXMLAccess`, in the target environment, applies it:
```yaml
spec:
  connectionRef: { name: dx }                  # the DX of the target environment
  source: { image: { ref: harbor.example.com/dx-exports/url-mappings:dev-20261117-214132, pullSecrets: [{ name: harbor-pull }] } }
  files: [request.xml]                         # results.json is the summary, not a request
  dryRun: true                                 # check first; then remove it
```

**Run the files of a folder of a Git repository**, in name order (`01-…xml`, `02-…xml`), into a virtual portal:
```yaml
spec:
  connectionRef: { name: dx }
  virtualPortal: { context: europesip }           # /wps/config/europesip
  source:
    url: { url: https://gitlab.example.com/acme/portal/-/archive/main/portal-main.tar.gz, path: portal-main/xmlaccess }
  variables: { SITE_HOST: www.acme.example }       # ${SITE_HOST} in the XML (never secrets)
  cleanup: { file: 99-cleanup.xml }                # optional: run when the resource is deleted
```
For a private repository use a CI artifact URL, an S3 presigned URL or push the folder to Harbor with ORAS
(`source.image`).

**Rules to remember**
- It runs **once per change** (spec, source revision or the annotation `dx.europesip.com/reconcile`). To run it again
  without changes: `kubectl annotate dxxml <name> dx.europesip.com/reconcile="$(date +%s)" --overwrite`.
- Every file is checked first: one broken file and **nothing** is sent. `dryRun: true` only checks and lists.
- The first failed request stops the run (`continueOnError: true` to go on). `status.results` has the DX message.
- No drift detection, nothing undone on delete (except `cleanup`). Write idempotent requests (`action="update"`).
- The answers of DX are kept only with `output.configMap` (at most 900 KiB: bigger answers fail the run with `OutputTooLarge`, never cut) or `output.oci` (a registry, no limit).

```bash
kubectl get dxxml -n <ns>
kubectl get dxxml <name> -o jsonpath='{range .status.results[*]}{.file}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
kubectl get events -n <ns> --field-selector involvedObject.name=<name>
```

## Limitations

- **Custom portlets** (JSR 168/286) of the source do not run on DX Compose without HCL's *Java Transition Module*;
  the page is imported, the portlet does not work. HCL portlets do.
- DX Compose has no ConfigEngine and no PAA; personalization rules have no command line.
- See [Restrictions and limitations](limitations.md).
