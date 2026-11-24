# Historial de versiones

[English](CHANGELOG.md) · **Español**

Para volver a una versión anterior: `helm upgrade dxop oci://ghcr.io/europesip/charts/dx-delivery-operator --version <versión> --reuse-values`
desde la 0.13.0, `oci://ghcr.io/europesip/charts/hcl-dx-scriptapp-operator` para la 0.12.x y anteriores
(la 0.3.0 renombró los recursos: mira sus notas).

## 0.17.0 · 2026-11-24 · DXJavaExtension: segundo modo de entrega por la carpeta de arranque de cliente de HCL

**Nuevo** (#72, propuesto por Jordi Urrea): `spec.delivery: startupHook` entrega las extensiones Java sin el
webhook de admisión, dentro de los valores que soporta el chart de HCL. Mira
[Dos modos de entrega](docs/java-extensions.es.md#dos-modos-de-entrega-webhook-por-defecto-o-startup-hook).
- El portal lleva un volumen en la carpeta de arranque de cliente de la imagen del Web Engine
  (`volumes.webEngine.customPVCs`, mountPath `/opt/openliberty/wlp/usr/svrcfg/bin/customer/startup`; el chart de HCL
  crea un único PVC compartido por todas las réplicas). El operador escribe ahí una vez un script pequeño y un token
  (un job corto), y el entrypoint de HCL ejecuta el script en cada arranque del contenedor, antes del arranque final
  de Liberty.
- El script descarga las extensiones **a través del operador** (nuevo endpoint interno del clúster, Service
  `<release>-java-extensions`, puerto 8090; token HMAC por Web Engine, clave en el Secret `<release>-startup-hook` que
  crea el operador): las credenciales del registro nunca llegan al contenedor de DX. Detiene el arranque (código 2)
  si una extensión obligatoria no se puede cargar.
- El despliegue pod a pod, la vuelta atrás, el registro con XMLAccess y los nombres únicos son iguales que con el
  webhook.
- **`webhook` sigue siendo el modo por defecto.** Los dos modos nunca se mezclan en un Web Engine: un namespace con la
  etiqueta del webhook o extensiones de un mismo Web Engine con modos distintos dan `DeliveryConflict`. Motivos nuevos:
  `StartupVolumeMissing`, `SeedingStartupVolume`, `SeedFailed`, `StartupHookNotRun`, `StartupHookDisabled`.
- Chart: `javaExtensions.startupHook.enabled` (por defecto `true`) y `.port` (8090); el RBAC añade `pods` patch,
  `persistentvolumeclaims` get/list/watch/patch y `jobs` create/delete. El bundle de OLM incluye el Service.
- Al actualizar: las extensiones con webhook no se reinician (su hash de despliegue no cambia).

## 0.16.0 · 2026-11-24 · DXJavaExtension: los mismos identificadores de portlet en todos los entornos

**Nuevo** (#69, a propuesta de Jordi Urrea): las páginas y peticiones XMLAccess que se refieren a un portlet
siguen funcionando al pasar de un entorno a otro. Mira [Los mismos identificadores en todos los entornos](docs/java-extensions.es.md#los-mismos-identificadores-en-todos-los-entornos).
- Cada portlet se registra con un **nombre único** `<uid de la portlet-app>.<portlet-name>` derivado de
  `portlet.xml` (`spec.identifiers.uniqueNames`, por defecto true). Los registros existentes lo reciben en la
  siguiente reconciliación, sin reiniciar el Web Engine.
- Campos nuevos en el status: `webModuleObjectID`, `portletAppObjectID`, `portletIDs` (nombre, nombre único,
  object ID).
- No se ofrecen object ID fijos: probado en CF239, DX los ignora en los portlets `predeployed` y genera los suyos.

**Corregido**: al borrar un `DXJavaExtension` con página, los portlets y la página se quedaban en DX: la petición
XMLAccess llevaba `content-node` antes que `web-app` y el esquema la rechaza entera (EJPXA0049E). Ahora va primero
`web-app`.

Para actualizar desde la 0.15.0: aplica las CRD (campos opcionales nuevos) y `helm upgrade` con tu fichero de valores.

## 0.15.0 · 2026-11-24 · OpenShift sin flag, bundle de OLM

**Nuevo** (#63): el mismo chart y los mismos valores funcionan en k3s, en otros Kubernetes y en **OpenShift** (SCC
`restricted-v2`), sin ningún valor específico de OpenShift. Mira [OpenShift y otras plataformas](docs/openshift.es.md).
**Probado solo sobre k3s; todavía no se ha probado en OpenShift.**
- **Sin UID fijo**: el pod ya no pone `runAsUser`/`runAsGroup`/`fsGroup` 1000 (`restricted-v2` lo rechaza). En
  Kubernetes el proceso sigue corriendo con el UID 1000 (el de la imagen); en OpenShift la SCC pone el UID del
  namespace. Valores nuevos `podSecurityContext` y `containerSecurityContext` (por defecto: `runAsNonRoot`, seccomp
  `RuntimeDefault`, sin escalada de privilegios, quitar `ALL`); pon ahí `runAsUser: 1000`, etc. para mantener el pod
  anterior.
- **Rutas escribibles con cualquier UID**: `HOME=/tmp` y un emptyDir en `/dxclient/store` (configuración y logs de
  DXClient).
- **Certificado del webhook detectado** (`javaExtensions.certificate.provider: auto`): el service CA de OpenShift en
  OpenShift sin cert-manager, cert-manager en todos los demás casos (como antes). Se puede forzar `cert-manager` u
  `openshift-service-ca` (`helm template`, Argo CD).
- **Init container de las extensiones Java**: si el pod del Web Engine no tiene UID a nivel de pod pero su contenedor
  sí, el init container toma ese, así que siempre corre con el usuario de DX.
- **Bundle de OLM** (`hack/olm-bundle.py`, `make bundle`): se genera a partir del chart para los catálogos
  comunitarios (OperatorHub.io, OperatorHub de OpenShift); validado con `operator-sdk bundle validate`. Aún sin
  publicar.

Para actualizar desde la 0.14.2: aplica las CRD (sin cambios) y `helm upgrade` con tu fichero de valores. El pod del
operador se reinicia (comprueba que no haya ninguna importación ni exportación en marcha); en DX no cambia nada.

## 0.14.2 · 2026-11-24 · DXJavaExtension: sin falsos WebhookNotActive

**Corregido** (#61): un `DXJavaExtension` decía `WebhookNotActive` aunque el webhook funcionaba, en el momento en que
el pod viejo del Web Engine se para y el nuevo aún no existe, y mientras el pod nuevo seguía Pending (por ejemplo,
sin memoria en los workers).
- El controlador decide ahora con los hechos de cada pod: `WebhookNotActive` solo cuando un pod creado con la
  plantilla **actual** no lleva la inyección, y el mensaje incluye un **diagnóstico** del webhook (la
  MutatingWebhookConfiguration que sirve la ruta de las extensiones, su `namespaceSelector` frente a las etiquetas del
  namespace y endpoints listos de su Service).
- Motivo nuevo **`PodPending`** con el mensaje del scheduler (p. ej. `Insufficient memory`) cuando el pod nuevo no se
  puede programar; `RollingOut` dice si espera a que pare el pod viejo o al pod nuevo.
- RBAC: el operador también lee (`get`, `list`) `mutatingwebhookconfigurations` y `endpointslices`.

Para actualizar desde la 0.14.1: aplica los CRDs (sin cambios) y `helm upgrade` con tu fichero de valores.

## 0.14.1 · 2026-11-23 · DXJavaExtension: ya no se queda en RollingOut

**Corregido** (#57): tras un cambio de versión, un `DXJavaExtension` podía quedarse en `RollingOut` aunque el pod
del Web Engine ya estuviera Ready y sirviendo la versión nueva, hasta que otra cosa lo volviera a revisar.
- El controlador ahora **vigila los pods del Web Engine y su StatefulSet** (solo los que ha tocado el webhook): un
  pod que pasa a Ready (o cuyo init container falla) revisa sus extensiones al momento.
- Durante el despliegue sigue consultando cada **10 s** (antes 15 s), y cada revisión tiene un límite (4 min): una
  llamada colgada al registro o a DX ya no puede bloquear el controlador.
- Valor nuevo del chart **`javaExtensions.rolloutTimeout`** (duración Go, por defecto 30m): el tiempo por defecto que
  puede durar un despliegue antes de volver atrás; `spec.rolloutTimeout` lo sigue sobrescribiendo.

Para actualizar desde 0.14.0: aplica los CRDs (sin cambios) y `helm upgrade` con tu fichero de valores
(`--reuse-values` no recoge los valores nuevos del chart).

## 0.14.0 · 2026-11-23 · Extensiones Java (DXJavaExtension)

Recurso nuevo **`DXJavaExtension`** (#54): Java propio en DX Compose **sin imagen propia**. Un **portlet** JSR 168/286
(WAR) o **JAR de librería compartida** desde un artefacto OCI se cargan sobre la imagen **oficial** del Web Engine.
Mira [Extensiones Java](docs/java-extensions.es.md).

- **Webhook de admisión** (valor del chart `javaExtensions.enabled`, desactivado por defecto; certificado con
  cert-manager): cuando se crea un pod del Web Engine en un namespace con la etiqueta
  `dx.europesip.com/java-extensions=enabled`, le añade un init container, volúmenes `emptyDir` y montajes en
  `resources/customApplications/`, `libraries/customSharedLib/` y `configDropins/defaults/`. Nunca cambia el chart de
  HCL, la release de Helm ni la imagen.
- **Imagen del init container** `ghcr.io/europesip/dx-delivery-operator/dx-extensions-init`: descarga cada extensión
  por digest, la verifica, descomprime WAR y JAR y escribe el `<webApplication>` de Liberty; falla con un mensaje claro
  si falta una extensión obligatoria (`optional: true` la salta).
- **Controlador**: resuelve la etiqueta a un digest **antes** de reiniciar nada (una etiqueta que no existe nunca
  reinicia el portal: `ArtifactNotFound`), reinicia el Web Engine pod a pod a través de su StatefulSet, espera a que
  todos los pods estén Ready con el digest nuevo, registra los portlets en DX con XMLAccess `predeployed` **solo si
  faltan o cambian**, página opcional, vuelta al digest anterior si el despliegue falla (`RolledBack`), y los da de
  baja al borrar.
- Valores del chart `enableControllers` y `leaderElectionID` (una segunda instancia del operador, por ejemplo una de
  pruebas limitada a un namespace y un controlador).
- Ejemplo [16-java-portlet](samples/16-java-portlet): el portlet EUROPESIP Showcase y su script de construcción.

## 0.13.0 · 2026-11-23 · Nuevo nombre: DX Delivery Operator

El producto se llama ahora **DX Delivery Operator** (de EUROPESIP); antes, *HCL DX Script Application operator*
(#52). Empezó desplegando Script Applications y hoy también lleva librerías WCM, páginas, temas, XMLAccess,
colecciones del DAM, tareas de administración de WCM, bases de datos y paquetes de portal; el foco sigue siendo la
entrega de aplicaciones. Sin cambios de funcionamiento.

**Nombres nuevos**
- Repositorio `europesip/hcl-dx-scriptapp-operator` → `europesip/dx-delivery-operator` (GitHub redirige las URL
  antiguas); módulo Go `github.com/europesip/dx-delivery-operator`.
- Imagen `ghcr.io/europesip/dx-delivery-operator`. La 0.13.x se publica también como
  `ghcr.io/europesip/hcl-dx-scriptapp-operator` (obsoleta, se retira en la 0.15.0).
- Chart de Helm `oci://ghcr.io/europesip/charts/dx-delivery-operator`. La 0.13.x se publica también con el nombre
  antiguo `hcl-dx-scriptapp-operator` (alias obsoleto, se retira en la 0.15.0), idéntico salvo que mantiene los
  nombres de recursos de la 0.12.x.
- Valores `nameOverride` / `fullnameOverride` (nuevos).
- La anotación `com.europesip.dx.tool.operator`, la línea `Operator:` de `EXPORT-INFO.txt` y el origen de los eventos
  dicen ahora `dx-delivery-operator <versión>`.

**No cambia:** el grupo de la API `dx.europesip.com`, ningún tipo ni CRD, los finalizadores, las etiquetas, el lease
de elección de líder (`hcl-dx-scriptapp-operator.europesip.com`, así nunca funcionan a la vez un pod antiguo y uno
nuevo), el nombre de release `dxop` ni el namespace `dxop-system` de la documentación. Los manifiestos y artefactos
existentes siguen funcionando.

**Actualizar desde la 0.12.x** (aplica antes los CRDs, como en cada versión):
- Manteniendo los nombres de los recursos: `helm upgrade dxop oci://ghcr.io/europesip/charts/dx-delivery-operator
  --version 0.13.0 -n dxop-system --reuse-values --set nameOverride=hcl-dx-scriptapp-operator --set image.repository=ghcr.io/europesip/dx-delivery-operator`
  (o el chart obsoleto `hcl-dx-scriptapp-operator` 0.13.0, que pone él mismo el `nameOverride`).
- O con los nombres nuevos (Deployment, ServiceAccount, ClusterRole y Binding `dxop-dx-delivery-operator`): el mismo
  comando sin `nameOverride`. Helm sustituye los objetos antiguos; el lease compartido pasa al pod nuevo.
- Con `--reuse-values`, pon `image.repository` como arriba: si no, una release antigua conserva el repositorio de
  imagen guardado.
- Como en cualquier reinicio del operador, comprueba antes que no hay ninguna importación o exportación WCM en marcha.

## 0.12.0 · 2026-11-23 · Tipos de artefacto `theme` y `app`

- **Tipo `theme`** (#49): un `DXExport` con solo `parts.themes` sube un artefacto de tipo `theme` (súbelo a
  `<proyecto>/themes/<origen>-<tema>`); todo paquete con temas lleva `com.europesip.dx.themes` y `bundle.parts` ya
  incluye `themes` (antes se omitía). Los lectores traducen `theme`/`themes` a `theme` (no a `site`).
- **Tipo `app`** (#50): Script Applications y portlets, carpeta `apps/`, lo genera DX Transfer Studio 0.2.5; se
  reconoce y está documentado (anotaciones `scriptapps`, `portlets`, `portlets.jsr`, `portlets.war`); el operador aún
  no lo importa.
- Documentación: artifact-annotations, portable-bundles, spec docs/specs/49-50-theme-app-kinds.md.

## 0.11.0 · 2026-11-22 · DX Core (DX 9.5 tradicional sobre WebSphere)

- **Campos de Secrets que dicen de quién son las credenciales** (#45, docs/secret-fields.es.md): nuevos
  `DXConnection.spec.portalCredentialsSecret` y `tlsCASecret`, `DXDatabase.spec.dbAdminCredentialsSecret`
  (+ `dbAdminCredentialsSecretNamespace`) y `outputSecretName`.
- **Obsoletos** (se siguen aceptando, con un evento `DeprecatedField` y una nota en la condición `Ready`; se quitarán
  más adelante, con aviso): `credentialsSecret` → `portalCredentialsSecret`, `caSecret` → `tlsCASecret`, `adminSecret` →
  `dbAdminCredentialsSecret`, `adminSecretNamespace` → `dbAdminCredentialsSecretNamespace`, `secretName` → `outputSecretName`.
  El API server rechaza poner el nombre nuevo y el antiguo con valores distintos.
- **Formato de los artefactos, esquema 2** (común con DX Transfer Studio y Backstage, `docs/artifact-annotations.es.md`):
  `com.europesip.dx.kind` es `wcm`, `site` o `xmlaccess` (`portal` reservado para portales completos, nunca con
  credenciales); `source.server` es un nombre corto (`DXConnection.spec.sourceName`, por defecto el nombre de la
  conexión) y la URL va en el nuevo `source.url`; se escribe `source.virtual-portal` junto a `source.portal`;
  `org.opencontainers.image.authors` y `com.europesip.dx.reason` salen de los nuevos `spec.destination.authors` y
  `spec.destination.reason` (si no, de las anotaciones del recurso). Los lectores aceptan el esquema 1 y la guía v1.
- **DX Core** (#44): `DXConnection.spec.runtime` (`compose` por defecto, `core`) y `spec.dxConnect` (URL, `cwCredentialsSecret` del
  Config Wizard y `wasCredentialsSecret` de WebSphere, perfil). Con `core`, las exportaciones e importaciones de librerías WCM van por
  **DXConnect** (las llamadas de DXClient `wcm-library-export` / `wcm-library-import`), porque DX Core no tiene
  `dxFileTransfer`; el operador convierte el tar de DXConnect (una carpeta por librería) en su zip habitual y al revés,
  así lo exportado pasa entre DX Core y DX Compose. `mode: ImportCopy` se rechaza en Core (import-wcm-data conserva IDs).
- **Raíz de contexto** sacada de `contentHandlerPath` en todo (XMLAccess, URL de páginas): un DX tradicional con la raíz
  vacía funciona con `contentHandlerPath: /mycontenthandler`.
- **`DXExport` `parts.themes`**: el registro de cada tema con sus propios skins (`themes/theme-<nombre>.xml`, limpio para
  importar: sin datos de idioma vacío ni locates de skin repetidos) y su carpeta WebDAV
  (`themes/theme-<nombre>-webdav.zip`). Los **portlets clonados** (`$cloned`) de las páginas exportadas se definen en
  `xmlaccess/01-pages.xml`.
- **`DXTheme` `registration: FilesOnly`**: sube solo los ficheros WebDAV (del `-webdav.zip` de un paquete) y deja el
  registro a un `DXXMLAccess` con el XML del tema; los ficheros que el destino rechaza con HTTP 400 se avisan con un evento.
- **XMLAccess** respeta el tiempo máximo del recurso (se cortaba a los 90 s, poco para importar un sitio) y va por
  `internalURL` si está puesto.
- Documentación *DX Core*, ejemplo 15.

## 0.10.1 · 2026-11-19 · Importar carpetas subidas con oras; portales virtuales con LDAP

- **Carpetas subidas con `oras push <ref> <carpeta>`** (#41): se desempaquetan como hace `oras pull` (quedaban un
  nivel más abajo y DX fallaba: SER3009E), y los zip de los lotes empiezan siempre en la carpeta del export que espera
  DX (dos niveles por encima de los `<biblioteca>.node`), tenga la fuente las carpetas que tenga.
- **Portales virtuales en DX Compose con LDAP** (#42): el realm es el del spec, el de un portal virtual que ya exista
  o, si no, `FederatedRealm` y después `defaultWIMFileBasedRealm`; cada intento se comprueba en la lista de portales
  virtuales (XMLAccess contesta `ok` aunque DX lo rechace), así que un fallo para la ejecución en vez de importar en el
  portal base.

## 0.10.0 · 2026-11-19 · Módulos de mantenimiento de WCM

- **Recurso nuevo `DXWCMAdminTask`** (#39): el member fixer y el workflow checker de WCM en DX Compose por HTTP
  (`/wps/wcm/myconnect?MOD=MemberFixer|WorkflowControlChecker`), el `ConfigEngine run-wcm-admin-task-*` del DX
  tradicional: bibliotecas o todas, una petición cada una, **modo informe salvo `fix: true`**, solo opciones
  documentadas. Resumen por biblioteca en `status.libraries`, la respuesta de DX en el ConfigMap `<nombre>-report`,
  opcionalmente un artefacto OCI. Toma el bloqueo del DX (nunca a la vez que una importación). Docs *Módulos de
  mantenimiento de WCM*, ejemplo 14.
  Con `DXConnection.spec.internalURL` (0.9.2) las peticiones van a la URL interna del clúster.

## 0.9.2 · 2026-11-19 · Las importaciones WCM no se solapan tras un corte del proxy; internalURL

- **Sin lotes solapados** (#37): cuando un proxy corta la petición del módulo de datos de un lote (HTTP 502/503/504), DX
  sigue importándolo; ahora el operador espera, antes del siguiente lote, el tiempo que DX necesita (1,5 × sus ficheros a
  la velocidad de los lotes no cortados de la ejecución, o 4 ficheros/s; de 3 a 90 minutos; evento `ChunkCut`). Dos
  importaciones a la vez bloqueaban Derby.
- **Las bibliotecas de un lote cortado se comprueban en DX** al final en vez de darse por fallidas: importadas si DX las
  lista y son nuevas o se actualizaron (`updated` de WCM REST) después de empezar su lote, con un aviso.
- **`DXConnection.spec.internalURL`**: URL interna del Web Engine en el clúster (`http://dx-deployment-web-engine.<ns>.svc:9080`)
  para las peticiones largas (importación y exportación del módulo de datos, dxFileTransfer): ningún timeout de Traefik
  ni HAProxy las corta. Inicia sesión con el formulario (LtpaToken2 sin dominio); la comprobación de la conexión la prueba
  y, si no responde, se usa `url`.
## 0.9.1 · 2026-11-19 · Las importaciones se retoman tras un reinicio; los IDs se eligen al importar

- **`DXWCMLibraryImport` sigue en el lote donde iba** tras un reinicio del operador (#33): el plan de lotes y el avance se
  guardan en `status.resume`; los lotes terminados no se vuelven a importar. El lote interrumpido, que DX puede seguir
  importando, se vuelve a importar tras una espera (1,5 veces el lote más largo, de 3 a 30 minutos) en una carpeta nueva
  de dxFileTransfer (los nombres llevan el intento; las carpetas antiguas se borran al terminar: borrar la carpeta que
  DX estaba leyendo hacía fallar su importación). Las importaciones como copia marcan el lote interrumpido como fallido
  en vez de duplicarlo. Si cambia el spec o la exportación, empieza de cero.
- **Los IDs de objeto se eligen al importar** (como WCM import / import-copy), también para XML exportado con
  `xmlaccess.sh` de un DX tradicional: **`DXXMLAccess.spec.objectIDs`**: `Keep` (por defecto, como antes: las
  peticiones se envían tal cual y se conservan los IDs del XML) o `New` (`create-oids="true"` en cada `<request>`, una
  vez: DX da IDs nuevos). La petición de `cleanup` se envía tal como está escrita.
- **DXExport conserva los IDs de objeto** de las páginas en `xmlaccess/01-pages.xml` (la 0.9.0 ponía
  `create-oids="true"`): se elige al importar. Los owners del control de acceso se siguen quitando (un owner
  desconocido hace fallar la importación). `EXPORT-INFO.txt` y `bundle.json` (`objectIds`, y `composeNewIds` /
  `traditionalNewIds` en cada paso) dan las dos formas: mantener (recomendada) o IDs nuevos, con los comandos del
  tradicional (un `sed` para `create-oids`, el módulo de datos `taskType=import-copy` para WCM). La misma opción para
  páginas y Portal Site (Keep ↔ `mode: Import`, New ↔ `mode: ImportCopy`); documentación *Paquetes portables*.

## 0.9.0 · 2026-11-19 · Paquetes portables (DXExport)

- **Recurso nuevo `DXExport`** (#31): páginas (XMLAccess), la biblioteca Portal Site y bibliotecas WCM de un portal
  DX Compose (base o virtual) en un artefacto OCI, un **DX bundle** (`dx-bundle/v1`): `EXPORT-INFO.txt` (origen,
  versiones, quién y por qué, contenido, avisos y **cómo importarlo en DX Compose y en un DX tradicional sobre
  WebSphere**, con los comandos `xmlaccess.sh` y `ConfigEngine.sh import-wcm-data`), `bundle.json` (pasos de
  importación ordenados, tamaño y sha256 de cada fichero), `xmlaccess/01-pages.xml` (la exportación XMLAccess
  convertida en petición update con `create-oids="true"`, sin status, build, comentarios ni owners) y
  `wcm/wcm-export-<nombre>.zip` (la exportación del módulo de datos). DXXMLAccess y DXWCMLibraryImport leen un paquete
  como fuente de imagen (comprobado en DX Compose CF239). Documentación: *Paquetes portables*. Ejemplo 13.
- La parte WCM de `DXWCMLibraryExport` se comparte con `DXExport` (sin cambios de comportamiento).

## 0.8.0 · 2026-11-19 · Atributos de los artefactos

- **Atributos de los artefactos** (docs/artifact-annotations.es.md): todo artefacto que se sube (exportaciones WCM,
  respuestas de XMLAccess) lleva anotaciones `org.opencontainers.image.*` y `com.europesip.dx.*` (tipo, servidor de
  origen, versión, conexión y portal, bibliotecas, quién lo pidió, motivo, versión del operador, con qué se creó el
  recurso), para que una versión futura pueda listar y filtrar por ellas.
  **`DXWCMLibraryExport.spec.destination.attributes`**: atributos libres clave/valor, como `com.europesip.dx.attr.<clave>`.
- **EXPORT-INFO.txt** dice también quién lo pidió y por qué, la versión del operador, con qué se creó el recurso
  (Backstage y su plugin, de la anotación `dx.europesip.com/created-with`) y los atributos.
- El chart pasa `OPERATOR_VERSION` (su appVersion) al operador.

## 0.7.0 · 2026-11-19 · Exportar bibliotecas WCM

- **Recurso nuevo `DXWCMLibraryExport`** (#27): exporta las bibliotecas WCM que elijas de un portal DX Compose
  (base o virtual) con el módulo de datos de WCM y las sube a un registro como artefacto de ficheros ORAS (sin
  artifactType): `wcm-export-<nombre>.zip`, tal cual lo escribe DX (el formato JCR de `export-wcm-data` de
  ConfigEngine), y `EXPORT-INFO.txt` (servidor de origen, build de DX, portal virtual, fecha y hora, bibliotecas con
  su tamaño, cómo importarlo). `DXWCMLibraryImport` lo lee como origen de imagen; un DX tradicional importa la
  carpeta descomprimida con `import-wcm-data`. Status: `phase`, `currentLibrary`, `exportedLibraries`,
  `totalBytes`, `image`, `digest`. Borrarlo detiene una exportación en marcha. Ejemplo 12, documentación (cómo
  funciona, referencia). Chart: la CRD nueva y las reglas RBAC.

## 0.6.2 · 2026-11-18 · Borrar una importación en marcha la detiene

- `DXWCMLibraryImport` (#25): mientras dura una ejecución, el operador comprueba cada 10 s si su recurso se ha borrado;
  si es así se detiene en el acto: la descompresión de la exportación, la descarga de una imagen y los lotes que
  faltan por subir. Antes seguía (CPU, disco) hasta terminar el paso.

## 0.6.1 · 2026-11-18 · Las exportaciones grandes ya no reinician el operador

- **Una exportación WCM de cientos de MB ya no reinicia el operador en bucle** (#23). Visto con un `.tar.bz2` de
  760 MB: las sondas de salud vencían (1 s) mientras descomprimía en un nodo de control plane ocupado con 50m de CPU,
  el pod se reinició 7 veces y cada reinicio volvía a empezar la importación. Chart: requests `500m` / `512Mi`, límite
  `2Gi`; prefiere nodos que no sean control plane (preferente: un clúster de un nodo sigue funcionando); volumen de
  trabajo `8Gi` (antes `2Gi`: no cabían el archivo y su árbol descomprimido); sondas con `timeoutSeconds: 5` y
  `failureThreshold: 6`, configurables (`probes`).
- **La descompresión muestra su avance** en `status.message` cada 15 s: `unpacking <fichero>: 45% (340 of 760 MB read)`.

## 0.6.0 · 2026-11-18 · Las bibliotecas de una exportación WCM, para elegir

- `DXWCMLibraryImport` (#21): nuevo **`status.exportLibraries`**, cada biblioteca de la exportación con su `title`,
  **`description`** (`icm:description` del portal de origen, hasta 300 caracteres), `bytes`, `files`,
  `existsInTarget` y **`plan`**: `Import`, `Skip` (Portal Site, o ya existe y `overwrite` es false), `CannotUpload`
  (nombres que DX rechaza) o `NotSelected` (fuera por `include`, `exclude` o `excludeHCLDefaults`), con su `reason`.
  Lo escribe cada ejecución, también la simulación, para que una interfaz liste las bibliotecas e importe solo las
  elegidas (`libraries.include`).

## 0.5.2 · 2026-11-18 · La carpeta de una imagen se encuentra sola

- **Imágenes sin ficheros en `/app`** (la carpeta por defecto): si la imagen solo tiene una carpeta de primer nivel con
  ficheros que no sea del sistema operativo (por ejemplo `/export` de una imagen de exportación WCM), se usa esa. Si
  no, el error dice qué carpetas tienen ficheros.
- **Una carpeta equivocada ya no se reintenta siempre**: `DXWCMLibraryImport` y `DXXMLAccess` terminan en **Failed**
  con el motivo `SourceError` («… has no files under /x; folders with files: /export»), sin importar ni enviar nada.
  Antes se quedaban en Pending reintentando cada pocos minutos.

## 0.5.1 · 2026-11-18 · Las respuestas de XMLAccess nunca se recortan

- `DXXMLAccess` `output.configMap` (#17): las respuestas que pasan de 900 KiB en total (un ConfigMap admite como mucho 1 MiB)
  **ya no se recortan**. El ConfigMap solo guarda entonces `README`, que dice dónde están. Sin `output.oci` la
  ejecución queda en Failed con el motivo nuevo **`OutputTooLarge`**, que indica usar `output.oci`. Con `output.oci`
  el registro se escribe primero y las guarda todas, así que la ejecución conserva su resultado. Usa `output.oci`
  para las exportaciones.

## 0.5.0 · 2026-11-17 · Peticiones XMLAccess

- **Recurso nuevo `DXXMLAccess`** (#15): ejecuta peticiones XMLAccess propias contra el portal base o un portal virtual
  (`/wps/config/<contexto>`). Las peticiones van en línea (`xml`) o son los ficheros `.xml` de una fuente: un archivo
  por URL (por ejemplo una carpeta de Git por su URL de archivo, o una URL prefirmada de S3), una imagen OCI o un
  artefacto ORAS, o un ConfigMap. Opciones:
  - `files` fija el orden y `variables` rellena `${NOMBRE}`;
  - se comprueban todos los ficheros antes de enviar nada; `dryRun` solo los comprueba y lista;
  - para en el primer fallo salvo `continueOnError`;
  - `output.configMap` guarda las respuestas de DX (exports), y `output.oci` las sube a un registro (Harbor) como
    artefacto OCI con una etiqueta nueva en cada ejecución (`status.outputRef`). El artefacto puede ser la fuente de
    otro recurso;
  - `cleanup` envía una petición al borrar el recurso.

  Se ejecuta **una vez por cambio**, como un Job: no detecta deriva. Ejemplo 11.
- `DXXMLAccess` puede ser un `dependsOn`.
- Documentación: una [guía](docs/migration-guide.es.md) para migrar un portal y ejecutar XMLAccess, como YAML o desde
  el plugin HCL DX de Backstage (también en TechDocs).

## 0.4.0 · 2026-11-17 · Importación de bibliotecas WCM

- **Recurso nuevo `DXWCMLibraryImport`** (#11): importa bibliotecas WCM de un export en DX Compose sin ConfigEngine
  (que DX Compose no tiene), en el portal base o en un portal virtual (`virtualPortal.create` lo crea). Selección de
  bibliotecas (include, exclude, `excludeHCLDefaults`), `overwrite`, **Portal Site protegida** salvo
  `portalSiteLibraryOverwrite: true`, `mode: Import|ImportCopy`, inventario con `dryRun`, roles `access` en las
  bibliotecas importadas, lotes automáticos (`upload.maxChunkSize`, `upload.maxChunkFiles`), ejecución de una sola vez.
  Ejemplo 10. Probado en DX Compose CF239 con un export CF230 de 48 bibliotecas, desde una URL y desde Harbor, y usado
  en producción.
- **Fuentes**: los archivos por URL se descargan a disco (hasta 8 GiB, también `.tar.bz2` y `.tar`); los **artefactos
  ORAS** (`oras push <ref> fichero.tar.bz2`) se leen como ficheros; la importación WCM descomprime el archivo que traiga
  la fuente.
- **Login**: si DX no tiene la API REST (`/dx/api/core` responde 404/502/503, p. ej. un DX solo con el Web Engine), el
  operador entra con el formulario.
- **Chart**: `logFormat` (por defecto `json`) y `logEvents` (por defecto `false`: cada evento de Kubernetes también como
  línea de log, para clústeres que no recojan eventos; desactivado para no guardar cada evento dos veces).
  `DXWCMLibraryImport` puede usarse en `dependsOn`.
- **TechDocs para Backstage** (#6): `mkdocs.yml` y portada de la documentación.

## 0.3.2 · 2026-11-12 · Cookie de sesión OIDC WAS_p...

- **Corrección:** con OpenID Connect, Liberty llama a la cookie de SSO de DX `WAS_<letra><hash>`, y la letra depende del
  DX (`WAS_n...` en el DX de los labs, `WAS_p...` en otro DX Compose CF239). El operador solo aceptaba `WAS_n...`: el
  `DXConnection` se quedaba en `LoginFailed` ("DX did not open a session ... form login HTTP 302"). Ahora cualquier
  cookie `WAS_` es la sesión (`WASReqURL...` y `WASOidc...` no). Probado con DX Compose CF239 + Keycloak 26.3:
  `DXConnection` Ready, el ejemplo 01 subido y su página creada.

## 0.3.1 · 2026-11-08 · XMLAccess, de una en una

- **Corrección:** DX responde a veces con un HTTP 200 vacío cuando recibe peticiones XMLAccess simultáneas; el recurso
  pasaba a `DXError` (y ArgoCD lo mostraba Degraded) hasta la siguiente comprobación. Ahora el operador envía una sola
  petición XMLAccess a la vez a cada DX y repite dos veces una respuesta vacía (a los 2 y 4 segundos). Visto en DX
  Compose CF239 al comprobar una página y un tema en el mismo momento.

## 0.3.0 · 2026-11-08 · Prefijo DX, DXDatabase para cualquier motor, temas propios

**Cambio incompatible: los recursos se han renombrado** para que todos lleven el prefijo DX:

| Antes | Ahora | Nombre corto |
|---|---|---|
| `ScriptApplication` | `DXScriptApplication` | `dxapp`, `dxsa` |
| `DAMCollection` | `DXDamCollection` | `dxdam` |
| `PostgresDatabase` | `DXDatabase` (`superuserSecret` → `adminSecret`) | `dxdb` |

`DXConnection`, `DXPage` y `DXTheme` mantienen su nombre. **Migrar desde la 0.2:** aplica los CRDs de la 0.3.0, cambia el
`kind` de tus recursos (y `superuserSecret` / `superuserSecretNamespace` → `adminSecret` / `adminSecretNamespace`) y
vuelve a aplicarlos: el operador encuentra el contenido existente en DX por su nombre (nombre en WCM, nombre de la
colección, base de datos) y lo adopta. Después borra los recursos antiguos (sus finalizers solo los quita un operador
que los conozca: con la 0.3.0 en marcha, quítalos con `kubectl patch ... --type merge -p '{"metadata":{"finalizers":null}}'`;
no se borra nada en DX) y los CRDs antiguos.

- **DXDatabase**: PostgreSQL, **Oracle** (`oracle.service`; la base de datos es un esquema) y **SQL Server**
  (`sqlServer.encrypt`, `trustServerCertificate`), con `engine`. El mismo Secret con cualquier motor (`engine`,
  `username`, `password`, `host`, `port`, `database`, `uri`, `jdbcUrl`). Probado con PostgreSQL 17, Oracle Database 23ai
  Free y SQL Server 2022: crear, entrar como propietario, volver a reconciliar y borrar. DB2 aún no está soportado (su
  driver de Go necesita cgo).
- **DXTheme**: `base.copyFiles: false` sube solo los ficheros del origen (un tema propio, sin copiar nada de los temas de
  HCL); `profile` y `layout` fijan el perfil de agregación de recursos y el layout por defecto. El ejemplo 09 instala
  ahora el [tema de ejemplo de EUROPESIP](https://github.com/europesip/hcl-dx-theme-sample) desde su release.

 2026-11-08 · Temas

(La 0.2.0 se etiquetó pero nunca se publicó: su CI falló por la cabecera de un fichero generado.)

- Recurso nuevo **DXTheme** (nombre corto `dxtheme`): un tema WebDAV propio. El operador copia un tema base del mismo
  DX (por defecto Portal 8.5) por WebDAV, añade los ficheros de su `source` (ConfigMap, imagen o URL, como las Script
  Applications) y el HTML de `headHTML` en el `<head>` de sus plantillas, sube la carpeta y registra el tema con
  XMLAccess (copia del registro del tema base). La deriva (registro o carpeta borrados en DX) se restaura o se
  avisa; `deletionPolicy: Delete` borra el registro y la carpeta.
- Las páginas lo usan con `spec.theme` (su nombre único, por defecto `dxop.<namespace>.<nombre>`) y pueden esperarlo
  con `dependsOn: [{kind: DXTheme, name: ...}]` (tipo de dependencia nuevo).
- Ejemplo nuevo [09-theme](samples/09-theme/theme.yaml) y health check de ArgoCD para DXTheme.
- Funciona con y sin OpenID Connect (autenticación básica para WebDAV y XMLAccess). Probado con DX Compose CF239,
  con y sin Keycloak.
- **Corrección:** tras un reinicio de DX (por ejemplo un `helm upgrade` de DX) la cookie SSO antigua seguía en el
  operador, el nuevo login se saltaba el login por formulario y todas las llamadas respondían 401 hasta reiniciar el
  operador (recursos en `DXError`). Ahora cada login empieza con el almacén de cookies vacío.
- CRD nuevo: Helm solo instala los CRDs en la primera instalación; con una release existente, aplica antes
  `charts/hcl-dx-scriptapp-operator/crds/`. Los demás CRDs solo ganan el tipo de dependencia `DXTheme`.

## 0.1.3 · 2026-11-08 · Un interruptor para la compatibilidad con OpenID Connect

- Nuevo valor del chart **`dx.oidcCompatibility`** (por defecto `true`). Con `false` se desactiva el login de formulario
  de la 0.1.2 y se vuelve al comportamiento de la 0.1.1: solo el login REST de DX. Fija la variable de entorno
  `DX_FORM_LOGIN` (`auto` / `false`) del operador.

## 0.1.2 · 2026-11-08 · DX con OpenID Connect (Keycloak, Entra ID...)

- **Qué cambia:** cuando el login REST de DX (`/dx/api/core/v1/auth/login`) responde sin cookie de sesión, el operador
  entra con el formulario de login del portal (`<context root>/j_security_check`, sin seguir la redirección) y usa esa
  sesión para las llamadas a la Ring API y al DAM.
- **Por qué:** cuando DX autentica a los usuarios con OpenID Connect (la feature `openidConnectClient-1.0` de Liberty),
  Liberty llama a la cookie de SSO `WAS_n<hash>` en lugar de `LtpaToken2`, y no se puede configurar (issue 18803 de Open
  Liberty). El login REST de DX responde entonces 200 sin cookie, y todas las llamadas REST del operador daban 401: los
  recursos se quedaban en `DXError` y la DXConnection nunca estaba Ready. La Ring API y la API del DAM aceptan la cookie
  nueva. Probado con DX Compose CF239 y Keycloak 26.8.
- **En DX:** el `authFilter` de OIDC debe dejar fuera `/wps/config` (XMLAccess, como documenta HCL), `j_security_check` y
  las peticiones con `Authorization: Basic` (DXClient). Ver [docs/limitations.es.md](docs/limitations.es.md).
- **Sin OIDC no cambia nada:** el login REST devuelve `LtpaToken2` y no se hace login de formulario.
- **Volver atrás:** chart 0.1.3 con `dx.oidcCompatibility=false`, o `--version 0.1.1`.

## 0.1.1 · 2026-11-08 · Páginas adoptadas

- Una DXPage que ya estaba en DX tal como se declaraba (adoptada) registra ahora su estado aplicado. Antes, una página
  adoptada ignoraba `deletionPolicy: Delete` (se quedaba en DX al borrar el recurso) y los cambios de layout posteriores.

## 0.1.0 · 2026-11-08 · Primera versión

- ScriptApplication, DXPage, DAMCollection, PostgresDatabase y DXConnection: Script Applications subidas con HCL DXClient
  (dentro de la imagen del operador), páginas con XMLAccess, ficheros del DAM, bases de datos de los backends, detección y
  restauración de la deriva, health checks de Argo CD.
