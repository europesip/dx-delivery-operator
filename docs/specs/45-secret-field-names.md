# Secret fields that say whose credentials they hold

Issue: #45 · Status: Implemented (branch 45-secret-field-names, on top of #44) · Release: next minor

## Summary
Every Secret field says whose credentials it holds, following `dxConnect.cwCredentialsSecret` /
`wasCredentialsSecret` of #44. Four fields renamed with a deprecation period.

## Current behaviour
`DXConnection.spec.credentialsSecret` (portal user, required), `caSecret`; `DXDatabase.spec.adminSecret` (required),
`adminSecretNamespace`, `secretName` (output). Read in `internal/controller/common.go`,
`dxconnection_controller.go` (Secret watch), `dxdatabase_controller.go`.

## Specification
- New fields: `portalCredentialsSecret`, `tlsCASecret` (DXConnection); `dbAdminCredentialsSecret`,
  `dbAdminCredentialsSecretNamespace`, `outputSecretName` (DXDatabase). Old ones optional and deprecated.
- CEL on the spec: one of new/old required for the portal and DB admin Secrets; new and old with different values
  rejected (all four pairs).
- Readers: new field first, then the old one (`PortalCredentials()`, `TLSCA()`, `DBAdminSecret()`, `Output()`).
- Old field in use: `Warning` event `DeprecatedField` once per generation, and the `Ready` message ends with the list.
- The Secret watch of DXConnection also covers `cwCredentialsSecret` and `wasCredentialsSecret`.

## Acceptance criteria
- [x] Old names keep working, with the warning event and the note.
- [x] New names work without warning.
- [x] New and old with different values rejected by the API server.
- [x] Unit tests for the fallbacks; docs EN/ES (`docs/secret-fields*.md`), samples, reference, CHANGELOG.

## Out of scope
Removing the old names; registry fields and `secretKeyRef`.

## Test plan
Unit tests; isolated test operator (namespace dxop-test) with existing CRs (old names) and new ones; DXDatabase
against a throwaway PostgreSQL in a test namespace.
