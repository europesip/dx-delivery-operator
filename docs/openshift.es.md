# OpenShift y otras plataformas

[English](openshift.md) · **Español**

Desde la 0.15.0 (issue #63) el mismo chart y los mismos valores funcionan en k3s, en cualquier otro Kubernetes y en
**OpenShift** (SCC `restricted-v2` por defecto). **No hay un flag para OpenShift**: el chart solo usa ajustes que
aceptan todas las plataformas y detecta al instalar lo que tiene que detectar.

> **Probado solo sobre k3s.** El chart y la imagen tienen en cuenta el contexto de seguridad y las particularidades de
> OpenShift (sin UID fijo, `restricted-v2`, las anotaciones de namespace de HCL DX, el service CA de OpenShift), pero
> el operador **todavía no se ha probado en OpenShift**. Se agradecen las experiencias de quien lo use en OpenShift
> (abre una issue).

## Qué hace el chart en cada plataforma

| Tema | k3s y otros Kubernetes | OpenShift |
|---|---|---|
| UID del pod del operador | el UID de la imagen (1000), porque el chart no pone ninguno | el UID del rango del namespace, que pone la SCC |
| `runAsNonRoot`, `seccompProfile: RuntimeDefault`, `allowPrivilegeEscalation: false`, quitar `ALL` | sí | sí (lo que pide `restricted-v2`) |
| Rutas en las que se escribe | `/work`, `/tmp` y `/dxclient/store` son emptyDir y `HOME=/tmp`, así que cualquier UID puede escribir | igual |
| Certificado del webhook (solo con `javaExtensions.enabled`) | cert-manager | cert-manager si está instalado; si no, el **service CA de OpenShift** |
| Init container de las extensiones Java | corre con el usuario del pod del Web Engine | igual |

Antes de la 0.15.0 el chart fijaba `runAsUser`, `runAsGroup` y `fsGroup` a 1000, y `restricted-v2` lo rechaza en un
namespace con el rango de UID por defecto. Para mantener el comportamiento anterior en un clúster que lo necesite,
ponlos en `podSecurityContext`:

```yaml
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000
  seccompProfile: { type: RuntimeDefault }
```

## Las anotaciones de namespace de HCL DX

Para instalar DX en OpenShift, HCL pide crear el **namespace de DX** con tres anotaciones
([documentación de HCL](https://help.hcl-software.com/digital-experience/9.5/latest/deployment/install/container/helm_deployment/preparation/mandatory_tasks/prepare_namespace/)):

```yaml
metadata:
  annotations:
    openshift.io/sa.scc.mcs: "s0:c24,c4"
    openshift.io/sa.scc.supplemental-groups: "1001/10000"
    openshift.io/sa.scc.uid-range: "1000/10000"
```

Sirven para que `restricted-v2` acepte el UID 1000 y el grupo 1001, el usuario de los pods de DX (el chart de HCL pone
`runAsUser: 1000`, `runAsGroup: 1001` y `fsGroup: 1001` a nivel de pod). Qué supone para el operador:

- **El namespace del operador no las necesita** (por ejemplo `dxop-system`): el chart no pone UID, así que OpenShift
  le da el UID de su propio rango. Si instalas el operador en el namespace de DX, recibe el UID 1000 por esas
  anotaciones, y también funciona.
- **El init container de las extensiones Java** corre dentro del pod del Web Engine del namespace de DX y toma el UID
  y el GID de ese pod (1000/1001, dentro del rango de las anotaciones). Si un pod del Web Engine no tuviera UID a nivel
  de pod pero su contenedor sí, el webhook le pone al init container el mismo. Escribe los WAR y los JAR con permisos
  0644/0755, así que el Web Engine los lee con cualquier UID.
- **La etiqueta de las extensiones Java** (`dx.europesip.com/java-extensions=enabled`) es una etiqueta, no una
  anotación: ponla en el namespace de DX junto a las anotaciones de HCL.

## Certificado del webhook

`javaExtensions.certificate.provider`:

| Valor | Qué crea el chart |
|---|---|
| `auto` (por defecto) | En OpenShift **sin** cert-manager: el service CA (anotación `service.beta.openshift.io/serving-cert-secret-name` en el Service y `service.beta.openshift.io/inject-cabundle` en el MutatingWebhookConfiguration). En todos los demás casos: cert-manager, como antes de la 0.15.0 |
| `cert-manager` | un `Certificate` de cert-manager (y un `Issuer` autofirmado, o el emisor de `javaExtensions.certManager`) |
| `openshift-service-ca` | las anotaciones del service CA |

El Secret es siempre `<release>-webhook-cert` con `tls.crt` y `tls.key`, así que el operador no cambia.

**`helm template` y Argo CD**: ahí Helm no ve las APIs del clúster y `auto` significa cert-manager. En OpenShift sin
cert-manager pon `javaExtensions.certificate.provider: openshift-service-ca` (o pasa
`--api-versions route.openshift.io/v1`).

## OperatorHub (bundle de OLM)

`python3 hack/olm-bundle.py` (o `make bundle`) genera un bundle de OLM en `bundle/<versión>/` a partir del chart, el
formato de los catálogos comunitarios (OperatorHub.io y el OperatorHub que trae OpenShift). Diferencias con el chart:

- OLM pone los namespaces (OperatorGroup), el certificado del webhook y las CRD.
- **La licencia de HCL DXClient se acepta en la Subscription**, no en el bundle:
  `spec.config.env: [{name: DXCLIENT_ACCEPT_LICENSE, value: "true"}]`. Hasta entonces el operador no ejecuta DXClient.
- El webhook de las extensiones Java se registra siempre con `failurePolicy: Ignore`, porque OLM pone el
  `namespaceSelector` por su cuenta: un pod del Web Engine de un namespace sin `DXJavaExtension` nunca espera al
  operador.

Se valida con `bin/operator-sdk bundle validate bundle/<versión> --select-optional suite=operatorframework`.

### Dónde está publicado

- **OperatorHub.io**: publicado, [https://operatorhub.io/operator/dx-delivery-operator](https://operatorhub.io/operator/dx-delivery-operator) (primera versión 0.15.0, ahora 0.17.0, canal `alpha`).
- **Catálogo comunitario de OpenShift** (el OperatorHub que trae OpenShift, catálogos v4.16 a v4.22): enviado con la
  0.17.0, ha pasado el pipeline de Red Hat y **está pendiente de la aprobación de un mantenedor**
  ([community-operators-prod#12110](https://github.com/redhat-openshift-ecosystem/community-operators-prod/pull/12110)). Esta página se actualizará cuando se acepte.

### Instalar desde OperatorHub.io

En un clúster con OLM (OperatorHub.io da los comandos para instalar OLM), crea la Subscription con la licencia de HCL
DXClient aceptada:

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

Sin `DXCLIENT_ACCEPT_LICENSE=true` el operador se instala pero no ejecuta DXClient. El aviso de **probado solo en
k3s** del principio de esta página también vale para el bundle.

## Probado

- k3s: la imagen del operador con UID 1000 y con un UID cualquiera sin el grupo 1000 (1000680000, grupo 0, como los
  da OpenShift): DXClient escribe su configuración y sus logs en `/dxclient/store` en los dos casos.
- `helm template` de todos los modos de certificado; pruebas unitarias del usuario del init container.
- Todavía no en un clúster OpenShift real.
