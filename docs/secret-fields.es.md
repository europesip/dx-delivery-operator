# Campos de Secrets: de quién es cada credencial

[English](secret-fields.md) · **Español**

Cada campo que apunta a un Secret dice de quién son las credenciales que guarda (issue
[#45](https://github.com/europesip/dx-delivery-operator/issues/45)). Se han renombrado cuatro campos; los
nombres antiguos quedan **obsoletos**, pero se siguen aceptando, así que los recursos que ya existen siguen
funcionando.

| Recurso | Campo | Qué guarda | Nombre obsoleto |
|---|---|---|---|
| DXConnection | `portalCredentialsSecret` | `username` y `password` del **usuario del portal DX** con el que entra el operador (XMLAccess, WCM, DAM, DXClient) | `credentialsSecret` |
| DXConnection | `tlsCASecret` | `ca.crt` para verificar el **certificado TLS** de DX | `caSecret` |
| DXConnection | `dxConnect.cwCredentialsSecret` | el administrador del **Config Wizard** (DX Core) | — (nuevo en #44) |
| DXConnection | `dxConnect.wasCredentialsSecret` | el administrador de **WebSphere** (DX Core) | — (nuevo en #44) |
| DXDatabase | `dbAdminCredentialsSecret` (+ `dbAdminCredentialsSecretNamespace`) | un administrador del **servidor de base de datos** que puede crear usuarios y bases de datos | `adminSecret` (+ `adminSecretNamespace`) |
| DXDatabase | `outputSecretName` | el Secret que **crea** el operador para el backend (engine, username, password, host, port, database, uri, jdbcUrl) | `secretName` |

No cambian, porque siguen las convenciones de Kubernetes: `pullSecrets` y `pushSecrets` (registros) y `secretKeyRef`
(entorno de una DXScriptApplication).

## Qué pasa con los nombres antiguos

- El operador lee el campo nuevo y, si no está, el antiguo: el comportamiento es el mismo.
- Un recurso que use un nombre antiguo recibe un evento `Warning` **`DeprecatedField`** (una vez por cambio de la
  especificación) y el mensaje de su condición `Ready` termina con `deprecated fields in use (still accepted, rename
  them): …`.
- El API server rechaza un recurso que ponga el nombre nuevo y el antiguo con valores **distintos**; con el mismo
  valor se acepta (útil mientras se migra).
- Los nombres antiguos se quitarán en una versión menor posterior, anunciada en el historial de versiones.

## Cómo migrar

```yaml
# antes
spec:
  credentialsSecret: { name: dx-admin }
  caSecret: { name: dx-ca }
# después
spec:
  portalCredentialsSecret: { name: dx-admin }
  tlsCASecret: { name: dx-ca }
```

```yaml
# DXDatabase, antes
spec:
  adminSecret: { name: dx-pg-superuser }
  adminSecretNamespace: dx-shared
  secretName: dxop-visits-db
# después
spec:
  dbAdminCredentialsSecret: { name: dx-pg-superuser }
  dbAdminCredentialsSecretNamespace: dx-shared
  outputSecretName: dxop-visits-db
```

Con `kubectl edit` o en Git: cambia el nombre de la clave y aplica. Los Secrets no cambian. Backstage genera los
nombres nuevos.
