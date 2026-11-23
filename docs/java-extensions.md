# Java extensions on DX Compose (DXJavaExtension)

**English** · [Español](java-extensions.es.md)

> **Video (7 min):** [Java portlets on HCL DX Compose without a custom image](https://youtu.be/ncVQlSORBOg) —
> the HCL Java Transition Module, the Java code it covers, and a live DXJavaExtension demo (English, subtitles EN/ES).

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
| The new version breaks the start (init container fails, not Ready before `rolloutTimeout`) | The operator **rolls back** to the previous digest (`RolledBack`), and does not try that digest again until the spec changes |
| The webhook is down | `failurePolicy: Fail` (default): the Web Engine pod is not created until it answers (no Web Engine without its extensions). `Ignore`: it starts without them. Run the operator with 2 replicas if this matters |
| The webhook is not enabled for the namespace | The controller does not roll out: `NamespaceNotEnabled` (missing label) or `WebhookNotActive` |

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

## Enable it

```bash
helm upgrade dxop oci://ghcr.io/europesip/charts/dx-delivery-operator --reuse-values --set javaExtensions.enabled=true
kubectl label namespace <portal-namespace> dx.europesip.com/java-extensions=enabled
```

Needs cert-manager (the webhook certificate). Values: `javaExtensions.initImage.*` (the init image; `pullSecret` if it
comes from a private registry), `javaExtensions.webhook.failurePolicy`, `namespaceSelector`, `objectSelector`
(default: pods with `app=dx-deployment-web-engine`; add yours if the DX release has another name).

## Support and limits

- The webhook changes the **pod** of the Web Engine (an init container, volumes, mounts). That is outside the values
  the HCL chart supports, so, strictly, that part is outside HCL support — the same as a Helm post-renderer. We have
  asked HCL (Idea) for chart values (`initContainers`/extra volumes, or `webEngine.extensions`) and for automatic
  registration of predeployed portlets, which would remove the need for it.
- A new version still needs a Web Engine restart (Liberty loads the WAR at start), as with a custom image.
- Phase 1: portlets and shared libraries. Later: properties files and filters (`propertiesFilesOverrides`, the CF239
  append), cosign verification, a persistent cache with digest check.
- DX Compose only (on traditional DX, DX deploys the WAR itself).

Fields: [API reference](reference.md#dxjavaextension).
