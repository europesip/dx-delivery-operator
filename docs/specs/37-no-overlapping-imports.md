# WCM import: never overlap chunks after a proxy cut; internalURL for the long requests

Issue: #37 · Status: Implemented · Release: 0.9.2

## Summary
On 19-nov an import of 48 libraries into a DX Compose on Derby (namespace kike) ran 11 chunks. The data-module request
of several chunks was cut by a proxy (Traefik/HAProxy, HTTP 504) while DX went on importing; the operator started the
next chunk at once, so two imports ran in DX at the same time and Derby deadlocked (`SQLTransactionRollbackException`:
11 libraries really failed). The 31 libraries of the cut chunks, which DX did import, were reported failed.

## Current behaviour (0.9.1)
`internal/controller/dxwcmlibraryimport_controller.go`: a cut (`dx.ErrCutByProxy`) marks the libraries of the chunk as
failed, keeps its dxFileTransfer folder and goes on with the next chunk immediately. All requests use `spec.url`.

## Specification
1. **No overlap.** After a cut the operator does not start the next chunk until DX should have finished the cut one.
   DX gives no "done" signal over HTTP (its progress is only in the Web Engine log), so the wait is estimated:
   `1.5 × files of the chunk ÷ speed − time already spent`, between 3 and 90 minutes. The speed is the one of the uncut
   chunks of the run (files ÷ seconds), or 4 files/s when there is none (DX on a busy Derby did ~6). The status message
   and an event (`ChunkCut`) say how long and until when.
2. **internalURL.** `DXConnection.spec.internalURL` (optional, `^https?://[^/]+/?$`): in-cluster URL of the Web Engine,
   e.g. `http://dx-deployment-web-engine.<ns>.svc:9080`. The long requests go there: WCM data module import and
   export, dxFileTransfer create/upload/download/delete. Everything else keeps `spec.url`. The session is made there
   with the form login of the portal (the Ring API is not behind that service): Liberty sets `LtpaToken2` without a
   domain, so the cookie works on the internal host (checked on CF239, namespace kike). It shares the lock of the
   connection (XMLAccess and data module never run at once). The DXConnection check signs in through it too; if it
   fails the condition says so and the long requests use `spec.url`; an unreachable internalURL (no such host,
   connection refused) also falls back to `spec.url` per request.
3. **Cut chunks are checked, not failed.** At the end, a library of a cut chunk is *imported* if DX lists it and it is
   new in the target or its `updated` (WCM REST) is after the start of its chunk (a warning says the request was cut);
   *failed* if DX does not list it, or if it existed and was not updated (DX does not say whether it finished
   overwriting it).

## Acceptance criteria
- [x] With internalURL, dxFileTransfer and the data module go to it; with an unreachable one, to `spec.url` (unit test).
- [x] `cutWait` gives the estimate and its bounds (unit test).
- [x] `updated` of a library is read from WCM REST (unit test).
- [ ] Lab: a DXConnection with internalURL imports a large export without any 504.

## Out of scope
Exports (`DXWCMLibraryExport`, `DXExport`) only get internalURL (a cut export still fails the run). Reading the Web
Engine log to know when DX is done (needs pod log rights in every DX namespace and a DX in the same cluster).

## Test plan
`go test ./...`; in the lab, set internalURL on a test connection and import a large export.
