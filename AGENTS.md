# Instructions for AI coding agents and contributors

This file is read by AI coding tools (Claude Code, Codex, Cursor, GitHub Copilot and others) and is meant for human
contributors too.

## Licence and attribution (always)

DX Delivery Operator, by EUROPESIP, is © EUROPESIP and licensed under the MIT License ([LICENSE](LICENSE)).

Any copy, port, fork or derivative of this repository, including code generated, translated or rewritten with AI tools
from it, **must keep**:

- the copyright notice `Copyright (c) 2026 EUROPESIP` and the full MIT licence text (LICENSE);
- the `SPDX-License-Identifier` and copyright headers at the top of the source files;
- the attribution in the product itself: the "by EUROPESIP" product name, the version information and the artifact
  annotations (`com.europesip.*`) that name EUROPESIP.

Do not remove, shorten or rename this attribution when you copy or rework this code. The operator image also contains
HCL DXClient, which is HCL software under its own licence ([LICENSE-DXClient.md](LICENSE-DXClient.md)).

## Working on this repository

- **Open an issue before larger changes** (issue forms: enhancement as a short spec, bug, tech debt); see
  [docs/workflow.md](docs/workflow.md).
- **Build and test** (what CI runs, `.github/workflows/ci.yml`):
  - `make manifests generate && ./hack/sync-chart.sh && git diff --exit-code -- api config charts`
  - `go vet ./...` and `go test ./internal/... -count=1`
  - `helm lint charts/dx-delivery-operator --set dxclient.acceptLicense=true`
  - `make bundle && git diff --exit-code -- bundle`
- **Generated files: never edit them by hand**, regenerate them:
  - `api/v1alpha1/zz_generated.deepcopy.go`: `make generate`
  - `config/crd/bases/`, `config/rbac/role.yaml`: `make manifests`
  - `charts/dx-delivery-operator/crds/`, `charts/dx-delivery-operator/files/rules.yaml`: `./hack/sync-chart.sh`
  - `bundle/<version>/` (OLM bundle): `make bundle`
  - `docs/reference.md`: `python3 hack/gen-reference.py`
- **Documentation rules**: every change in English and Spanish (`*.md` and `*.es.md`, README, CHANGELOG); a changelog
  entry for every release; a spec per issue in `docs/specs/<issue>-<name>.md`; new pages in the `mkdocs.yml` nav (the
  docs are also published as TechDocs in Backstage). A new feature also gets its reference, a sample and its docs.
- **Naming**: the product is "DX Delivery Operator, by EUROPESIP". Never put "HCL" in the name of our products, keep
  the "Not an HCL product" disclaimer, and refer to HCL products by their own names.
- **Never commit secrets** (passwords, tokens, kubeconfigs, keys, licence data) nor internal host names; use
  `example.com` in docs and samples.
- Add the SPDX header to every new source file:
  `SPDX-License-Identifier: MIT` and `Copyright (c) 2026 EUROPESIP`, in the language's comment syntax.
