# WCM maintenance modules (DXWCMAdminTask)

**English** · [Español](wcm-admin-tasks.es.md)

The *WCM modules* page of DX Compose lists the modules that traditional DX runs with
`ConfigEngine run-wcm-admin-task-*`: **library import and export** (`DXWCMLibraryImport`, `DXWCMLibraryExport`),
the **member fixer** and the **workflow checker**. DX Compose has no ConfigEngine: it runs them over HTTP, with a
signed-in administrator, at `/wps/wcm/myconnect[/<virtual portal>]?MOD=<module>&...`. `DXWCMAdminTask` runs the last
two, once per spec, one library after the other.

| Task | Module (`MOD=`) | What it does | Traditional DX |
|---|---|---|---|
| `MemberFixer` | `MemberFixer` | Checks the users and groups that WCM items reference (renamed, deleted, other unique ID) and fixes or removes the references | `ConfigEngine.sh run-wcm-admin-task-member-fixer -Dlibrary=... [-Dfix=true]` |
| `WorkflowControlChecker` | `WorkflowControlChecker` | Re-applies the security of the workflow stages, reschedules pending workflow actions, repairs items without a workflow | the same URL on the portal (`/wps/wcm/myconnect?MOD=WorkflowControlChecker&...`) |

## Report first

Without `fix: true` (the default) DX runs in **report mode** and changes nothing. HCL warns that the fixes may not be
easy to undo: run a report, read it, then the same resource with `fix: true`.

## Fields

- `task`: `MemberFixer` or `WorkflowControlChecker`.
- `libraries` (titles) or `allLibraries: true` (read from WCM REST when the run starts). One request per library.
- `fix`, `restrictOn` (item types: Content, SiteArea, Workflow, Cmpnt...), `preserveDates`.
- `memberFixer`: `altDn` (update: the alternate DNs of `MemberFixerModule.properties`; remove), `invalidDn`
  (update: the portal administrator; remove), `mismatchedId` (update; remove), `realm`, `noRealmDn`.
- `workflowChecker`: at least one of `updateDocSecurity`, `rescheduleActions`, `repairInvalidWorkflow` (DX does nothing
  without one); `preserveSecurityInheritancePropagation` with `updateDocSecurity`.
- `virtualPortal`, `timeout` (per library, default 1h), `dependsOn`, `suspend`, `output.oci` (also push the report).

Only documented parameters are sent; no free query parameters. Names checked on CF239 where DX echoes them in its
answer (`preserve_dates`, `restrictOn`, `noRealmDn`).

## Result

- `status.libraries`: per library the summary DX prints (items processed, updated and saved; member references to
  remove or update; virtual members to update; errors) and `result` (ok, the error, or "No parameter is specified").
- ConfigMap `<name>-report` (owned by the resource): DX's answer for each library. **The per item detail is only in the
  log of the Web Engine** (`SystemOut`, logger `MemberFixerModule` / `PlutoUtilityModule`).
- With `output.oci`: an artifact with one file per library and `REPORT-INFO.json`, annotations `com.europesip.dx.*`
  (`kind: wcm-admin-report`).
- Ready reason `NeedsAttention` when a library failed or DX reported errors; `TaskFailed` when every library failed.

## Notes

- Each request holds the lock of the DX: it never runs at the same time as a data module import, an export or an
  XMLAccess request of the same DX.
- On DX Compose CF239 both modules are enabled as installed (checked on a basic portal): the WCMConfigService
  properties that traditional DX needs for the workflow checker (`connect.businesslogic.module.workflowcontrolchecker.*`)
  are not needed.
- Long runs through Traefik/HAProxy can be cut (HTTP 504): DX goes on, the library shows the cut. The in-cluster URL of
  the connection (0.9.2, `spec.internalURL`) avoids it.
- Run it again with the annotation `dx.europesip.com/reconcile` or a change of the spec.
