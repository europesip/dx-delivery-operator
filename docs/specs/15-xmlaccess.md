# DXXMLAccess: run XMLAccess requests against DX, once per change

Issue: #15 · Status: Implemented · Release: 0.5.0

## Summary
New resource `DXXMLAccess`. It runs XMLAccess requests of your own (inline, or files from Git, a URL, Harbor or a
ConfigMap) against the base portal or a virtual portal. It is the escape hatch for what has no typed resource yet, and
the base of exports, because the answers can be kept in a ConfigMap.

## Current behaviour
`internal/dx/client.go` `XMLAccess()` posts to `/wps/config` only, with XML that the operator builds for `DXPage`,
`DXTheme` and the virtual portals. No resource accepts XML from a user.

## Specification
- **Requests:** `spec.xml` (one) or `spec.source` plus optional `spec.files`. Exactly one of the two (CEL rule).
  Without `files`, every `*.xml` of the source runs, sorted by path. Paths are kept inside the source.
- **Variables:** `${NAME}` is replaced from `spec.variables`; unknown names are left as they are.
- **Checks before sending:** every request must be well-formed XML with `<request type="…">`. If one is not, the run
  fails with `InvalidRequest` and nothing is sent.
- **Dry run:** `dryRun: true` checks, lists the requests in `status.results` (`checked`) and ends with Ready
  (`DryRun`). It does not call DX.
- **Endpoint:** `/wps/config`, or `/wps/config/<virtualPortal.context>`. A request runs one at a time per DX (the
  shared lock) and has `timeout` (10m by default).
- **Errors:** the first failure stops the run unless `continueOnError`; later requests are `skipped`. The phase is
  `Failed` and the reason `RequestFailed`, or `SucceededWithErrors` when it went on. Otherwise the phase is
  `Succeeded` and the reason `Succeeded`.
- **Output:** `output.configMap` holds one key per request with DX's answer. The ConfigMap is owned by the resource
  (controller reference) and its keys come from the paths (`/` becomes `__`). Answers over 900 KiB in all are **never
  cut** (0.5.1): the ConfigMap then holds only `README` (where they are, or why they were not kept). Without
  `output.oci` the run is Failed with the reason `OutputTooLarge`; with it, the registry is written first and has
  them all, so the run keeps its result.
- **OCI output:** `output.oci` (repository, tag with `${DATE}`, push secrets) pushes the answers plus `results.json` as
  an artifact with one layer per file, titled like ORAS does. `status.outputRef` and `outputDigest`; event `OutputPushed`.
  A failed push makes the run Failed (`OutputFailed`), because the requests did run.
- **One-shot:** the hash is made of the spec (without suspend, dependsOn and cleanup), the source revision and the
  reconcile annotation. With the same hash and a finished phase, nothing runs.
- **Delete:** with `cleanup` the resource gets a finalizer, and the cleanup request (inline or a file of the source)
  runs if the resource ever ran (and was not a dry run). Errors are retried for 10 minutes, then reported with an
  event, and the finalizer is removed. Without `cleanup` there is no finalizer.

## Acceptance criteria
See the issue (#15).

## Out of scope
Native Git clone, a dry run inside DX, secrets in variables.

## Plan
- Types: `api/v1alpha1/dxxmlaccess_types.go`.
- Controller: `internal/controller/dxxmlaccess_controller.go`.
- `dx.Client.XMLAccessAt`.
- Docs: reference, how it works, the migration guide, sample 11.

## Test plan
- Unit tests:
  - variables, request checks, the file list and path safety, the hash, the output keys;
  - `XMLAccessAt` path for a virtual portal.
- Lab cluster: sample 11 (export of URL mappings) on dx-test, a failing request, the annotation re-run, and cleanup.

## Result on the lab cluster (2026-11-17, dx-test CF239)
- Sample 11: 21 URL mapping contexts exported, kept in the ConfigMap, which was deleted with the resource.
- Two files, the first failing (EJPXA0240E): the run stops; the second is `skipped`.
- Dry run: both files `checked`, DX not called.
- Virtual portal `dxopwcm`: ok. Cleanup on delete: event `CleanedUp`.
- OCI output: pushed to `harbor.k3s.europesip.com/dx-wcm/xmlaccess-test-url-mappings:dx-test-20261117-214132`, and
  read back as the source of a dry-run DXXMLAccess (`request.xml`, type update).
- An unrelated annotation does not run it again; `dx.europesip.com/reconcile` does.
