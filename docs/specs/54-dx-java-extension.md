# DXJavaExtension: Java extensions from a registry onto the official Web Engine image

Issue: #54 · Status: Implemented (tested on a test portal, not released) · Release: 0.14.0

## Summary
Deploy custom Java extensions (phase 1: JSR 168/286 portlets and shared-library JARs) to the DX Compose Web Engine
without a custom image. The extension is an OCI artifact; the operator injects it into the official Web Engine pod at
creation time (mutating admission webhook + init container), rolls the Web Engine out, and registers the portlets in
DX with XMLAccess `predeployed` only when missing. Replaces the manual steps of HCL's Java Transition Module guide
(custom image per code change and per CF, `configOverrideFiles`, manual XMLAccess).

## Current behaviour
No resource deploys Java code. POC without the operator on 23-nov-2026 (portal `jtm-poc`): init container + Helm
post-renderer (the chart 2.50.0 has no `initContainers`/extra volumes values), XMLAccess once (internal notes
`notas/operador/JTM-ALTERNATIVE-POC.md`).

## Specification
- CRD `DXJavaExtension` (`dx.europesip.com/v1alpha1`, namespaced, short names `dxjx`, `dxjavaext`) in the portal
  namespace. Spec: `connectionRef`, `type` (`portlet` default | `sharedLibrary`), `artifact.ref` (+ `pullSecrets`),
  `fileName` (default `<name>.war` / `<name>.jar`), `contextRoot` (default `/<name>`), `target.statefulSet` (default
  `dx-deployment-web-engine`), `target.container` (default `web-engine`), `register` (default true), `portletAppUID`,
  `page` (`uniqueName`, `title`, `titles`, `parentUniqueName` default `ibm.portal.Home`, `friendlyName`, `anonymous`
  default true), `optional`, `rolloutTimeout` (default 30m).
- Status: `resolvedDigest`, `desiredRef`, `previousRef`, `rolledOutRef`, `failedRef`, `rolloutStarted`,
  `portletAppUID`, `portlets`, `registeredHash`, `observedGeneration`, conditions `Ready`, `ArtifactResolved`,
  `RolledOut`, `Registered`.
- Artifact: one file layer: a WAR (zip) or a tar/tar.gz of the expanded WAR (portlet); a JAR or a tar/tar.gz of JARs
  (shared library). Size limit 512 MiB. Annotations `com.europesip.dx.java.*` (see docs/java-extensions.md).
- Webhook (chart `javaExtensions.enabled`, default false): `MutatingWebhookConfiguration` on pod CREATE,
  `namespaceSelector` label `dx.europesip.com/java-extensions=enabled`, `objectSelector` `app In
  [dx-deployment-web-engine]`, `failurePolicy` Fail (configurable), cert-manager Certificate (self-signed Issuer of
  the release or an existing issuer). It injects, for every extension of the namespace targeting the pod's
  StatefulSet with a `desiredRef`: init container `dx-java-extensions` (env `DX_JAVA_EXTENSIONS`, JSON), emptyDir
  volumes `dx-jx-apps`, `dx-jx-config`, `dx-jx-libs` (only with shared libraries), Secret volumes of the pull secrets,
  mounts on `resources/customApplications`, `configDropins/defaults` (read-only) and `libraries/customSharedLib`, the
  annotation `dx.europesip.com/java-extensions` (`name@digest,...`) and, if set, the init image pull secret.
  Idempotent (an injected pod is left alone).
- Init container: pulls by digest, verifies manifest and file digests, unpacks, writes
  `configDropins/defaults/dx-java-extensions.xml`; exit 1 with a clear message when a required extension fails.
- Controller: namespace label check (`NamespaceNotEnabled`); resolve tag → digest before any change
  (`ArtifactNotFound`/`RegistryError`, no rollout); read `portlet.xml` (uid = `id` of `<portlet-app>`, else the
  annotation, else `spec.portletAppUID`; `InvalidArtifact`); a new digest → `previousRef`, `desiredRef`, status
  written first, then the StatefulSet pod-template annotation `dx.europesip.com/java-extensions-hash` (hash of every
  extension of that StatefulSet); rollout done when the StatefulSet is updated and every pod is Ready with the
  annotation `name@digest`; failure (init container exit ≠ 0, CrashLoopBackOff ≥ 2, or timeout) → rollback to
  `previousRef`, `failedRef` set until the spec changes; registration: XMLAccess export of `<uid>.webmod`, register
  (`predeployed`, page) only when missing, the portlet set differs, or a page is declared, and only when the
  registration hash changed; delete: unregister (web module + page) when registered, roll out without the extension.
- Chart values `enableControllers` and `leaderElectionID` (second instances).

## Acceptance criteria
- [x] Given a portlet artifact, when applied, then the Web Engine restarts once with the official image and the
      portlet is registered and visible on its page (test portal `jtm-poc`, showcase 1.0.0).
- [x] Given a new tag, then the Web Engine rolls out with the new digest and DX registration is not repeated.
- [x] Given a tag that does not exist, then no rollout happens and the status says why.
- [x] Given the webhook disabled (default), nothing changes for existing installations (chart renders the same plus
      two env vars).

## Out of scope
Properties files and filters (`propertiesFilesOverrides`, CF239 append), cosign verification, persistent cache with
digest check (phase 2). DX Core.

## Plan
`api/v1alpha1/dxjavaextension_types.go`, `internal/javaext` (artifact, unpack, portlet.xml, Liberty XML, XMLAccess,
injection), `internal/webhook`, `internal/controller/dxjavaextension_controller.go`, `cmd/dx-extensions-init`,
`Dockerfile.extensions-init`, chart `templates/webhook.yaml` + values + deployment, CI image matrix, docs, sample 16.
Risk: the pod mutation is outside the HCL chart's supported values (documented; Idea to HCL).

## Tasks
- [x] CRD, controller, webhook, init image, chart, RBAC
- [x] Unit tests (javaext, webhook)
- [x] Docs EN/ES, reference, mkdocs nav, README, CHANGELOG, sample
- [x] Test instance on `jtm-poc` (not prod)
- [x] Release 0.14.0 and prod upgrade (Andrés, 23-nov-2026)

## Test plan
`go test ./internal/...`, `helm lint` (on and off). Lab: a test instance of the operator (`dxop-jtm`, only namespace
`jtm-poc`, only the `dxjavaextension` controller, own lease and webhook) with the showcase portlet 1.0.0 → 1.1.0 and a
missing tag.

## Follow-up: 0.14.1 (#57)
Found while recording the demo video on prod 0.14.0: after a version bump the CR stayed `RollingOut` although the
pod was Ready. Fix: the controller watches the Web Engine pods and StatefulSet touched by the webhook (map to the
DXJavaExtensions of that StatefulSet), polls every 10 s while rolling out, bounds every reconcile (4 min) so a hung
call cannot block the worker, and the default rollout timeout is configurable (`javaExtensions.rolloutTimeout`).
Tests: `TestRolloutFailure`, `TestRolloutTimeoutDefaults`, `TestRequestsFor`; lab check on `jtm-poc` (1.0.2 → 1.1.2
→ 1.0.2 must reach Ready by itself).

## Follow-up: 0.14.2 (#61)
Seen on prod 0.14.0/0.14.1 (23-nov-2026, `jtm-poc`, workers out of memory): `WebhookNotActive` during the pod
replacement window and while the new pod was Pending, although the webhook had injected it. Fix: `jxRolloutState`
classifies the pods from facts (terminating; created from the current template by the java-extensions hash or the
update revision; injected; Unschedulable); `WebhookNotActive` only for a current pod without the injection, with
`diagnoseWebhook` (configuration serving the path, namespaceSelector vs namespace labels, ready EndpointSlices); new
reason `PodPending` with the scheduler message. Tests: `TestJXStateReplacementWindow`, `TestJXStatePodPending`,
`TestJXStateNotInjected`, `TestDiagnoseWebhook`.
