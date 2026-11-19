# DXWCMLibraryImport resumes after an operator restart

Issue: #33 · Status: Implemented · Release: 0.9.1

## Summary
A restart of the operator during a chunked WCM import started it again from chunk 1, and deleting and recreating the
dxFileTransfer folder of the interrupted chunk made DX's own (still running) import of it fail. Imports must go on at
the chunk they were on.

## Current behaviour (0.9.0)
`internal/controller/dxwcmlibraryimport_controller.go`: every run recomputes the plan, starts at chunk 1, and before
uploading a chunk deletes its folder `dxop-<ns>-<name>-<chunk>` ("leftovers of a run that was cut").

## Specification
- While a run with chunks goes on, `status.resume` keeps: spec hash, source revision, the titles of each chunk, the
  chunks completed, the failed libraries of completed chunks, the planned libraries that existed before, when the import
  request of the next chunk was sent (`chunkStartedAt`), the longest chunk, and the attempt number.
- A run resumes when `status.resume` is for the same spec hash and source revision, is not finished, and it is not a dry
  run; the libraries to import must be exactly the planned ones (else a new plan).
- Completed chunks are skipped; their failures are kept.
- Interrupted chunk with `chunkStartedAt` set: `mode: ImportCopy` → its libraries fail with the reason (no duplicate);
  otherwise wait `1.5 × longestChunk` clamped to 3–30 min (5 min without a finished chunk) from `chunkStartedAt`, then
  import it again.
- dxFileTransfer folders are `dxop-<ns>-<name>-<chunk>-<attempt>`; no folder is deleted before upload; the folders of
  the previous attempt (and the 0.9.0 names) are deleted when the run ends.
- Messages say "(resumed at chunk N/M after a restart)"; event `Resumed`.
- `status.resume` is removed when the run ends (success, failure, dry run, nothing to do).

## Acceptance criteria
- [x] Given a plan at chunk 5/11 and a restart, the next run imports chunks 5–11 only.
- [x] A changed spec or export starts from scratch.
- [x] The folder DX may be reading is not deleted or reused while the run goes on.
- [x] Copy imports never re-import an interrupted chunk.

## Test plan
Unit tests (`importresume_test.go`): resumable, plan rebuild, wait. Lab: restart the operator during a chunked import.
