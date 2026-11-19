# WCM maintenance modules: member fixer and workflow checker (DXWCMAdminTask)

Issue: #39 · Status: Implemented · Release: 0.10.0

## Summary
The *WCM modules* page of DX Compose (CF239) lists Workflow checker, Library import/export and Member fixer, which
traditional DX runs with ConfigEngine `run-wcm-admin-task-*`. Import/export exist (DXWCMLibraryImport/Export); this adds
the member fixer and the workflow checker, run over HTTP with a signed-in administrator, report mode by default.

## Current behaviour
Only by hand: sign in to the portal and open `/wps/wcm/myconnect?MOD=...` in a browser; long runs time out.

## Specification
- CRD `DXWCMAdminTask`: `task` (MemberFixer | WorkflowControlChecker), `libraries` or `allLibraries`, `fix` (default
  false), `restrictOn`, `preserveDates`, typed `memberFixer` / `workflowChecker` options (documented ones only),
  `virtualPortal`, `timeout`, `output.oci`, `dependsOn`, `suspend`.
- One GET per library on `/wps/wcm/myconnect[/<vp>]?MOD=<task>&library=...`, holding the lock of the DX; runs once per
  spec hash (again with `dx.europesip.com/reconcile`).
- Status: per library summary (parsed from "SUMMARY INFORMATION"), report ConfigMap, artifact ref.
- Validation: options of the other task, workflow checker without an action, libraries and allLibraries together → InvalidSpec.

## Acceptance criteria
- [x] Report mode never sends `fix`.
- [x] The parameter names DX echoes are the ones sent (verified on demorbita, CF239: `preserve_dates`, `restrictOn`, `noRealmDn`).
- [x] A workflow checker without an action is refused before calling DX.
- [x] The summary counters are read from the real answers of CF239 (unit tests).

## Verified on CF239 (demorbita, 19-nov-2026, read-only)
- `MOD=MemberFixer&library=orbita`: HTTP 200 in 13 s, "Log for Utility: Member Fixer Module", options echoed, SUMMARY
  INFORMATION (Items Processed: 9 ...), "Utility completed."; per item lines only in SystemOut.
- `MOD=WorkflowControlChecker&library=orbita` without an action: "No parameter is specified, please use
  'rescheduleActions' ... and/or 'updateDocSecurity' ...". The module is enabled in Compose as installed.
- Not verified on purpose (they would change DX): `fix=true`, `altDn`, `invalidDn`, `mismatchedId`, the workflow actions.

## Out of scope
Custom DN mappings (`MemberFixerModule.properties` in the Web Engine: Helm `propertiesFilesOverrides`), the workflow
update tool, a Backstage card (next step in the plugin).
