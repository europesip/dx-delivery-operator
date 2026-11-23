# Secret fields: whose credentials each one holds

**English** · [Español](secret-fields.es.md)

Every field that points to a Secret says whose credentials it holds (issue
[#45](https://github.com/europesip/dx-delivery-operator/issues/45)). Four fields were renamed; the old names are
**deprecated** but still accepted, so existing resources keep working.

| Resource | Field | Holds | Deprecated name |
|---|---|---|---|
| DXConnection | `portalCredentialsSecret` | `username`, `password` of the **DX portal user** the operator signs in with (XMLAccess, WCM, DAM, DXClient) | `credentialsSecret` |
| DXConnection | `tlsCASecret` | `ca.crt` to verify the **TLS certificate** of DX | `caSecret` |
| DXConnection | `dxConnect.cwCredentialsSecret` | the **Config Wizard** administrator (DX Core) | — (new in #44) |
| DXConnection | `dxConnect.wasCredentialsSecret` | the **WebSphere** administrator (DX Core) | — (new in #44) |
| DXDatabase | `dbAdminCredentialsSecret` (+ `dbAdminCredentialsSecretNamespace`) | an administrator of the **database server** that can create users and databases | `adminSecret` (+ `adminSecretNamespace`) |
| DXDatabase | `outputSecretName` | the Secret the operator **creates** for the backend (engine, username, password, host, port, database, uri, jdbcUrl) | `secretName` |

Unchanged, because they follow the Kubernetes conventions: `pullSecrets` and `pushSecrets` (registries) and
`secretKeyRef` (environment of a DXScriptApplication).

## What happens with the old names

- The operator reads the new field and, when it is missing, the old one: the behaviour is the same.
- A resource that uses an old name gets a `Warning` event **`DeprecatedField`** (once per change of the spec) and
  its `Ready` condition message ends with `deprecated fields in use (still accepted, rename them): …`.
- The API server rejects a resource that sets the new and the old name with **different** values; with the same
  value it is accepted (handy while migrating).
- The old names will be removed in a later minor version, announced in the release history.

## Migrating

```yaml
# before
spec:
  credentialsSecret: { name: dx-admin }
  caSecret: { name: dx-ca }
# after
spec:
  portalCredentialsSecret: { name: dx-admin }
  tlsCASecret: { name: dx-ca }
```

```yaml
# DXDatabase, before
spec:
  adminSecret: { name: dx-pg-superuser }
  adminSecretNamespace: dx-shared
  secretName: dxop-visits-db
# after
spec:
  dbAdminCredentialsSecret: { name: dx-pg-superuser }
  dbAdminCredentialsSecretNamespace: dx-shared
  outputSecretName: dxop-visits-db
```

With `kubectl edit` or in Git: rename the key and apply. The Secrets themselves do not change. Backstage generates
the new names.
