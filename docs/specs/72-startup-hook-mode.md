# DXJavaExtension: second delivery mode through the HCL customer startup folder

Issue: #72 · Status: Implemented · Release: 0.17.0 (unreleased)

## Summary
A second way to deliver the Java extensions that stays inside what the HCL chart and image support: a volume of the
chart value `volumes.webEngine.customPVCs` mounted on the Web Engine customer startup folder, whose scripts the HCL
entrypoint runs on every container start. Suggested by Jordi Urrea. The webhook stays the default.

## Current behaviour (before)
- Only the mutating admission webhook adds an init container to the Web Engine pod (outside the chart values).
- CF239 image (read-only check on a running Web Engine): `svrcfg/entrypoint/entryPoint.sh` calls
  `execute_custom_scripts /opt/openliberty/wlp/usr/svrcfg/bin/customer/startup` (`scripts/utility.sh`) after its own
  configuration and before `startServerIfStopped`; every `*.sh` there runs as `dx_user` (uid 1000, gid 1001); exit
  code 2 stops the start (`abort_container`). `customer/update` is run by `applyCF.sh`. The image has `curl` and
  `wget`, not `oras`. `resources/customApplications`, `libraries/customSharedLib` and `configDropins` are owned by
  `dx_user` (writable).
- HCL chart 2.50.0: `customPVCs` creates **one PVC per entry, shared by every Web Engine replica**
  (`<release>-web-engine-custom-<name>`), access modes configurable.

## Specification
- `spec.delivery`: `webhook` (default) | `startupHook`. All the extensions of one StatefulSet use the same mode.
- Conflicts → Ready=False `DeliveryConflict`, nothing changes: `startupHook` in a namespace with the label
  `dx.europesip.com/java-extensions=enabled`; extensions of one StatefulSet with different modes.
- `startupHook` needs a volume mounted (no subPath) on `StartupHookDir` in the target container → else
  `StartupVolumeMissing` with the values to add. The claim is in `status.startupHookClaim`.
- Seeding: the operator writes `dx-java-extensions.sh` (0750) and `dx-java-extensions.token` (0640) with a Job
  (`dx-jx-seed-<sts>-<hash>`, init image, command `seed`, the UID/GID/fsGroup of the Web Engine pod, pinned to the
  node of a running Web Engine pod for RWO volumes), then annotates the PVC with the hash
  (`dx.europesip.com/java-extensions-startup-hook`). Reasons `SeedingStartupVolume`, `SeedFailed`.
- Endpoint (every replica of the operator, not leader-only): `GET /dx-java-extensions/v1/bundle?namespace=&statefulset=&pod=`
  with `Authorization: Bearer <HMAC-SHA256(key, namespace/statefulset)>`; key in a Secret of the operator namespace
  created on first start. 401 bad token, 409 webhook label present, 204 no extensions, 502 a required extension
  failed, 200 a tar.gz (`apps/<file>/…` exploded WARs, `libs/*.jar`, `config/dx-java-extensions.xml`, `SUMMARY`).
  The operator pulls with the pull secrets of the namespace; on success it annotates the pod with
  `dx.europesip.com/java-extensions` (name@digest), as the webhook does.
- Script: sources `utility.sh`, logs with `log_message_customer` (`[dx-java-extensions]`), retries the operator 12×10 s,
  removes what an earlier start wrote, unpacks into the three folders, `abort_container`/exit 2 on errors.
- Rollout hash = summary of the extensions + (for startupHook) mode summary + seeded hash: a mode change or a new
  script restarts the Web Engine; webhook-only portals keep the previous hash (no restart on upgrade).
- Rollout state with startupHook: a current pod without the annotation is starting (RollingOut), Ready without it is
  `StartupHookNotRun`, a Web Engine container in CrashLoopBackOff (≥2 restarts) fails the rollout (rollback).
- Chart: `javaExtensions.startupHook.enabled` (true), `.port` (8090), Service `<release>-java-extensions`, env
  `JAVA_EXTENSIONS_STARTUP_HOOK*`, `POD_NAMESPACE`.

## Acceptance criteria
- [x] Default unchanged (webhook); the rollout hash of webhook-only portals is unchanged.
- [x] With the startup volume and `startupHook`, the portlet is loaded and registered without the webhook; a version
      change rolls out and the previous version comes back (lab, jtm-poc).
- [x] Both modes on one portal → `DeliveryConflict` (namespace label; mixed extensions).
- [x] No registry credential in the DX pod (only the token on the volume).
- [x] Unit tests (token, volume detection, script, seed files, bundle, rollout state, job name).

## Out of scope
HCL confirmation that `customer/startup` is a supported extension point (asked separately); TLS on the endpoint
(cluster-internal HTTP, integrity by digest in the operator, per-Web-Engine token).

## Test plan
Unit tests; lab on jtm-poc with a second operator instance limited to jtm-poc (kike scaled down while testing).
