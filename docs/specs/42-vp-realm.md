# Virtual portal creation: realm and check that DX created it

Issue: #42 · Status: Implemented · Release: 0.10.1

## Summary
On dx-basic-cf230 (DX Compose CF239 with LDAP) `virtualPortal.create: true` sent realm `defaultWIMFileBasedRealm`.
DX rejected it (EJPEB0805E in the Web Engine log) but XMLAccess answered `result="ok"`; the operator then waited for
the WCM of the virtual portal with a library query that answers for any name (base portal fallback), so it reported
the portal created and imported the libraries into the base portal. With `FederatedRealm` the same request creates the
portal in 6 s.

## Current behaviour (0.10.0)
`internal/controller/dxwcmlibraryimport_controller.go` `createVirtualPortal`: realm = spec, else first existing VP,
else `defaultWIMFileBasedRealm`; trusts XMLAccess `ok`.

## Specification
1. Realms tried: `spec.virtualPortal.realm`; else the realm of an existing virtual portal; else `FederatedRealm`,
   then `defaultWIMFileBasedRealm`.
2. After each try, wait up to 90 s until the portal is in the PortalVPHandler export list; else the next realm.
3. None listed: the run fails ("set spec.virtualPortal.realm"), nothing imported. Event `VirtualPortalCreated` names
   the realm used.

## Acceptance criteria
- [x] Unit test of the realm order (`TestVPRealms`).
- [ ] Retest: aeat, jurrea, mussap created as virtual portals on dx-basic-cf230 and imported into them.

## Out of scope
Deleting virtual portals (XMLAccess `delete` answered ok without deleting `vprealmtest`).
