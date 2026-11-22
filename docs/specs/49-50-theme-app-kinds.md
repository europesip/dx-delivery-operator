# Artifact kinds `theme` and `app`

Issue: #49, #50 · Status: Implemented (operator part) · Release: 0.12.0

## Summary
A theme can only travel inside a `site` bundle and applications (Script Applications, portlets) have no artifact
kind. DX Transfer Studio 0.2.4 added the kind `theme` (folder `themes/`) and 0.2.5 adds `app` (folder `apps/`). The
operator and Backstage must read and, where they write, produce the same kinds.

## Current behaviour
`internal/source.NormalizeKind` mapped `theme`/`themes` to `site`; `DXExport` always wrote `kind: site` and left
`themes` out of `bundle.parts` even when `parts.themes` was exported.

## Specification
- Kinds `theme` and `app`; `NormalizeKind`: `theme`, `themes` → `theme`; `app`, `apps`, `application(s)` → `app`;
  `site`, `sites`, `dx-bundle`, `pages` → `site`. `FolderOf(kind)`: `wcm`, `sites`, `themes`, `apps`, `xmlaccess`,
  `portal`.
- `DXExport`: `bundle.parts` lists `themes` (first) when themes are exported; a bundle with only themes has
  `kind: theme` (manifest annotation and `bundle.json`); `com.europesip.dx.themes` on every bundle with themes.
- Annotations documented: `com.europesip.dx.themes`, `scriptapps`, `portlets`, `portlets.jsr`, `portlets.war`.
- `app` artifacts: recognised and documented; not imported by the operator yet (DX Transfer Studio imports them).
- Old theme-only bundles (`kind: site`, `bundle.parts: themes`) are not reclassified.

## Acceptance criteria
- [x] Given a DXExport with only `parts.themes`, the artifact has `kind: theme`, `bundle.parts: themes` and
  `com.europesip.dx.themes`.
- [x] Given themes and pages, `kind: site`, `bundle.parts: themes,pages` and `com.europesip.dx.themes`.
- [x] `NormalizeKind` and `FolderOf` unit tests.

## Out of scope
Importing `app` artifacts with the operator (Script Application zip → `DXScriptApplication`, portlets → WAR deploy +
`DXXMLAccess`): follow-up in #50. The destination image of a DXExport stays what the user writes.

## Tasks
- [x] internal/source kinds, annotations, FolderOf, KindOfBundle
- [x] DXExport parts and kind
- [x] docs EN/ES (artifact-annotations, portable-bundles), CHANGELOG

## Test plan
`go test ./...` (internal/source, internal/controller TestThemeOnlyBundleKind).
