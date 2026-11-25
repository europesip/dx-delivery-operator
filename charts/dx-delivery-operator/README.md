<!-- SPDX-License-Identifier: MIT -->
<!-- Copyright (c) 2026 EUROPESIP -->
# DX Delivery Operator, by EUROPESIP

**English** · [Español](https://github.com/europesip/dx-delivery-operator/blob/main/charts/dx-delivery-operator/README.es.md)

A Kubernetes operator that delivers HCL DX applications and content from Git, declaratively, and keeps them in DX
exactly as declared (drift correction): Script Applications in WCM, their pages and DAM files, the database of their
backend, WCM libraries, themes, pages, XMLAccess and whole portal bundles moved between portals as OCI artifacts, WCM
maintenance modules and Java extensions (portlets, shared libraries) on the official DX Compose Web Engine image.
Works with HCL DX Compose and DX Core on Kubernetes (tested on k3s; designed for OpenShift, not yet tested there) and
with traditional DX on a virtual machine.

## Prerequisites

- Kubernetes 1.28+ and Helm 3.8+.
- The operator image includes **HCL DXClient**, HCL software under its own licence: read it and accept it with
  `dxclient.acceptLicense=true`, or the chart refuses to install.
- A DX user (editor of the Script Application Library; a DX administrator for pages, DAM and XMLAccess).

## Install

```bash
helm upgrade --install dxop oci://ghcr.io/europesip/charts/dx-delivery-operator \
  -n dxop-system --create-namespace --set dxclient.acceptLicense=true
```

Then, in the namespace of your applications: a Secret with the DX user, a `DXConnection` and your resources
(`DXScriptApplication`, `DXPage`, …). Step by step and samples: [documentation](https://github.com/europesip/dx-delivery-operator#readme).

## Main values

| Value | Default | What it does |
|---|---|---|
| `dxclient.acceptLicense` | `false` | Accept the HCL DXClient licence (required) |
| `watchNamespaces` | `[]` | Namespaces to watch (empty: all) |
| `javaExtensions.enabled` | `false` | Java extensions (`DXJavaExtension`) with the admission webhook |
| `javaExtensions.certificate.provider` | `auto` | Webhook certificate: cert-manager, or the OpenShift service CA |
| `javaExtensions.startupHook.enabled` | `true` | Second delivery mode through the HCL customer startup folder |
| `podSecurityContext` / `containerSecurityContext` | platform-neutral | No fixed UID: works on any Kubernetes and OpenShift |
| `resources`, `workDir.sizeLimit` | see values.yaml | Memory/CPU and working folder size |

All values: [values.yaml](https://github.com/europesip/dx-delivery-operator/blob/main/charts/dx-delivery-operator/values.yaml).

## Licence

MIT. Redistributions must keep the copyright notice and the licence. The DX Delivery Operator is not an HCL product;
HCL, HCL DX and HCL DX Compose are trademarks of HCL Technologies.
