# Cómo funciona el operador

[English](how-it-works.md) · [README](https://github.com/europesip/hcl-dx-scriptapp-operator/blob/main/README.es.md)

## Las piezas

- **Una sola imagen**: la imagen pública de DXClient de HCL más el binario del operador. DXClient se ejecuta
  dentro del operador para los push; no se crean Jobs ni imágenes adicionales.
- **Los orígenes** los lee el propio operador: imágenes OCI directamente del registro (sus capas, con
  `go-containerregistry`; no se ejecuta nada), ConfigMaps o archivos por HTTP(S). Se guardan en caché por
  revisión (digest de la imagen, hash del ConfigMap, SHA256 del archivo).
- **APIs de DX**: Ring API (inicio de sesión, contenido de WCM por ID y por nombre), WCM REST v2 (borrado),
  endpoint de XMLAccess `/wps/config` (páginas), API REST del DAM (colecciones, assets, renditions) y la API de
  acceso de Ring (permisos del DAM).

## El bucle de reconciliación

Cada recurso se reconcilia cuando cambia, cuando cambia un objeto al que hace referencia (su ConfigMap, la
colección del DAM de una sustitución, el Deployment del que depende, su conexión...) y cada `resyncInterval` de su
DXConnection (10 minutos por defecto). En cada pasada:

1. **Borrado** (si el recurso se está borrando): con `deletionPolicy: Delete`, quita el objeto de DX; después
   libera el finalizer. `Retain` (por defecto) deja DX como está.
2. **Suspend**: con `spec.suspend: true` se detiene aquí.
3. **Dependencias**: cada `dependsOn` y las implícitas (las colecciones del DAM de las sustituciones, las Script
   Applications de una página). Si alguna no está lista: `Ready=False, reason Waiting`, y se reintenta en 20 s.
4. **Conexión**: la DXConnection debe estar Ready.
5. **Estado deseado**: lo que declara el recurso, como un hash.
6. **Estado real** en DX, y la decisión: crear, actualizar, restaurar, avisar o nada.
7. **Status y eventos**: condiciones `Ready`, `Drifted` y `DependenciesReady`, y eventos de Kubernetes.

Las escrituras en el status son merge patches, así que dos pasadas simultáneas no pierden lo hecho en DX.

### DXScriptApplication

- **Preparar**: obtener el origen; copiarlo a una carpeta de trabajo; sustituir los `${NOMBRE}` de
  `substitutions` en los ficheros de texto (`.html .htm .js .mjs .css .json .txt .svg .xml`; los demás `${...}`
  se dejan tal cual); comprobar que existe el HTML principal y que ningún fichero pasa de 4,5 MB.
- **Hash** de todos los ficheros preparados más los ajustes de WCM (nombre, título, site area, fichero principal,
  proyecto) y la anotación de forzado.
- **Decidir**:
  - sin content ID todavía → push (`Created`);
  - el hash cambió → push (`Updated`);
  - si no, leer el contenido en DX por ID (Ring API): 404 → se **borró** en DX; una fecha `updated` distinta de
    la registrada tras el último push → se **modificó** en DX. Con `driftPolicy: Restore` se vuelve a subir
    (`Restored`; tras un borrado WCM crea un elemento nuevo, con content ID nuevo); con `Report` se marca
    `Drifted` y no se toca.
- **Push**: `dxclient deploy-scriptapplication push`, con la conexión en el fichero de configuración de DXClient
  (que se borra después). Solo se da por bueno si DXClient termina con 0 **y** la respuesta de DX tiene
  `"status":"success"` (DXClient dice «successful» aunque DX rechace el contenido). Los errores temporales de un
  DX que está arrancando (Service Unavailable, Bad Gateway, conexión cortada...) se reintentan 5 veces, cada 20 s.
- **Registrar** el content ID, el hash, la revisión del origen, la fecha `updated`, el tamaño y un historial de
  los 10 últimos push.
- **Modo Verify**: nunca sube nada; busca el contenido por su nombre de WCM e informa Ready o `NotFound`.
- **Borrar** (política Delete): WCM REST v2 `DELETE /wcmrest-v2/contents/{id}`. No se usa
  `undeploy-scriptapplication` de DXClient porque se niega mientras una página borrada siga referenciando la
  aplicación (DX elimina las páginas borradas más tarde, con su tarea de limpieza).

### DXPage

- **Deseado**: los content ID de sus Script Applications (en orden), títulos, padre, nombre amigable, plantilla de
  layout, skin, tema, acceso y posición, como un hash. Un segundo hash cubre solo la lista de aplicaciones (el
  layout).
- **Real**: un export de XMLAccess por nombre único. `EJPXA0240E` significa que la página no existe; cualquier
  otro error es un error (un fallo temporal no debe tomarse por una página borrada, o se crearía dos veces). El
  hash aplicado por el operador se guarda en la propia página, como el parámetro `dxop.hash`.
- **Decidir**: no existe y nunca se creó → crearla con su layout; no existe tras haberla creado → se borró en DX
  (restaurar o avisar); cambió la lista de aplicaciones → borrarla y crearla de nuevo (XMLAccess no puede
  sustituir los componentes de una página); cambió otro ajuste → actualizar títulos, parámetros y acceso sin
  tocar el layout (los ajustes que la gente hace en la página se conservan); el `dxop.hash` de DX es distinto
  del aplicado → alguien cambió los ajustes en DX (restaurar o avisar).
- Una Script Application que recibe un content ID nuevo (restaurada tras un borrado) dispara sus páginas: cambia
  el hash del layout y la página se reconstruye.

### DXTheme

- **Deseado**: el spec (carpeta, nombre único, títulos, base, perfil, layout, HTML de cabecera) y la revisión de su
  origen, como un hash.
- **Real**: un export de XMLAccess del tema por nombre único (el hash aplicado se guarda en el tema, en el parámetro
  `dxop.hash`) y un listado WebDAV de su carpeta (`dav:fs-type1/themes/<carpeta>/`).
- **Aplicar**: con `base.copyFiles: false` (un tema propio) solo los ficheros del origen; si no, descargar antes todos
  los ficheros del tema base por WebDAV y poner encima los del origen (mismas rutas); después
  insertar el HTML de cabecera antes de `</head>` en `theme.html`, `theme_sidenav.html` y sus versiones por idioma,
  subir la carpeta (MKCOL + PUT) y registrar el tema con una copia del registro del tema base (nombre único nuevo,
  plantillas apuntando a la carpeta nueva, `default="false"`, solo los títulos declarados, y `profile` / `layout` si
  se indican).
- **Deriva**: falta el registro o la carpeta en DX → subirlo y registrarlo otra vez (`Restore`) o avisar.
- **Borrar** (política Delete): borrar el registro (XMLAccess) y la carpeta (WebDAV). Las páginas que lo usan pasan
  al tema por defecto.
- WebDAV y XMLAccess usan autenticación básica: el tema funciona igual con DX detrás de OpenID Connect.
- No se usa `deploy-theme` de DXClient: su paso de registro falla en DX Compose CF239.

### DXDamCollection

- Buscar la colección por el ID del status, o por nombre (así se adopta una existente), o crearla; dar el rol User
  a los anónimos y/o a todos los usuarios autenticados.
- Para cada fichero del origen (assets con el nombre de su ruta, `/` → `-`): sin cambios (mismo SHA256 registrado)
  → nada; cambiado → `PUT` de una versión nueva del mismo asset (mismo ID y URL); no está en DX → subirlo de nuevo
  (restaurar o avisar); ya hay un asset con ese nombre → adoptarlo. Después esperar a su rendition original y
  registrar su URL.
- `prune: true` borra los assets que ya no están en el origen.
- Borrar (política Delete): la colección, ignorando las referencias.

### DXDatabase

- Un Secret `<nombre>-db` (o `secretName`) con los datos de conexión; la contraseña se genera una vez y se
  conserva, y el Secret pertenece al recurso.
- Con el Secret del administrador, según el motor (se revisa cada 30 minutos):
  - **PostgreSQL**: crear o modificar el rol (login, contraseña), crear la base de datos con ese propietario si falta
    y crear las extensiones.
  - **Oracle** (`oracle.service`, la PDB): la base de datos es un esquema: crear el usuario (tablespace por defecto y
    cuota) con permisos para crear tablas, vistas, secuencias, procedimientos, triggers, tipos y sinónimos; a un
    usuario existente se le vuelve a poner la contraseña.
  - **SQL Server**: crear o modificar el login, crear la base de datos si falta, su usuario para ese login y la
    pertenencia a `db_owner`.
- Borrar (política Delete): PostgreSQL `DROP DATABASE ... WITH (FORCE)` y `DROP ROLE`; Oracle `DROP USER ... CASCADE`;
  SQL Server `DROP DATABASE` (antes en modo de un solo usuario) y `DROP LOGIN`.
- El Secret tiene las mismas claves con cualquier motor: `engine`, `username`, `password`, `host`, `port`, `database`,
  `uri` y `jdbcUrl` (y `service` con Oracle).

### DXConnection

Inicia sesión en DX con una sesión nueva (`/dx/api/core/v1/auth/login`) y comprueba que DXClient puede ejecutarse
(licencia aceptada, ficheros presentes). Un cambio en sus Secrets dispara una nueva comprobación.

## Concurrencia y rendimiento

- Una réplica activa (leader election). Las ejecuciones de DXClient se **serializan** (de una en una): DXClient
  guarda sus ajustes en su propia carpeta, y DX tampoco lleva bien los push en paralelo. Las llamadas REST van en
  paralelo.
- Un push tarda unos segundos; una página, alrededor de un segundo; una revisión (resync), una o dos llamadas REST
  por recurso.
- Las sesiones REST se reutilizan por conexión mientras no cambien sus Secrets.

## Forzar y pausar

- `metadata.annotations["dx.europesip.com/reconcile"]`: cambia su valor para reconciliar al momento; en una
  Script Application fuerza además un push (el valor forma parte del hash).
- `spec.suspend: true`: no se hace nada hasta que vuelva a ser false.

## Status y eventos

```bash
kubectl get dxscriptapplication vacaciones -o yaml        # status.contentId, history, conditions
kubectl get events --field-selector involvedObject.name=vacaciones
```

Eventos: `Pushed` (Created / Updated / Restored), `Drifted`, `PushFailed`, `Created`, `Rebuilt`, `Synced`,
`Deleted`, `Undeployed`, `Dropped`, `Connected`, `LoginFailed`...

## Importación de bibliotecas WCM (DXWCMLibraryImport, 0.4.0)

DX Compose no tiene ConfigEngine, y DXClient `wcm-library-import` depende de él. El operador usa lo que DX Compose
ofrece por HTTP:

1. **Portal virtual**: se lista (y se crea con `create: true`) por XMLAccess con el esquema `PortalVPHandler.xsd`. Si
   no existe, la ejecución termina antes de descargar nada.
2. **Export**: se obtiene como cualquier fuente (URL a disco, imagen OCI o artefacto ORAS con el fichero); si la fuente
   trae un fichero comprimido, se descomprime. Las bibliotecas son los `<biblioteca>.node` del export, con su `icm:title`.
3. **Selección**: include, exclude, `excludeHCLDefaults`; las que ya existen en el destino (WCM REST
   `query?type=Library`) se saltan salvo `overwrite`; **Portal Site** (ID fijo `TWFuYWdlZCBQYWdlcyBSb290IExpYnJhcnk`)
   solo con `portalSiteLibraryOverwrite`. Las bibliotecas con nombres que dxFileTransfer rechaza se informan y no se suben.
4. **Lotes** de como máximo `upload.maxChunkSize` (450Mi) y `upload.maxChunkFiles` (4000): cada uno se sube con
   `dxFileTransfer` (`unzip=true`), se importa con el módulo de datos de WCM
   (`/wps/wcm/myconnect[/<vp>]?MOD=data&taskType=import|import-copy&input.dir=...`) y se borra su carpeta. Si un proxy
   corta la petición (502/503/504), la carpeta se conserva: DX sigue importando.
5. **Comprobación y roles**: se vuelven a listar las bibliotecas y se añaden los roles de `access`.

Es una operación **de una sola vez**: el estado guarda un hash del spec y solo se vuelve a importar si cambia el spec o
la anotación `dx.europesip.com/reconcile`. Las importaciones a un DX nunca coinciden con sus llamadas XMLAccess.

## Peticiones XMLAccess (DXXMLAccess, 0.5.0)

`DXXMLAccess` ejecuta peticiones XMLAccess propias: la forma de hacer lo que aún no cubren los recursos con tipo
(mapeos de URL, políticas, credential vault, WSRP, etiquetas, exports...). Funciona **como un Job**, no como `DXPage`:

1. **Peticiones**: `spec.xml` (una, en línea) o los ficheros `.xml` de una fuente (archivo por URL, imagen OCI o
   artefacto ORAS, ConfigMap), en el orden de `files` o por orden de ruta. `${NOMBRE}` se sustituye con `variables`.
2. **Primero se comprueban**: cada fichero tiene que ser XML bien formado con `<request type="...">`. Si alguno no lo
   es, **no se envía nada**. Con `dryRun: true` se para aquí y lista las peticiones.
3. **Ejecución**: cada petición va a `/wps/config` (o a `/wps/config/<contexto>` con `virtualPortal`), de una en una por
   DX (el mismo cerrojo que el resto de llamadas XMLAccess del operador), con `timeout` (10m). El primer fallo para la
   ejecución salvo `continueOnError`; las siguientes quedan como `skipped`.
4. **Resultado**: `status.results` (fichero, tipo de petición, `ok` / `error` / `skipped`, el primer mensaje de DX) y,
   con `output.configMap`, la respuesta de DX a cada petición en un ConfigMap propiedad del recurso (como mucho 900 KiB en total: las respuestas mayores nunca se recortan, la ejecución falla con `OutputTooLarge` salvo que también esté `output.oci`);
   con `output.oci`, las respuestas y un `results.json` se suben a un registro (Harbor) como artefacto OCI, un fichero
   por petición (como hace `oras push`), con una etiqueta nueva en cada ejecución (`${DATE}`): con versión, sin límite
   de tamaño y legible como `source` de otro recurso, por ejemplo para aplicar un export en otro entorno.

Se ejecuta **una vez por cambio**: el hash del spec, la revisión de la fuente (digest de la imagen, SHA256 del archivo,
ConfigMap) y la anotación `dx.europesip.com/reconcile`. El operador no sabe qué creó una petición cualquiera, así que no
busca deriva. Borrar el recurso no cambia nada en DX, salvo que `cleanup` indique una petición (en línea o un fichero de
la fuente) para ese momento; si falla, se reintenta 10 minutos, se avisa con un evento y el recurso se borra igualmente.
Mejor peticiones idempotentes (`action="update"` / `"locate"`): volver a ejecutarlas no hace daño.

