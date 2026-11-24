# 16 · Portlet Java (DXJavaExtension)

[English](README.md) · **Español**

El portlet **EUROPESIP Showcase** (JSR 286, Java 8, Java puro, MIT) cargado desde un registro sobre la imagen
**oficial** del Web Engine de DX Compose con `DXJavaExtension`, sin imagen propia. Mira
[Extensiones Java](../../docs/java-extensions.es.md).

Qué muestra en su página: la versión (de la construcción), la hora del servidor, el usuario, el idioma, el modo y el
estado de la ventana, un formulario de saludo (una **acción** que fija un **parámetro de render**), un contador de clics
(ida y vuelta acción → render), unas etiquetas de tema (un **parámetro de render público** que comparten los portlets
de la página) y, para usuarios con sesión y permiso de edición, un **modo EDIT** que guarda el saludo y el color como
**preferencias del portlet**. La 1.1.x añade una etiqueta «NEW», para ver un cambio de versión.

| Fichero | |
|---|---|
| `showcase-portlet/` | Proyecto Maven (`portlet-api` 2.0 provided, sin JSP) |
| `build-and-push.sh` | `mvn package` + `oras push` con las anotaciones que leen el operador y las personas |
| `dxjavaextension.yaml` | El recurso: artefacto, ruta y una página `…/wps/portal/Home/showcase` |

Publicado por EUROPESIP, público (no hace falta pull secret) y con etiquetas inmutables, en dos registros:

```
ghcr.io/europesip/dx-delivery-operator/samples/europesip-showcase-portlet:1.0.2
ghcr.io/europesip/dx-delivery-operator/samples/europesip-showcase-portlet:1.1.2
harbor.k3s.europesip.com/public/dx-delivery-operator/samples/europesip-showcase-portlet:1.0.2
harbor.k3s.europesip.com/public/dx-delivery-operator/samples/europesip-showcase-portlet:1.1.2
```

Están las versiones 1.0.0 a 1.1.2, y también `test-hello-portlet:1.0` y `1.1`, el portlet mínimo de nuestra primera
prueba de concepto. El proyecto `public` de Harbor es de solo lectura (descarga anónima); el workflow
`.github/workflows/publish-samples.yml` lo copia a ghcr.io.

Requisitos: el operador con `javaExtensions.enabled=true`, el namespace con la etiqueta
`dx.europesip.com/java-extensions=enabled`, un `DXConnection` y un pull secret de tu registro. Después:

```bash
./build-and-push.sh 1.0.0 <registro>/dx-artifacts/java-extensions/portlets/europesip-showcase-portlet
kubectl apply -n <namespace-del-portal> -f dxjavaextension.yaml   # antes cambia connectionRef, ref y pullSecrets
kubectl get dxjx -n <namespace-del-portal> -w                       # RollingOut → Ready
```
