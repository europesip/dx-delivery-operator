# Rename the product to DX Delivery Operator

Issue: #52 · Status: Implemented · Release: 0.13.0

## Summary
The product "HCL DX Script Application operator" becomes **DX Delivery Operator** (by EUROPESIP). It started as a
Script Application deployer; today it also moves WCM libraries, pages, themes, XMLAccess, DAM collections, WCM admin
tasks, databases and portal bundles. The new name is broader, keeps the focus on delivering applications and removes
"HCL" from the product name (HCL is named only as the platform).

## Current behaviour
Display name in README, docs, mkdocs, chart, catalog-info; technical name `hcl-dx-scriptapp-operator` in the
repository, Go module, image (`ghcr.io/europesip/hcl-dx-scriptapp-operator`), chart, kustomize scaffolding,
`tool.operator` annotation, `EXPORT-INFO.txt`, event recorder and leader-election lease. Chart resource names derive
from `.Chart.Name` (`dxop-hcl-dx-scriptapp-operator`).

## Specification
- Display name "DX Delivery Operator" everywhere except the CHANGELOG history; README/docs intros say "formerly HCL DX
  Script Application operator".
- Go module `github.com/europesip/dx-delivery-operator`; image `ghcr.io/europesip/dx-delivery-operator`; chart
  `charts/dx-delivery-operator` (0.13.0).
- Compatibility for 0.13.x: CI also pushes the operator image as `ghcr.io/europesip/hcl-dx-scriptapp-operator` and a
  deprecated alias chart `hcl-dx-scriptapp-operator` built by `hack/alias-chart.sh` (same chart, old name,
  `deprecated: true`, `nameOverride: hcl-dx-scriptapp-operator`). Both are removed in 0.15.0.
- New values `nameOverride` / `fullnameOverride` (standard Helm semantics) so an existing release can keep its names.
- Unchanged: API group `dx.europesip.com`, kinds, CRDs, finalizers, labels, leader-election ID
  `hcl-dx-scriptapp-operator.europesip.com` (old and new pods share the lease during an upgrade), release `dxop`,
  namespace `dxop-system`.
- `com.europesip.dx.tool.operator` and `EXPORT-INFO.txt` say `dx-delivery-operator <version>`; readers must not
  depend on the operator name in that value (DX Transfer Studio and the Backstage plugin only display it).

## Acceptance criteria
- [x] No product name "Script Application operator" left outside the CHANGELOG history and migration notes.
- [x] `go build`, `go vet`, `go test ./internal/...`, sample build, `helm lint` of both charts pass.
- [x] `helm template dxop <alias chart>` renders the 0.12.x names (`dxop-hcl-dx-scriptapp-operator`); the new chart
  renders `dxop-dx-delivery-operator`, or the old names with `nameOverride`.
- [ ] Prod `dxop` upgraded from 0.12.0 to 0.13.0 with no CR/CRD change (after merge, tag and Andrés's OK).
- [ ] GitHub repository and project board renamed (Andrés's OK).

## Out of scope
API group or kind changes; functional changes.

## Plan
`git mv` the chart, rename references with sed (excluding the CHANGELOG history and the lease ID), rewrite the
intros, add the overrides, the alias-chart script and the CI steps, CHANGELOG 0.13.0 EN/ES with upgrade notes.
Order of the release: rename the GitHub repo → merge → tag v0.13.0 → upgrade prod.

## Test plan
Unit tests and helm checks in CI; on the lab cluster, `helm upgrade dxop` from 0.12.0 to 0.13.0 with and without
`nameOverride`, checking that CRs keep reconciling and that only one pod holds the lease.
