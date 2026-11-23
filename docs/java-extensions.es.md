# Extensiones Java en DX Compose (DXJavaExtension)

[English](java-extensions.md) · **Español**

> **Vídeo (7 min):** [Portlets Java en HCL DX Compose sin imagen propia](https://youtu.be/ncVQlSORBOg) —
> el HCL Java Transition Module, el código Java que cubre y una demo real de DXJavaExtension (en inglés, subtítulos EN/ES).

`DXJavaExtension` (0.14.0, issue [#54](https://github.com/europesip/dx-delivery-operator/issues/54)) despliega
**código Java propio** en el Web Engine de DX Compose **sin imagen propia**: **portlets** JSR 168/286 (WAR) y
**JAR de librería compartida**. El código es un artefacto OCI en tu registro (Harbor, GHCR…); el operador lo carga
sobre la imagen **oficial** del Web Engine de HCL, reinicia el Web Engine de forma escalonada y registra los portlets
en DX.

## Por qué

DX Compose ejecuta los portlets dentro del Web Engine (Open Liberty). El método documentado por HCL para portlets
propios (la guía del Java Transition Module) es **construir tu propia imagen del Web Engine** (`FROM` la oficial +
`COPY` del WAR en `resources/customApplications/`), subirla a tu registro, cambiar la imagen de la release de Helm,
añadir un `<webApplication>` de Liberty en `configOverrideFiles` y registrar los portlets a mano con XMLAccess.
Funciona, y para uno o dos portlets que cambian poco es una buena opción. Sale caro cuando hay más:

- cada **subida de CF** obliga a reconstruir y volver a probar **tu** imagen sobre la base nueva;
- cada **cambio de un portlet** obliga a construir y subir una imagen de varios GB;
- necesitas una CI que construya imágenes, acceso a `hclcr.io` desde ella y un registro para las imágenes;
- la imagen que corre ya no es la de HCL;
- el registro es un paso manual.

Con `DXJavaExtension` la imagen sigue siendo la de HCL, tu código es un artefacto pequeño con versión (KB/MB), subir
de CF es solo cambiar la etiqueta oficial, y el despliegue y el registro son automáticos.

## Cómo funciona

```
  Git (PR)          Argo CD            DX Delivery Operator                        Pod del Web Engine (imagen oficial)
 ─────────     ─────────────────   ───────────────────────────────────   ─────────────────────────────────────────────
 cambio CR ──▶ aplica el CR ──▶    1 resuelve etiqueta → digest
                                    2 guarda status.desiredRef
                                    3 parchea la plantilla del StatefulSet ──▶ el StatefulSet crea el pod de nuevo
                                                                          API ──▶ webhook (pod del operador): añade
                                                                                  init container + volúmenes
                                                                          init container: descarga por digest,
                                                                                  verifica, descomprime WAR/JAR,
                                                                                  escribe el <webApplication>
                                                                          Web Engine: Liberty arranca el WAR
                                    4 espera: todos los pods Ready ◀── Ready
                                    5 XMLAccess predeployed (solo si falta) ──▶ catálogo de DX (base de datos)
```

### Las piezas

| Pieza | Qué es | Quién la crea |
|---|---|---|
| **Webhook de admisión** | Una regla del servidor de la API de Kubernetes: «antes de dar de alta un pod de este tipo, mándamelo». El nuestro es **mutante**: responde con un parche. Solo actúa sobre los pods del Web Engine (`objectSelector`) de los namespaces con la etiqueta `dx.europesip.com/java-extensions=enabled` (`namespaceSelector`) | El **chart del operador**, una vez, al instalarlo con `javaExtensions.enabled=true` (un `MutatingWebhookConfiguration`, un Service y un Certificate de cert-manager). El **servidor del webhook va dentro del pod del operador** |
| **`DXJavaExtension`** | Dice **qué** inyectar: el artefacto, el tipo, la ruta, la página | Tú (Git) |
| **Init container** (`dx-extensions-init`) | Se ejecuta antes que el Web Engine, en el mismo pod, y deja el código en su sitio | Lo añade el webhook a cada pod del Web Engine que se crea |
| **Controlador** | Resuelve, despliega, espera, registra, vuelve atrás | El operador |

El webhook nunca cambia el chart de HCL, la release de Helm ni la imagen: solo el pod que se está creando. Sobrevive a
cualquier `helm upgrade` de DX porque actúa al crear el pod, no en Helm (no hace falta post-renderer).

### Qué hace el init container, paso a paso

1. Lee la lista de extensiones (nombre, `repositorio@digest`, tipo, fichero, ruta) que le pasa el webhook.
2. Se autentica en el registro con los pull secrets de la extensión (basta solo lectura).
3. Descarga cada artefacto **por digest** y verifica el digest del manifiesto y del fichero.
4. Portlets: descomprime el WAR en `resources/customApplications/<fichero>.war/`. Librerías: escribe los JAR en
   `libraries/customSharedLib/` (el classpath global del Web Engine, CF236+).
5. Escribe el `<webApplication>` de Liberty de cada WAR en `configDropins/defaults/dx-java-extensions.xml`.
6. Termina con 0. Si una extensión **obligatoria** no se puede cargar, termina con error y un mensaje claro, y el Web
   Engine no arranca con la mitad de sus extensiones (`optional: true` la salta con un aviso).

### ¿Para qué el XMLAccess, si el WAR ya está en customApplications?

Hay **dos capas**, y cada una tiene que enterarse:

- **Liberty** (el servidor de aplicaciones): el WAR en `customApplications/` y su `<webApplication>` hacen que Liberty
  *arranque* la aplicación web. El código corre y responde en su ruta.
- **DX** (el portal): DX guarda su propio **catálogo en su base de datos**: módulos web, portlets, identificadores,
  permisos, configuración y páginas. DX **no** mira en `customApplications/`. Si nadie se lo dice, el portlet no
  existe para DX: no está en la administración, no se puede poner en una página, no tiene permisos.

El XMLAccess con `predeployed="true"` le dice a DX: «en esta ruta hay un módulo **ya arrancado**; dalo de alta con
estos portlets». (Como un centro comercial: Liberty abre la tienda; DX es el directorio. La tienda puede estar abierta,
pero si no está en el directorio nadie la encuentra ni la puede poner en el plano.)

En el DX tradicional las dos cosas pasaban de golpe (DX desplegaba el WAR en WebSphere y lo registraba). En Compose
están separadas; por eso el método de HCL también exige el XMLAccess.

**Cuándo**: el registro se guarda en la base de datos de DX y sobrevive a reinicios y a que el pod se cree de nuevo.
El operador solo lo lanza si el módulo **falta**, si **cambian los portlets de `portlet.xml`** o si cambia la página.
Una versión nueva del código con los mismos identificadores (1.0.0 → 1.1.0) no necesita nada en DX.

## Qué pasa en cada arranque y qué solo una vez

| En cada arranque del pod del Web Engine (también si se borra y se crea de nuevo) | ¿Se repite? |
|---|---|
| Descargar el artefacto (init container) | Sí, a un `emptyDir` (un WAR de 10 KB tarda en torno a un segundo) |
| Liberty arranca el WAR | Sí, igual que con imagen propia (Liberty arranca sus aplicaciones en cada arranque) |
| Registrar en DX (XMLAccess) | **No**: queda en la base de datos de DX |

## Fallos

| Situación | Qué pasa |
|---|---|
| Una etiqueta que no existe (p. ej. una errata en un PR) | El controlador resuelve la etiqueta **antes** de cambiar nada: **no se reinicia**, el Web Engine sigue con su versión, `ArtifactResolved=False`, motivo `ArtifactNotFound`, un evento |
| Un pod se reinicia de forma inesperada y el artefacto ya no está o el registro está caído | El init container falla con un mensaje claro, el pod queda en `Init:Error` y Kubernetes reintenta; igual que una imagen propia que desaparece (`ImagePullBackOff`). Protege lo que está en uso: **etiquetas inmutables** y una regla de retención que no borre versiones en uso; `optional: true` para extensiones sin las que el portal puede vivir |
| La versión nueva rompe el arranque (falla el init container, o no está Ready antes de `rolloutTimeout`: campo del spec, por defecto el valor del chart `javaExtensions.rolloutTimeout`, 30m) | El operador **vuelve** al digest anterior (`RolledBack`) y no reintenta ese digest hasta que cambie el spec |
| El webhook está caído | `failurePolicy: Fail` (por defecto): el pod del Web Engine no se crea hasta que responda (nunca un Web Engine sin sus extensiones). `Ignore`: arranca sin ellas. Si importa, el operador con 2 réplicas |
| El webhook no está activo para el namespace | El controlador no despliega: `NamespaceNotEnabled` (falta la etiqueta) o `WebhookNotActive` — solo cuando un pod creado con la plantilla actual no lleva la inyección; el mensaje dice por qué (sin configuración, `namespaceSelector` que no coincide, sin endpoints listos) |
| El pod nuevo del Web Engine no se puede programar (p. ej. sin memoria) | `PodPending` con el mensaje del scheduler; el despliegue sigue esperando hasta que se programe o `rolloutTimeout` lo devuelva atrás |

## Desplegar una extensión

1. **Construir y subir** (CI): `mvn package` y `oras push` del WAR con sus anotaciones (mira
   [samples/16-java-portlet](../samples/16-java-portlet)). Usa **etiquetas semver inmutables**.
2. **Declarar**: un PR con el `DXJavaExtension` (o una etiqueta nueva en `artifact.ref`).
3. **Aplicar**: Argo CD aplica el CR (los CR van por GitOps; la release de Helm de DX no).
4. Del resto se encarga el operador: resolver, despliegue pod a pod, registro, página y estado.

El ejemplo de CR, las anotaciones y la estructura de carpetas recomendada en el registro están en la
[versión en inglés](java-extensions.md#deploy-an-extension).

## Activarlo

```bash
helm upgrade dxop oci://ghcr.io/europesip/charts/dx-delivery-operator --reuse-values --set javaExtensions.enabled=true
kubectl label namespace <namespace-del-portal> dx.europesip.com/java-extensions=enabled
```

Necesita cert-manager (el certificado del webhook). Valores: `javaExtensions.initImage.*` (la imagen del init;
`pullSecret` si viene de un registro privado), `javaExtensions.webhook.failurePolicy`, `namespaceSelector`,
`objectSelector` (por defecto: pods con `app=dx-deployment-web-engine`; añade el tuyo si la release de DX se llama de
otra forma).

## Soporte y límites

- El webhook cambia el **pod** del Web Engine (un init container, volúmenes, montajes). Eso queda fuera de los valores
  que soporta el chart de HCL, así que, estrictamente, esa parte queda fuera del soporte de HCL — igual que un
  post-renderer de Helm. Hemos pedido a HCL (Idea) valores en el chart (`initContainers`/volúmenes extra, o
  `webEngine.extensions`) y el registro automático de portlets `predeployed`, con lo que dejaría de hacer falta.
- Una versión nueva sigue necesitando reiniciar el Web Engine (Liberty carga el WAR al arrancar), igual que con imagen
  propia.
- Fase 1: portlets y librerías compartidas. Más adelante: ficheros de propiedades y filtros
  (`propertiesFilesOverrides`, el «append» de CF239), verificación con cosign, caché persistente con comprobación de
  digest.
- Solo DX Compose (en el DX tradicional, DX despliega el WAR él mismo).

Campos: [referencia de la API](reference.md#dxjavaextension).
