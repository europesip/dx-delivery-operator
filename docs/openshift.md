# OpenShift and other platforms

**English** · [Español](openshift.es.md)

Since 0.15.0 (issue #63) the same chart and the same values run on k3s, any other Kubernetes and **OpenShift** (default
`restricted-v2` SCC). **There is no OpenShift flag**: the chart only uses settings that every platform accepts and
detects at install time what it needs to detect.

> **Tested on k3s only.** The chart and the image take the security context and the particularities of OpenShift
> into account (no fixed UID, `restricted-v2`, the HCL DX namespace annotations, the OpenShift service CA), but the
> operator **has not been tested on OpenShift yet**. Feedback from OpenShift users is welcome (open an issue).

## What the chart does on every platform

| Topic | k3s and other Kubernetes | OpenShift |
|---|---|---|
| UID of the operator pod | the UID of the image (1000), because the chart sets none | the UID of the namespace range, given by the SCC |
| `runAsNonRoot`, `seccompProfile: RuntimeDefault`, `allowPrivilegeEscalation: false`, drop `ALL` | yes | yes (what `restricted-v2` asks for) |
| Writable paths | `/work`, `/tmp`, `/dxclient/store` are emptyDir volumes and `HOME=/tmp`, so any UID can write | same |
| Webhook certificate (only with `javaExtensions.enabled`) | cert-manager | cert-manager if it is installed, otherwise the **service CA of OpenShift** |
| Java extensions init container | runs as the user of the Web Engine pod | same |

Before 0.15.0 the chart fixed `runAsUser`, `runAsGroup` and `fsGroup` to 1000, which `restricted-v2` rejects in a
namespace with the default UID range. To keep the old behaviour on a cluster that needs it, set them in
`podSecurityContext`:

```yaml
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000
  seccompProfile: { type: RuntimeDefault }
```

## The namespace annotations of HCL DX

To install DX on OpenShift, HCL asks to create the **DX namespace** with three annotations
([HCL documentation](https://help.hcl-software.com/digital-experience/9.5/latest/deployment/install/container/helm_deployment/preparation/mandatory_tasks/prepare_namespace/)):

```yaml
metadata:
  annotations:
    openshift.io/sa.scc.mcs: "s0:c24,c4"
    openshift.io/sa.scc.supplemental-groups: "1001/10000"
    openshift.io/sa.scc.uid-range: "1000/10000"
```

They make `restricted-v2` accept UID 1000 and group 1001, the user of the DX pods (the HCL chart sets
`runAsUser: 1000`, `runAsGroup: 1001`, `fsGroup: 1001` at pod level). What it means for the operator:

- **The operator namespace does not need them** (for example `dxop-system`): the chart sets no UID, so OpenShift
  gives it the UID of its own range. If you install the operator in the DX namespace it gets UID 1000 from them,
  which also works.
- **The Java extensions init container** runs inside the Web Engine pod of the DX namespace and takes the UID and
  GID of that pod (1000/1001, inside the range of the annotations). If a Web Engine pod had no UID at pod level but
  its container had one, the webhook gives the init container the same one. It writes the WARs and JARs with modes
  0644/0755, so the Web Engine reads them with any UID.
- **The label of the Java extensions** (`dx.europesip.com/java-extensions=enabled`) is a label, not an annotation:
  add it to the DX namespace next to the HCL annotations.

## Webhook certificate

`javaExtensions.certificate.provider`:

| Value | What the chart creates |
|---|---|
| `auto` (default) | On OpenShift **without** cert-manager: the service CA (annotation `service.beta.openshift.io/serving-cert-secret-name` on the Service and `service.beta.openshift.io/inject-cabundle` on the MutatingWebhookConfiguration). Everywhere else: cert-manager, as before 0.15.0 |
| `cert-manager` | a cert-manager `Certificate` (and a self-signed `Issuer`, or the issuer of `javaExtensions.certManager`) |
| `openshift-service-ca` | the service CA annotations |

The Secret is always `<release>-webhook-cert` with `tls.crt` and `tls.key`, so the operator does not change.

**`helm template` and Argo CD**: there Helm cannot see the APIs of the cluster and `auto` means cert-manager. On
OpenShift without cert-manager set `javaExtensions.certificate.provider: openshift-service-ca` (or pass
`--api-versions route.openshift.io/v1`).

## OperatorHub (OLM bundle)

`python3 hack/olm-bundle.py` (or `make bundle`) generates an OLM bundle in `bundle/<version>/` from the chart, the
format of the community catalogs (OperatorHub.io and the OperatorHub embedded in OpenShift). Differences with the
chart:

- OLM gives the namespaces (OperatorGroup), the webhook certificate and the CRDs.
- **The HCL DXClient licence is accepted in the Subscription**, not by the bundle:
  `spec.config.env: [{name: DXCLIENT_ACCEPT_LICENSE, value: "true"}]`. Until then the operator does not run DXClient.
- The Java extensions webhook is always registered with `failurePolicy: Ignore`, because OLM sets the
  `namespaceSelector` itself: a Web Engine pod of a namespace without `DXJavaExtension` never waits for the operator.

Validate it with `bin/operator-sdk bundle validate bundle/<version> --select-optional suite=operatorframework`.

### Where it is published

- **OperatorHub.io**: published, [https://operatorhub.io/operator/dx-delivery-operator](https://operatorhub.io/operator/dx-delivery-operator) (first version 0.15.0, now 0.17.0, channel `alpha`).
- **OpenShift community catalog** (the OperatorHub embedded in OpenShift, catalogs v4.16 to v4.22): submitted with
  0.17.0, it passed the Red Hat pipeline and is **waiting for a maintainer approval**
  ([community-operators-prod#12110](https://github.com/redhat-openshift-ecosystem/community-operators-prod/pull/12110)). This page will be updated when it is merged.

### Install from OperatorHub.io

On a cluster with OLM (OperatorHub.io gives the commands to install OLM), create the Subscription with the HCL
DXClient licence accepted:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: dx-delivery-operator
  namespace: operators
spec:
  channel: alpha
  name: dx-delivery-operator
  source: operatorhubio-catalog
  sourceNamespace: olm
  config:
    env:
      - name: DXCLIENT_ACCEPT_LICENSE
        value: "true"
```

Without `DXCLIENT_ACCEPT_LICENSE=true` the operator installs but does not run DXClient. The **tested on k3s only**
warning at the top of this page applies to the bundle too.

## Tested

- k3s: the operator image with UID 1000 and with an arbitrary UID without group 1000 (1000680000, group 0, as
  OpenShift gives): DXClient writes its configuration and logs in `/dxclient/store` in both cases.
- `helm template` of every certificate mode; unit tests of the init container user.
- Not yet on a real OpenShift cluster.
