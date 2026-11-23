# 16 · Java portlet (DXJavaExtension)

**English** · [Español](README.es.md)

The **EUROPESIP Showcase** portlet (JSR 286, Java 8, plain Java, MIT) loaded from a registry onto the **official** DX
Compose Web Engine image with `DXJavaExtension` — no custom image. See [Java extensions](../../docs/java-extensions.md).

What it shows on its page: version (from the build), server time, user, locale, portlet mode and window state, a
greeting form (an **action** that sets a **render parameter**), a click counter (action → render parameter round trip),
topic chips (a **public render parameter** shared with other portlets on the page) and, for signed-in users with edit
rights, an **EDIT mode** that stores the greeting and the accent colour as **portlet preferences**. 1.1.x adds a "NEW"
badge, to see a version change.

| File | |
|---|---|
| `showcase-portlet/` | Maven project (`portlet-api` 2.0 provided, no JSP) |
| `build-and-push.sh` | `mvn package` + `oras push` with the annotations the operator and people read |
| `dxjavaextension.yaml` | The resource: artifact, context root and a page `…/wps/portal/Home/showcase` |

Published by EUROPESIP (immutable tags):

```
harbor.k3s.europesip.com/dx-artifacts/java-extensions/portlets/europesip-showcase-portlet:1.0.2
harbor.k3s.europesip.com/dx-artifacts/java-extensions/portlets/europesip-showcase-portlet:1.1.2
```

Prerequisites: the operator with `javaExtensions.enabled=true`, the namespace labelled
`dx.europesip.com/java-extensions=enabled`, a `DXConnection` and a pull secret for your registry. Then:

```bash
./build-and-push.sh 1.0.0 <registry>/dx-artifacts/java-extensions/portlets/europesip-showcase-portlet
kubectl apply -n <portal-namespace> -f dxjavaextension.yaml      # edit connectionRef, ref and pullSecrets first
kubectl get dxjx -n <portal-namespace> -w                          # RollingOut → Ready
```
