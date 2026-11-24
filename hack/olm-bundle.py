#!/usr/bin/env python3
# Copyright 2026 EUROPESIP. Licensed under the MIT License (see LICENSE).
"""Generate the OLM bundle (issue #63) from the Helm chart, so the chart stays the single source of truth.

    python3 hack/olm-bundle.py            # writes bundle/<version>/{manifests,metadata}
    bin/operator-sdk bundle validate bundle/<version> --select-optional suite=operatorframework

The bundle is the format of the community catalogs (OperatorHub.io: k8s-operatorhub/community-operators, and the
OperatorHub embedded in OpenShift: redhat-openshift-ecosystem/community-operators-prod). Differences with the chart:
- OLM installs it: the namespaces come from the OperatorGroup (olm.targetNamespaces), the webhook certificate from
  OLM (no cert-manager, no service CA), the CRDs from the bundle.
- The HCL DXClient licence is NOT accepted by the bundle: the user accepts it with the env
  DXCLIENT_ACCEPT_LICENSE=true in spec.config of the Subscription (until then the operator does not run DXClient).
- The Java extensions webhook is always registered, with failurePolicy Ignore: OLM cannot limit it to the namespaces
  with the label dx.europesip.com/java-extensions=enabled (it sets the namespaceSelector itself), and a Web Engine pod
  of a namespace without DXJavaExtension must never wait for the operator.
Needs: helm, PyYAML.
"""
import base64
import copy
import datetime
import json
import os
import pathlib
import shutil
import subprocess
import sys

import yaml

ROOT = pathlib.Path(__file__).resolve().parent.parent
CHART = ROOT / "charts" / "dx-delivery-operator"
NAME = "dx-delivery-operator"
SA = NAME
REPO = "https://github.com/europesip/dx-delivery-operator"
IMAGE = "ghcr.io/europesip/dx-delivery-operator"
OPENSHIFT_VERSIONS = "v4.16"  # this version and later (community-operators-prod catalogs v4.16-v4.22)

DESCRIPTION = """\
**DX Delivery Operator** delivers HCL DX applications and content from Git, declaratively, and keeps them in DX
exactly as declared (drift correction).

- **Script Applications**: pushes them to WCM (from an OCI image, a Git repository or a URL), with substitutions,
  dependencies on their backend, and their **pages** (`DXPage`) and **DAM collections** (`DXDamCollection`).
- **Backend databases** (`DXDatabase`): PostgreSQL, Oracle or SQL Server user and database for an application.
- **Moving content between portals** as OCI artifacts in your registry: WCM libraries (`DXWCMLibraryExport`,
  `DXWCMLibraryImport`), themes (`DXTheme`), pages and XMLAccess (`DXXMLAccess`), whole portal bundles (`DXExport`).
- **WCM maintenance modules** (`DXWCMAdminTask`) and **Java extensions** (`DXJavaExtension`: portlet WARs and
  shared-library JARs loaded onto the official DX Compose Web Engine image, without a custom image).

Works with HCL DX Compose and DX Core on Kubernetes, and with traditional DX on a virtual machine.

> **Tested on k3s only.** The operator takes the security context and the particularities of OpenShift into account
> (no fixed UID, `restricted-v2` SCC, the HCL DX namespace annotations), but it has **not been tested on OpenShift
> yet**. Please report your experience in https://github.com/europesip/dx-delivery-operator/issues.

### Before you start: accept the HCL DXClient licence

The operator image includes **HCL DXClient** (from HCL's public image `hclcr.io/dx-public/dxclient`), which is HCL
software under its own licence. Read it (`docker run --rm hclcr.io/dx-public/dxclient:<tag> /dxclient/bin/dxclient
accept-license`) and accept it in the Subscription:

```yaml
spec:
  config:
    env:
      - { name: DXCLIENT_ACCEPT_LICENSE, value: "true" }
```

Until then the operator runs but does not call DXClient (the resources report it).

### Java extensions

To use `DXJavaExtension` in a portal namespace, label it `dx.europesip.com/java-extensions=enabled`.

Documentation: https://github.com/europesip/dx-delivery-operator · Not an HCL product. HCL, HCL DX and HCL DX
Compose are trademarks of HCL Technologies.
"""

ALM_EXAMPLES = [
    {"apiVersion": "dx.europesip.com/v1alpha1", "kind": "DXConnection", "metadata": {"name": "dx"},
     "spec": {"url": "https://dx.example.com", "portalCredentialsSecret": {"name": "dx-admin"}}},
    {"apiVersion": "dx.europesip.com/v1alpha1", "kind": "DXScriptApplication", "metadata": {"name": "time-off"},
     "spec": {"connectionRef": {"name": "dx"},
              "source": {"image": {"ref": "ghcr.io/acme/time-off:2.3.0", "path": "/app"}},
              "wcm": {"title": "Time off"}}},
]


def alm_examples():
    """One example per kind: the first one of the samples/ folder (the DXConnection and DXScriptApplication above
    first), without namespace or status."""
    out = list(ALM_EXAMPLES)
    seen = {e["kind"] for e in out}
    for f in sorted((ROOT / "samples").glob("*/*.yaml")):
        for d in yaml.safe_load_all(f.read_text()):
            if not d or not str(d.get("apiVersion", "")).startswith("dx.europesip.com/") or d["kind"] in seen:
                continue
            d.pop("status", None)
            d.get("metadata", {}).pop("namespace", None)
            seen.add(d["kind"])
            out.append(d)
    return out


def render(version):
    out = subprocess.run(
        ["helm", "template", "dxop", str(CHART), "--namespace", "dx-delivery-operator",
         "--set", "dxclient.acceptLicense=true", "--set", "javaExtensions.enabled=true",
         "--set", "javaExtensions.certificate.provider=cert-manager", "--set", f"image.tag={version}"],
        check=True, capture_output=True, text=True).stdout
    by_kind = {}
    for d in yaml.safe_load_all(out):
        if d:
            by_kind.setdefault(d["kind"], []).append(d)
    return by_kind


def clean_labels(labels):
    return {k: v for k, v in (labels or {}).items()
            if k not in ("helm.sh/chart", "app.kubernetes.io/managed-by", "app.kubernetes.io/instance")}


def deployment(dep):
    spec = copy.deepcopy(dep["spec"])
    spec["selector"]["matchLabels"] = {"app.kubernetes.io/name": NAME}
    spec["template"]["metadata"]["labels"] = {"app.kubernetes.io/name": NAME}
    pod = spec["template"]["spec"]
    pod["serviceAccountName"] = SA
    pod["volumes"] = [v for v in pod["volumes"] if v["name"] != "webhook-cert"]  # OLM mounts its own certificate
    c = pod["containers"][0]
    c["volumeMounts"] = [m for m in c["volumeMounts"] if m["name"] != "webhook-cert"]
    env = []
    for e in c["env"]:
        if e["name"] == "DXCLIENT_ACCEPT_LICENSE":
            continue  # accepted by the user in the Subscription (spec.config.env)
        if e["name"] == "WATCH_NAMESPACES":
            e = {"name": "WATCH_NAMESPACES",
                 "valueFrom": {"fieldRef": {"fieldPath": "metadata.annotations['olm.targetNamespaces']"}}}
        env.append(e)
    c["env"] = env
    return {"name": NAME, "label": {"app.kubernetes.io/name": NAME}, "spec": spec}


def owned_crds(crd_dir):
    owned = []
    for f in sorted(crd_dir.glob("*.yaml")):
        crd = yaml.safe_load(f.read_text())
        kind = crd["spec"]["names"]["kind"]
        for v in crd["spec"]["versions"]:
            if v.get("served"):
                desc = (v.get("schema", {}).get("openAPIV3Schema", {}).get("description") or kind).strip()
                owned.append({"name": crd["metadata"]["name"], "version": v["name"], "kind": kind,
                              "displayName": kind, "description": desc.split("\n")[0][:300]})
    return owned


def webhook(mwc):
    w = mwc["webhooks"][0]
    return {
        "type": "MutatingAdmissionWebhook", "generateName": "java-extensions.dx.europesip.com",
        "deploymentName": NAME, "containerPort": 443, "targetPort": 9443,
        "webhookPath": w["clientConfig"]["service"]["path"], "admissionReviewVersions": w["admissionReviewVersions"],
        "sideEffects": w["sideEffects"], "failurePolicy": "Ignore", "timeoutSeconds": w["timeoutSeconds"],
        "reinvocationPolicy": w["reinvocationPolicy"], "rules": w["rules"], "objectSelector": w["objectSelector"],
    }


def main():
    chart = yaml.safe_load((CHART / "Chart.yaml").read_text())
    version = chart["appVersion"]
    k = render(version)
    dep = k["Deployment"][0]
    out = ROOT / "bundle" / version
    created = os.environ.get("CREATED_AT")
    old_csv = out / "manifests" / f"{NAME}.clusterserviceversion.yaml"
    if not created and old_csv.exists():  # keep the date of this version, so CI can check the bundle is up to date
        created = yaml.safe_load(old_csv.read_text())["metadata"]["annotations"].get("createdAt")
    created = created or datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    if out.exists():
        shutil.rmtree(out)
    (out / "manifests").mkdir(parents=True)
    (out / "metadata").mkdir()
    for f in sorted((CHART / "crds").glob("*.yaml")):
        shutil.copy(f, out / "manifests" / f.name)
    icon = base64.b64encode((ROOT / "docs" / "images" / "europesip-icon.svg").read_bytes()).decode()
    csv = {
        "apiVersion": "operators.coreos.com/v1alpha1", "kind": "ClusterServiceVersion",
        "metadata": {
            "name": f"{NAME}.v{version}",
            "annotations": {
                "alm-examples": json.dumps(alm_examples(), indent=2),
                "capabilities": "Basic Install", "categories": "Developer Tools,Integration & Delivery",
                "containerImage": f"{IMAGE}:{version}", "createdAt": created,
                "description": "Delivers HCL DX applications and content from Git and keeps them as declared.",
                "repository": REPO, "support": "EUROPESIP", "operators.operatorframework.io/builder": "hack/olm-bundle.py",
                "features.operators.openshift.io/disconnected": "false",
                "features.operators.openshift.io/fips-compliant": "false",
                "features.operators.openshift.io/proxy-aware": "false",
                "features.operators.openshift.io/tls-profiles": "false",
                "features.operators.openshift.io/token-auth-aws": "false",
                "features.operators.openshift.io/token-auth-azure": "false",
                "features.operators.openshift.io/token-auth-gcp": "false",
                "features.operators.openshift.io/cnf": "false",
                "features.operators.openshift.io/cni": "false",
                "features.operators.openshift.io/csi": "false",
            },
        },
        "spec": {
            "displayName": "DX Delivery Operator", "version": version, "maturity": "alpha",
            "description": DESCRIPTION, "minKubeVersion": "1.27.0",
            "icon": [{"base64data": icon, "mediatype": "image/svg+xml"}],
            "keywords": chart.get("keywords", []),
            "links": [{"name": "Documentation", "url": REPO},
                      {"name": "Release history", "url": REPO + "/blob/main/CHANGELOG.md"}],
            "maintainers": [{"name": "EUROPESIP", "email": "info@europesip.com"}],
            "provider": {"name": "EUROPESIP", "url": "https://www.europesip.com"},
            "installModes": [{"type": "OwnNamespace", "supported": True},
                             {"type": "SingleNamespace", "supported": True},
                             {"type": "MultiNamespace", "supported": True},
                             {"type": "AllNamespaces", "supported": True}],
            "customresourcedefinitions": {"owned": owned_crds(CHART / "crds")},
            "install": {"strategy": "deployment", "spec": {
                "clusterPermissions": [{"serviceAccountName": SA, "rules": k["ClusterRole"][0]["rules"]}],
                "permissions": [{"serviceAccountName": SA, "rules": k["Role"][0]["rules"]}],
                "deployments": [deployment(dep)],
            }},
            "webhookdefinitions": [webhook(k["MutatingWebhookConfiguration"][0])],
            "relatedImages": [
                {"name": "manager", "image": f"{IMAGE}:{version}"},
                {"name": "dx-extensions-init", "image": f"{IMAGE}/dx-extensions-init:{version}"},
            ],
        },
    }
    with open(out / "manifests" / f"{NAME}.clusterserviceversion.yaml", "w") as f:
        yaml.safe_dump(csv, f, sort_keys=False, width=120)
    # scorecard (basic + OLM suites): the community catalogs run it on every PR
    sc = {"apiVersion": "scorecard.operatorframework.io/v1alpha3", "kind": "Configuration",
          "metadata": {"name": "config"}, "stages": [{"parallel": True, "tests": [
              {"entrypoint": ["scorecard-test", t], "image": "quay.io/operator-framework/scorecard-test:v1.42.3",
               "labels": {"suite": suite, "test": t + "-test"}}
              for suite, t in [("basic", "basic-check-spec"), ("olm", "olm-bundle-validation"),
                               ("olm", "olm-crds-have-validation")]]}]}
    (out / "tests" / "scorecard").mkdir(parents=True)
    with open(out / "tests" / "scorecard" / "config.yaml", "w") as f:
        yaml.safe_dump(sc, f, sort_keys=False)
    annotations = {"annotations": {
        "operators.operatorframework.io.bundle.mediatype.v1": "registry+v1",
        "operators.operatorframework.io.bundle.manifests.v1": "manifests/",
        "operators.operatorframework.io.bundle.metadata.v1": "metadata/",
        "operators.operatorframework.io.bundle.package.v1": NAME,
        "operators.operatorframework.io.bundle.channels.v1": "alpha",
        "operators.operatorframework.io.bundle.channel.default.v1": "alpha",
        "com.redhat.openshift.versions": OPENSHIFT_VERSIONS,
    }}
    with open(out / "metadata" / "annotations.yaml", "w") as f:
        yaml.safe_dump(annotations, f, sort_keys=False)
    print(out.relative_to(ROOT))


if __name__ == "__main__":
    sys.exit(main())
