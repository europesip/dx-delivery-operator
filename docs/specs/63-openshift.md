# Run on OpenShift without a flag + OLM bundle

Issue: #63 · Status: Implemented · Release: 0.15.0 (2026-11-24)

## Summary
The operator chart runs unchanged on OpenShift (default `restricted-v2` SCC) as well as on k3s and other Kubernetes,
without an OpenShift-specific flag, and an OLM bundle is generated from the chart for the community catalogs.

## Current behaviour
- `templates/deployment.yaml` fixed `runAsUser`/`runAsGroup`/`fsGroup` 1000: rejected by `restricted-v2` in a
  namespace with the default UID range.
- DXClient writes `/dxclient/store` inside the image (world-writable today only because of how HCL builds it);
  `HOME=/home/dx_user` (mode 0700, UID 1000).
- `templates/webhook.yaml` always needed cert-manager.
- The init container of the Java extensions had no UID of its own (it inherits the pod UID of the HCL Web Engine pod,
  1000/1001, inside the UID range of the HCL namespace annotations for OpenShift).
- No OLM bundle.

## Specification
- Pod: `podSecurityContext` (default `runAsNonRoot`, seccomp `RuntimeDefault`) and `containerSecurityContext`
  (default no privilege escalation, drop `ALL`). No UID/GID/fsGroup by default.
- `HOME=/tmp`; emptyDir `dxclient-store` at `/dxclient/store` (16Mi).
- `javaExtensions.certificate.provider`: `auto` (default), `cert-manager`, `openshift-service-ca`; anything else fails.
  `auto` = `openshift-service-ca` when the cluster has `route.openshift.io/v1` or `security.openshift.io/v1` and not
  `cert-manager.io/v1`; `cert-manager` otherwise (also with `helm template`, the behaviour before 0.15.0).
  The Secret `<release>-webhook-cert` (`tls.crt`, `tls.key`) is the same in both modes.
- Webhook injection: when the pod has no `runAsUser` (`runAsGroup`, `runAsNonRoot`) at pod level and the Web Engine
  container has one, the init container gets it; otherwise nothing is set.
- `hack/olm-bundle.py` / `make bundle`: `bundle/<version>/manifests` (CRDs of the chart + CSV) and `metadata`
  (package `dx-delivery-operator`, channel `alpha`, `com.redhat.openshift.versions: v4.16`). The CSV takes the
  Deployment, ClusterRole and leader-election Role of the rendered chart; WATCH_NAMESPACES from
  `olm.targetNamespaces`; no `DXCLIENT_ACCEPT_LICENSE` (accepted in the Subscription `spec.config.env`); the webhook
  as a `webhookdefinition` with `failurePolicy: Ignore` (OLM sets the namespaceSelector); one `alm-examples` entry per
  kind from `samples/`.

## Acceptance criteria
- [x] Default values: no `runAsUser`/`runAsGroup`/`fsGroup` in the pod; on k3s the process is UID 1000.
- [x] Arbitrary UID without group 1000 (1000680000, group 0): DXClient writes its store and logs (k3s test pod).
- [x] `auto` renders cert-manager with `cert-manager.io/v1`, the service CA with only OpenShift APIs, cert-manager
      without APIs; an unknown provider fails.
- [x] Unit test: init container user from the Web Engine container when the pod has none.
- [x] `helm lint`, `go test ./internal/javaext/`, `operator-sdk bundle validate --select-optional suite=operatorframework`.
- [ ] Production upgrade: only the operator pod restarts (after checking no import/export is running).

## Out of scope
Red Hat certification; the PRs to the community catalogs; a real OpenShift test.

## Test plan
`helm template` of every provider; diff against 0.14.2 with the production values (only the security context, HOME
and the store volume change); arbitrary-UID pod on k3s; `make bundle`.
