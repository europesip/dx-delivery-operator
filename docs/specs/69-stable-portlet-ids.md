# DXJavaExtension: the same portlet identifiers in every environment

Issue: #69 · Status: Implemented · Release: 0.16.0 (unreleased)

## Summary
Register the portlets of a `DXJavaExtension` with identifiers that are the same in every environment, so pages,
XMLAccess requests and content that reference a portlet keep working when they move between portals. Raised by
Jordi Urrea (HCL).

## Current behaviour
`internal/javaext/xmlaccess.go` registered the web module and the portlet application by `uid` (stable, from
`portlet.xml`) and each portlet only by `name`, with a temporary request id; DX generated a different object ID in
each portal and the portlet had no unique name. `UnregisterXML` put `content-node` before `web-app`, which the
schema rejects (EJPXA0049E): deleting a resource with a page left everything in DX.

## Specification
- `spec.identifiers.uniqueNames` (default true): every portlet is registered with the unique name
  `<portlet-app uid>.<portlet-name>`. When an export shows a portlet without it (or with another one), the
  registration runs again (`action="update"`), without a Web Engine restart.
- Status: `webModuleObjectID`, `portletAppObjectID`, `portletIDs[]` (`name`, `uniqueName`, `objectID`), read from an
  export after every registration.
- The registration hash includes the identifiers, so changing them re-registers.
- `UnregisterXML`: `web-app` before `content-node`.
- Fixed object IDs (proposed in the issue) are **not implemented**: see the test below.

## Acceptance criteria
- [x] The same `DXJavaExtension` gives each portlet the same unique name in any portal (derived from `portlet.xml`).
- [x] An existing registration without unique names gets them on the next reconcile, without a restart (jtm-poc).
- [x] Status shows the unique names and the object IDs (jtm-poc, dev controller).
- [x] Delete order accepted by XMLAccess (jtm-poc).
- [ ] ~~Fixed object IDs identical in two portals~~: not possible, DX ignores them (below).

## Test (DX Compose CF239, portal jtm-poc, 24-nov-2026)
1. `update` of the existing showcase registration with `uniquename` on the portlet: result ok, object IDs unchanged,
   page 200; the portlet can be located with `<portlet action="export" uniquename="…"/>`.
2. Delete + register the hello portlet with `create-oids="true"` and its previous object IDs: result ok, but DX
   generated new IDs (Z1_…C00G5 instead of Z1_…H0086).
3. The same with never-used IDs: result ok, DX generated its own again.
4. The same with `action="create"`: fails, `EJPXA0043E` + duplicate key `PK20` on `APP_DESC` (the predeployed
   deployment already created the portlet application from `portlet.xml`), web module created with a generated ID.
5. An `update` whose portlet `objectid` belongs to another existing portlet: result ok, the located portlet keeps
   its ID, but the page `portletref` follows the given ID — one more reason never to send foreign object IDs.

## Out of scope
Migrating existing page references from object IDs to unique names.
