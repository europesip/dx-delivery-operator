# List the chart on Artifact Hub

Issue: #77 · Status: Implemented · Release: next chart version

## Summary
List the Helm chart on Artifact Hub with a useful page (README, icon, images, links, CRDs, changelog) and the
"Verified publisher" label.

## Specification
- `Chart.yaml`: `icon`; annotations `artifacthub.io/{license,category,operator,operatorCapabilities,prerelease,
  containsSecurityUpdates,links,maintainers,images,changes,crds}`.
- `charts/dx-delivery-operator/README.md` (+ `.es.md`) packaged with the chart.
- `hack/artifacthub/artifacthub-repo.yml` pushed to `ghcr.io/europesip/charts/dx-delivery-operator:artifacthub.io`
  with oras once the repository ID is known.
- Release checklist in `docs/artifact-hub.md`.

## Acceptance criteria
- [x] `helm lint` passes; the packaged chart contains `README.md`.
- [ ] The Artifact Hub page shows README, icon, images, links, CRDs and changes after the next published version.
- [ ] "Verified publisher" after pushing `artifacthub-repo.yml`.

## Out of scope
Other charts; Official status; creating the Artifact Hub account (Andrés).
