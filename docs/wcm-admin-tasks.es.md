# Módulos de mantenimiento de WCM (DXWCMAdminTask)

[English](wcm-admin-tasks.md) · **Español**

La página *WCM modules* de DX Compose lista los módulos que el DX tradicional ejecuta con
`ConfigEngine run-wcm-admin-task-*`: **importar y exportar bibliotecas** (`DXWCMLibraryImport`,
`DXWCMLibraryExport`), el **member fixer** y el **workflow checker**. DX Compose no tiene ConfigEngine: los ejecuta por
HTTP, con un administrador con sesión, en `/wps/wcm/myconnect[/<portal virtual>]?MOD=<módulo>&...`.
`DXWCMAdminTask` ejecuta los dos últimos, una vez por spec, biblioteca a biblioteca.

| Tarea | Módulo (`MOD=`) | Qué hace | DX tradicional |
|---|---|---|---|
| `MemberFixer` | `MemberFixer` | Revisa los usuarios y grupos a los que apuntan los elementos de WCM (renombrados, borrados, otro ID único) y corrige o quita las referencias | `ConfigEngine.sh run-wcm-admin-task-member-fixer -Dlibrary=... [-Dfix=true]` |
| `WorkflowControlChecker` | `WorkflowControlChecker` | Vuelve a aplicar la seguridad de las etapas de flujo, reprograma acciones pendientes, repara elementos sin flujo | la misma URL en el portal |

## Primero el informe

Sin `fix: true` (por defecto) DX trabaja en **modo informe** y no cambia nada. HCL avisa de que las correcciones pueden
no ser fáciles de deshacer: lanza un informe, léelo, y después el mismo recurso con `fix: true`.

## Campos

- `task`: `MemberFixer` o `WorkflowControlChecker`.
- `libraries` (títulos) o `allLibraries: true` (se leen por WCM REST al empezar). Una petición por biblioteca.
- `fix`, `restrictOn` (tipos de elemento: Content, SiteArea, Workflow, Cmpnt...), `preserveDates`.
- `memberFixer`: `altDn` (update: los DN alternativos de `MemberFixerModule.properties`; remove), `invalidDn`
  (update: el administrador del portal; remove), `mismatchedId` (update; remove), `realm`, `noRealmDn`.
- `workflowChecker`: al menos uno de `updateDocSecurity`, `rescheduleActions`, `repairInvalidWorkflow` (sin ninguno DX no
  hace nada); `preserveSecurityInheritancePropagation` con `updateDocSecurity`.
- `virtualPortal`, `timeout` (por biblioteca, 1 h por defecto), `dependsOn`, `suspend`, `output.oci`.

Solo se envían parámetros documentados. Nombres comprobados en CF239 donde DX los repite en su respuesta
(`preserve_dates`, `restrictOn`, `noRealmDn`).

## Resultado

- `status.libraries`: por biblioteca, el resumen que da DX (elementos procesados, actualizados y guardados; referencias
  a quitar o actualizar; miembros virtuales; errores) y `result`.
- ConfigMap `<nombre>-report`: la respuesta de DX de cada biblioteca. **El detalle por elemento solo está en el log del
  Web Engine** (`SystemOut`, `MemberFixerModule` / `PlutoUtilityModule`).
- Con `output.oci`: un artefacto con un fichero por biblioteca y `REPORT-INFO.json`, con anotaciones `com.europesip.dx.*`
  (`kind: wcm-admin-report`).
- Motivo `NeedsAttention` si una biblioteca falló o DX informó de errores; `TaskFailed` si fallaron todas.

## Notas

- Cada petición toma el bloqueo del DX: nunca coincide con una importación, una exportación o un XMLAccess del mismo DX.
- En DX Compose CF239 los dos módulos están activos tal como se instala (comprobado en un portal básico): no hacen falta
  las propiedades de WCMConfigService que el DX tradicional pide para el workflow checker.
- Las ejecuciones largas a través de Traefik/HAProxy pueden cortarse (HTTP 504): DX sigue, y la biblioteca lo indica. La
  URL interna de la conexión (0.9.2, `spec.internalURL`) lo evita.
