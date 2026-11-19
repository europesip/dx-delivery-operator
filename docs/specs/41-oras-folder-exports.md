# WCM import from OCI: folders pushed with `oras push <dir>`

Issue: #41 · Status: Implemented · Release: 0.10.1

## Summary
On 19-nov Sergio pushed CF230 exports with `oras push <ref> .\554ee7f5\ leeme.txt` (the export folder itself). ORAS
stores a folder as one tar+gzip layer whose entries start with the folder name, annotated
`io.deis.oras.content.unpack=true`. The operator unpacked it into `<dir>/<title>`, so the export was at
`554ee7f5/554ee7f5/423dc4e4`; the chunk zips kept that extra level and DX failed (SER3009E, the jurrea import failed in
6 s).

## Current behaviour (0.10.0)
`internal/source/source.go` `writeArtifactFiles` untars folder layers into `dir/<title>`.
`internal/wcmexport/wcmexport.go` `WriteZip` names the zip entries relative to the source root.

## Specification
1. A folder layer is unpacked as `oras pull` does: into `dir` (its entries carry the folder name).
2. The chunk zips start at the export folder as DX writes and reads it: two levels above the folder with the
   `<library>.node` files (`<workspace>/<root node>/`), whatever extra folders the source has.

## Acceptance criteria
- [x] An artifact pushed as a folder is unpacked once (`dir/554ee7f5/423dc4e4/...`), other layers as files.
- [x] An export inside extra folders gives zips whose entries start with `<workspace>/<root node>/`.
- [ ] Retest with Sergio's original artifacts on dx-basic-cf230.

## Test plan
Unit tests `TestORASFolderArtifact`, `TestWriteZipNestedExport`; real import on dx-basic-cf230.
