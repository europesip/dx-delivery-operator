# Java extensions on DX Compose (DXJavaExtension)

**English** · [Español](java-extensions.es.md)

> **Video (8 min):** [Java portlets on HCL DX Compose without a custom image](https://youtu.be/bvTHifIU8nA) —
> the HCL Java Transition Module, the Java code it covers, and a live DXJavaExtension demo (English, subtitles EN/ES).

> **Licence requirement:** running custom Java code on DX Compose requires the customer to have purchased (be entitled
> to) HCL's **Java Transition Module** (JTM). `DXJavaExtension` is a way to deploy that code; it does not replace the
> entitlement.

`DXJavaExtension` (0.14.0, issue [#54](https://github.com/europesip/dx-delivery-operator/issues/54)) deploys **custom
Java code** to the DX Compose Web Engine **without a custom image**: JSR 168/286 **portlets** (WARs) and
**shared-library JARs**. The code is an OCI artifact in your registry (Harbor, GHCR…); the operator loads it onto the
**official** Web Engine image of HCL, rolls the Web Engine out, and registers the portlets in DX.

## Why

DX Compose runs portlets inside the Web Engine (Open Liberty). HCL's documented way for custom portlets (the guide of
the Java Transition Module) is to **build your own Web Engine image** (`FROM` the official one + `COPY` the WAR into
`resources/customApplications/`), push it to your registry, change the image of the Helm release, add a Liberty
`<webApplication>` in `configOverrideFiles`, and register the portlets by hand with XMLAccess. It works, and for one
or two portlets that rarely change it is a fine choice. It costs a lot when you have more:

- every **CF upgrade** means rebuilding and retesting **your** image on the new base;
- every **portlet change** means building and pushing a multi-GB image;
- you need an image-building CI, access to `hclcr.io` from it, and a registry for the images;
- the image that runs is no longer HCL's;
- the registration is a manual step.

With `DXJavaExtension` the image stays HCL's, your code is a small versioned artifact (KB/MB), a CF upgrade is only
the official tag, and the rollout and the registration are automatic.

## How it works

```
  Git (PR)          Argo CD            DX Delivery Operator                         Web Engine pod (official image)
 ─────────     ─────────────────   ───────────────────────────────────   ─────────────────────────────────────────────
 CR change ──▶ applies the CR ──▶  1 resolve tag → digest (registry)
                                    2 record status.desiredRef
                                    3 patch the StatefulSet template ──▶ StatefulSet recreates the pod
                                                                          API server ──▶ webhook (operator pod): adds
                                                                                         init container + volumes
                                                                          init container: pull by digest, verify,
                                                                                          unpack WAR / JARs, write
                                                                                          the Liberty <webApplication>
                                                                          Web Engine: Liberty starts the WAR
                                    4 wait: every pod Ready with digest ◀ Ready
                                    5 XMLAccess predeployed (only if missing) ──▶ DX catalogue (database)
```

### The pieces

| Piece | What it is | Who creates it |
|---|---|---|
| **Admission webhook** | A rule of the Kubernetes API server: "before you create a pod of this kind, send it to me". Ours is a **mutating** webhook: it answers with a patch. It only acts on the Web Engine pods (`objectSelector`) of the namespaces labelled `dx.europesip.com/java-extensions=enabled` (`namespaceSelector`) | The **operator's Helm chart**, once, when installed with `javaExtensions.enabled=true` (a `MutatingWebhookConfiguration`, a Service and a cert-manager Certificate). The webhook **server runs inside the operator pod** |
| **`DXJavaExtension`** | Says **what** to inject: the artifact, the type, the context root, the page | You (Git) |
| **Init container** (`dx-extensions-init`) | Runs before the Web Engine, in the same pod, and puts the code in place | Added by the webhook to every Web Engine pod being created |
| **Controller** | Resolves, rolls out, waits, registers, rolls back | The operator |

The webhook never changes the HCL chart, the Helm release or the image: only the pod being created. It survives any
`helm upgrade` of DX because it acts at pod creation, not in Helm (no post-renderer needed).

### What the init container does, step by step

1. Reads the list of extensions (name, `repository@digest`, type, file, context root) that the webhook gave it.
2. Logs in to the registry with the pull secrets of the extension (read-only is enough).
3. Pulls each artifact **by digest** and verifies the digest of the manifest and of the file.
4. Portlets: unpacks the WAR into `resources/customApplications/<file>.war/`. Shared libraries: writes the JARs into
   `libraries/customSharedLib/` (the global classpath of the Web Engine, CF236+).
5. Writes the Liberty `<webApplication>` of every WAR into `configDropins/defaults/dx-java-extensions.xml`.
6. Exits 0. If a **required** extension cannot be loaded it exits non-zero with a clear message, and the Web Engine
   does not start with half of its extensions (`optional: true` skips it with a warning instead).

### Why XMLAccess, if the WAR is already in customApplications?

There are **two layers**, and each one must know about the portlet:

- **Liberty** (the application server): the WAR in `customApplications/` and its `<webApplication>` make Liberty
  *start* the web application. The code runs and answers on its context root.
- **DX** (the portal): DX keeps its own **catalogue in its database**: web modules, portlets, their ids, permissions,
  settings, the pages they are on. DX does **not** scan `customApplications/`. Until it is told, the portlet does not
  exist for DX: it is not in the portlet administration, it cannot be put on a page, it has no permissions.

XMLAccess with `predeployed="true"` tells DX: "at this URL there is a web module **already running**; register it
with these portlets". (Picture a shopping centre: Liberty opens the shop; DX is the directory. The shop can be open,
but until it is in the directory nobody finds it or puts it on the floor plan.)

On traditional DX both happened in one step (DX deployed the WAR into WebSphere and registered it). On Compose they are
separate, which is why HCL's method also requires the XMLAccess.

**When**: the registration is stored in the DX database and survives restarts and pod re-creation. The operator only
runs it when the web module is **missing**, when the **portlets of `portlet.xml` change**, or when the page changes. A
new version of the code with the same ids (1.0.0 → 1.1.0) needs nothing in DX.

## What happens on every start, and only once

| On every Web Engine pod start (also delete/re-create, reschedule, scale) | Repeated? |
|---|---|
| Pull the artifact (init container) | Yes, into an `emptyDir` (a 10 KB WAR takes about a second) |
| Liberty starts the WAR | Yes, exactly as with a custom image (Liberty starts its apps on every start) |
| Register in DX (XMLAccess) | **No**: stored in the DX database |

## Failures

| Situation | What happens |
|---|---|
| A tag that does not exist (e.g. a typo in a PR) | The controller resolves the tag **before** anything changes: **no restart**, the Web Engine keeps its version, `ArtifactResolved=False`, reason `ArtifactNotFound`, an event |
| A pod restarts unexpectedly and the artifact is gone or the registry is down | The init container fails with a clear message, the pod stays in `Init:Error` and Kubernetes retries; the same as a custom image that disappeared (`ImagePullBackOff`). Protect what is in use: **immutable tags** and a retention rule that never deletes referenced versions; `optional: true` for extensions the portal can live without |
| The new version breaks the start (init container fails, not Ready before `rolloutTimeout`: spec field, default chart value `javaExtensions.rolloutTimeout`, 30m) | The operator **rolls back** to the previous digest (`RolledBack`), and does not try that digest again until the spec changes |
| The webhook is down | `failurePolicy: Fail` (default): the Web Engine pod is not created until it answers (no Web Engine without its extensions). `Ignore`: it starts without them. Run the operator with 2 replicas if this matters |
| The webhook is not enabled for the namespace | The controller does not roll out: `NamespaceNotEnabled` (missing label) or `WebhookNotActive` — only when a pod created from the current template has no injection; the message says why (no configuration, `namespaceSelector` not matching, no ready endpoints) |
| The new Web Engine pod cannot be scheduled (e.g. not enough memory) | `PodPending` with the scheduler message; the rollout keeps waiting until it is scheduled or `rolloutTimeout` rolls it back |

## Deploy an extension

1. **Build and push** (CI): `mvn package`, then `oras push` of the WAR with annotations (see
   [samples/16-java-portlet](../samples/16-java-portlet)). Use **immutable semver tags**.
2. **Declare**: a PR with the `DXJavaExtension` (or a new tag in `artifact.ref`).
3. **Apply**: Argo CD applies the CR (the CRs are GitOps; the DX Helm release is not).
4. The operator does the rest: resolve, rollout one pod at a time, registration, page, status.

```yaml
apiVersion: dx.europesip.com/v1alpha1
kind: DXJavaExtension
metadata: { name: europesip-showcase, namespace: my-portal }
spec:
  connectionRef: { name: dx }
  type: portlet
  artifact:
    ref: harbor.example.com/dx-artifacts/java-extensions/portlets/europesip-showcase-portlet:1.0.0
    pullSecrets: [ { name: registry-pull } ]
  contextRoot: /europesip-showcase
  page: { uniqueName: com.europesip.showcase.page, title: EUROPESIP Showcase, friendlyName: showcase }
```

Artifact annotations we use (`oras push --annotation`):

| Annotation | Example |
|---|---|
| `org.opencontainers.image.title / description / version / vendor / licenses / source / created` | the usual OCI metadata |
| `com.europesip.dx.kind` | `java-extension` |
| `com.europesip.dx.java.type` | `portlet` or `sharedLibrary` |
| `com.europesip.dx.java.jsr` | `286` |
| `com.europesip.dx.java.context-root` | `/europesip-showcase` |
| `com.europesip.dx.java.portlet-uids` | `ShowcasePortlet` |
| `com.europesip.dx.java.webapp-uid` | `com.europesip.showcase.ShowcasePortlet.webmod` (used when `portlet.xml` has no `id`) |
| `com.europesip.dx.java.min-cf` | `CF239` |

Registry layout we recommend: `<registry>/dx-artifacts/java-extensions/portlets/<name>:<semver>` and
`.../java-extensions/libraries/<name>:<semver>`.

## Same IDs in every environment

Pages, XMLAccess requests and content refer to a portlet by an identifier. For a promotion between environments
(development, staging, production) to work, that identifier must be **the same in every portal** (issue #69,
0.16.0):

| Identifier | Example | Same in every environment? |
|---|---|---|
| `uid` of the web module and of the portlet application | `com.europesip.showcase.ShowcasePortlet.webmod` | **Yes**: from `<portlet-app id>` in `portlet.xml` (or `spec.portletAppUID`) |
| **Unique name** of each portlet | `com.europesip.showcase.ShowcasePortlet.ShowcasePortlet` | **Yes** (new): `<portlet-app uid>.<portlet-name>`, set by the operator (helps your own XMLAccess requests; see the note below) |
| Object ID of each object | `Z3_4G9I0G01110000M8G31CLH0006` | **No**: DX generates it in each portal |

- The operator registers every portlet with its **unique name** (`spec.identifiers.uniqueNames`, default `true`).
  Portlets registered by an earlier version get it on the next reconcile, without restarting the Web Engine.
- In your XMLAccess requests and scripts, locate the portlet by its unique name (or by the `uid` of its application
  and its name), never by its object ID:

  ```xml
  <web-app action="locate" uid="com.europesip.showcase.ShowcasePortlet.webmod">
    <portlet-app action="locate" uid="com.europesip.showcase.ShowcasePortlet">
      <portlet action="locate" uniquename="com.europesip.showcase.ShowcasePortlet.ShowcasePortlet" objectid="showcase"/>
    </portlet-app>
  </web-app>
  <!-- … then portletref="showcase" in the page -->
  ```

- The status shows what DX has: `webModuleObjectID`, `portletAppObjectID` and `portletIDs` (name, unique name,
  object ID of each portlet), for diagnosis.
- **Fixed object IDs are not possible** for portlets registered this way: tested on DX Compose CF239, DX ignores
  the `objectid` of a predeployed web module, its portlet application and its portlets (with `create-oids="true"`
  too) and generates its own; with `action="create"` the request fails because the deployment already created the
  portlet application from `portlet.xml`. That is why the operator relies on `uid` and unique names.
- **Important — unique names do not replace stable object IDs.** In HCL DX the object ID is *the* identifier:
  release exports and imports, ReleaseBuilder, URLs with portlet IDs and references from content or rules use it, and
  the usual promotion between environments expects the same object in every environment to have the **same object
  ID**. Unique names only help when *your own* XMLAccess requests locate the portlet (as above). With this mode, the
  same portlet still gets a different object ID in each environment, which matters if you promote releases or pages
  that reference it by object ID. We are checking with HCL how to keep the object IDs identical (for example
  importing the reference environment's XML with `create-oids="true"` on the `<request>`); until then, plan your
  page promotion around unique names, or deploy that portlet with HCL's documented method. Thanks to Jordi Urrea
  (HCL) for the review.

## Two delivery modes: webhook (default) or startup hook

Since 0.17.0 (issue #72) the files of a `DXJavaExtension` reach the Web Engine in one of two ways
(`spec.delivery`). Everything else is the same in both: the artifact by digest, the rollout pod by pod with rollback,
the XMLAccess registration and the stable unique names.

| | `webhook` (default) | `startupHook` |
|---|---|---|
| How | The operator's admission webhook adds an init container to the Web Engine pod when it is created | A script on a volume mounted on the HCL customer startup folder of the image (`/opt/openliberty/wlp/usr/svrcfg/bin/customer/startup`) runs on every container start and downloads the extensions through the operator |
| Inside what the HCL chart supports | No: the pod is changed outside the chart values | Yes: the chart value `volumes.webEngine.customPVCs` plus the startup folder the HCL entrypoint runs |
| Needs | The webhook (certificate: cert-manager, OpenShift service CA or OLM) and the namespace label | A `customPVCs` entry on the portal (a `helm upgrade` of the HCL release) and the operator endpoint (port 8090, cluster-internal) |
| When the files arrive | Before the Web Engine container starts (init container) | During the container start, just before the final Liberty start |
| Registry credentials | Only in the init container | Only in the operator; the DX container gets a token valid for its own Web Engine |
| If loading fails | The init container fails, the pod does not start | The script stops the start (`abort_container`, exit 2); the container restarts |
| Several replicas | Nothing to share: every pod prepares itself | One volume for every replica (the chart creates **one PVC shared by all the Web Engine pods**): ReadWriteMany storage for more than one replica, or one replica with ReadWriteOnce |

**They cannot be used together on one portal.** All the extensions of one Web Engine use the same mode, and a
namespace with the webhook label (`dx.europesip.com/java-extensions=enabled`) cannot use `startupHook` (the webhook
would deliver the files too). The operator reports `DeliveryConflict` and changes nothing until it is fixed.

**Which one to choose**

- `webhook` (default): portals already installed that you do not want to `helm upgrade`, clusters where admission
  webhooks are normal (Istio, Vault), several replicas without RWX storage.
- `startupHook`: customers who want to stay strictly within the HCL chart values, OpenShift or clusters where a new
  admission webhook needs a security review, new portals (add the volume when you install).

### Set up the startup hook

1. Add the volume to the HCL chart values of the portal and `helm upgrade` it (real Helm, the HCL chart):

   ```yaml
   volumes:
     webEngine:
       customPVCs:
         - name: startup
           accessModes: ["ReadWriteMany"]     # ReadWriteOnce is enough with one Web Engine replica
           mountPath: /opt/openliberty/wlp/usr/svrcfg/bin/customer/startup
           storageClassName: <your storage class>
           requests: { storage: 100Mi }
   ```

2. The namespace must **not** have the label `dx.europesip.com/java-extensions=enabled`.
3. In every `DXJavaExtension` of that Web Engine: `spec.delivery: startupHook` (sample:
   `samples/16-java-portlet/dxjavaextension-startuphook.yaml`).

The operator writes a small script and a token on the volume once (a short job, as the user of the Web Engine;
`status.startupHookClaim` names the volume) and rolls the Web Engine out. On every start the HCL entrypoint runs the
script, which calls the operator (`http://<release>-java-extensions.<operator namespace>.svc:8090`), unpacks the
WARs into `resources/customApplications`, the JARs into `libraries/customSharedLib` and the Liberty configuration into
`configDropins/defaults`, and logs `[dx-java-extensions]` lines in the Web Engine log. Adding the volume to a running
portal restarts the Web Engine twice (the `helm upgrade`, then the rollout with the script).

### Change the mode of a portal

- **webhook → startupHook:** add the volume (`helm upgrade`), remove the namespace label, set `delivery:
  startupHook` in every extension of that Web Engine. One restart; the portlets stay registered.
- **startupHook → webhook:** label the namespace, set `delivery: webhook` (or remove the field) in every extension.
  The volume can stay (with no extension in that mode the script finds nothing and does nothing) or be removed with
  another `helm upgrade`.

## Enable it

```bash
helm upgrade dxop oci://ghcr.io/europesip/charts/dx-delivery-operator --reuse-values --set javaExtensions.enabled=true
kubectl label namespace <portal-namespace> dx.europesip.com/java-extensions=enabled
```

Needs a certificate for the webhook: cert-manager, or on OpenShift the service CA when there is no cert-manager
(`javaExtensions.certificate.provider`, see [OpenShift and other platforms](openshift.md)). Values: `javaExtensions.initImage.*` (the init image; `pullSecret` if it
comes from a private registry), `javaExtensions.webhook.failurePolicy`, `namespaceSelector`, `objectSelector`
(default: pods with `app=dx-deployment-web-engine`; add yours if the DX release has another name).

The startup hook mode is available with `javaExtensions.enabled=true` and `javaExtensions.startupHook.enabled` (default
`true`, port `javaExtensions.startupHook.port`, 8090); it needs no namespace label (see above).

## Support and limits

- The webhook changes the **pod** of the Web Engine (an init container, volumes, mounts). That is outside the values
  the HCL chart supports, so, strictly, that part is outside HCL support — the same as a Helm post-renderer. We have
  asked HCL (Idea) for chart values (`initContainers`/extra volumes, or `webEngine.extensions`) and for automatic
  registration of predeployed portlets, which would remove the need for it. The `startupHook` mode avoids changing
  the pod: it only uses the chart value `customPVCs` and the customer startup folder of the HCL image (documented in
  the image itself, `svrcfg/bin/customer/README.md`; we have asked HCL to confirm it as a supported extension point).
- A new version still needs a Web Engine restart (Liberty loads the WAR at start), as with a custom image.
- Phase 1: portlets and shared libraries. Later: properties files and filters (`propertiesFilesOverrides`, the CF239
  append), cosign verification, a persistent cache with digest check.
- DX Compose only (on traditional DX, DX deploys the WAR itself).

Fields: [API reference](reference.md#dxjavaextension).
