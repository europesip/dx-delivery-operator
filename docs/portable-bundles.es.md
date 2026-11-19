# Paquetes portables (DXExport)

[English](portable-bundles.md) · **Español**

Un **DX bundle** es lo que `DXExport` sube a un registro: un artefacto OCI (ORAS, sin *artifact type*) con páginas, la
biblioteca Portal Site y bibliotecas WCM de un portal DX Compose, **cada fichero en el formato nativo de HCL DX**. El
mismo paquete se importa en otro **DX Compose** (con el operador o desde Backstage) y en un **HCL DX tradicional sobre
WebSphere** (8.5, 9.0, 9.5) con sus herramientas, `xmlaccess.sh` y `ConfigEngine.sh`.

## Formato: `dx-bundle/v1`

```
EXPORT-INFO.txt            para personas: origen, versiones, quién y por qué, contenido, avisos, CÓMO IMPORTARLO
bundle.json                para herramientas: lo mismo, con los pasos de importación y tamaño y sha256 de cada fichero
xmlaccess/01-pages.xml     las páginas: una petición XMLAccess update con los IDs de objeto del origen
wcm/wcm-export-<nombre>.zip  las bibliotecas WCM y Portal Site: la exportación JCR del módulo de datos de WCM
```

Las anotaciones del manifiesto siguen los [atributos de los artefactos](artifact-annotations.es.md), con
`com.europesip.dx.kind: dx-bundle`, `com.europesip.dx.bundle.schema`, `com.europesip.dx.bundle.parts` y
`com.europesip.dx.pages`.

## Cómo se hace cada parte

**Páginas** (`parts.pages`): una exportación XMLAccess contra `/wps/config[/<vp>]` con
`<content-node action="export" uniquename="…" export-descendants="…"/>` por página. DX devuelve una petición update con
las páginas y los temas, skins y portlets que usan **localizados por nombre único**. El operador la convierte en una
petición importable: `type="update"` **conservando los IDs de objeto del origen** tal como los exportó DX (desde la
0.9.1; la 0.9.0 forzaba IDs nuevos: mantenerlos o no se elige al importar, ver *IDs de objeto*), sin el `<status>`,
sin los atributos `build`/`version`, sin los comentarios (nombran el pod y su IP) y sin el `owner` del control de
acceso, también al mantener IDs (los usuarios del origen no tienen por qué existir en el destino y un owner
desconocido hace fallar XMLAccess; el administrador que importa queda como propietario y se conservan los roles). Lo demás queda como lo escribió DX: títulos, parámetros,
layout, portlets y sus preferencias, permisos, nombres amigables y el mapeo de contenido WCM de las páginas
gestionadas. También lista lo que necesita el destino y avisa de padres sin nombre único y de páginas gestionadas sin
`parts.portalSite`.

**Portal Site y bibliotecas**: el módulo de datos de WCM, como `DXWCMLibraryExport`, en un solo zip.

## IDs de objeto: mantener o nuevos, se elige al importar

Los ficheros conservan los IDs de objeto de las páginas y los UUID de los elementos WCM tal como están en el origen.
Como en WCM import / import-copy, se elige **al importar**, así que vale igual para los paquetes y para un XML
exportado con `xmlaccess.sh` o `export-wcm-data` de un DX tradicional:

| Opción | Páginas | Portal Site y bibliotecas | Cuándo |
|---|---|---|---|
| **Mantener** (recomendada, por defecto) | `DXXMLAccess` `objectIDs: Keep`: la petición se envía tal cual. Tradicional: `xmlaccess.sh -in xmlaccess/01-pages.xml …` | `DXWCMLibraryImport` `mode: Import`. Tradicional: `ConfigEngine import-wcm-data` | Staging → producción, un portal que luego se sindica (la sindicación empareja por ID); importar otra vez actualiza lo que existe |
| **Nuevos** | `DXXMLAccess` `objectIDs: New`: `create-oids="true"` en cada `<request>`. Tradicional: `sed -i '0,/<request /s//<request create-oids="true" /' xmlaccess/01-pages.xml` y luego `xmlaccess.sh` | `DXWCMLibraryImport` `mode: ImportCopy`. Tradicional: el módulo de datos con `taskType=import-copy` (`import-wcm-data` no tiene modo copia) | Una copia en un portal donde los IDs chocarían |

**Usa la misma opción para las páginas y para Portal Site.** Las páginas gestionadas se asocian con su contenido de
Portal Site por ID (`content-mapping-info`): mezclarlas deja páginas apuntando a contenido que no existe. La copia
con IDs nuevos de páginas gestionadas con su Portal Site no está verificada todavía: pruébala antes en un portal de
pruebas.

## Importar un paquete

Orden: **primero las páginas, luego el zip de WCM** (Portal Site después de las páginas). El destino debe tener ya los
temas, skins y portlets que lista `EXPORT-INFO.txt`. Mismo CF que el origen o más nuevo.

**En DX Compose:** 1) `DXXMLAccess` con `source.image.ref: <paquete>`, `files: [xmlaccess/01-pages.xml]` y
`objectIDs: Keep` (o `New`), primero con `dryRun: true` (Backstage: *Run XMLAccess*). 2) `DXWCMLibraryImport` con el
paquete como fuente, `mode: Import` (o `ImportCopy`, a juego con el paso 1), con `portalSiteLibraryOverwrite: true`
si lleva Portal Site; prueba primero (Backstage: *Import WCM libraries*).

**En un DX tradicional sobre WebSphere:** `oras pull <paquete>`, copiar los ficheros al servidor y:

1. Páginas (mantener IDs; para IDs nuevos, antes `sed -i '0,/<request /s//<request create-oids="true" /' xmlaccess/01-pages.xml`):
   ```
   <wp_profile>/PortalServer/bin/xmlaccess.sh -in xmlaccess/01-pages.xml -user <admin> -password <pwd> \
     -url https://<host>:<puerto>/wps/config[/<portal virtual>] -out result.xml
   ```
2. Bibliotecas y Portal Site: descomprimir el zip de `wcm/` en una carpeta y
   ```
   <wp_profile>/ConfigEngine/ConfigEngine.sh import-wcm-data -Dimport.directory=<carpeta> \
     [-DVirtualPortalContext=<contexto>] -DWasPassword=<pwd> -DPortalAdminPwd=<pwd>
   ```
   Importa todas las bibliotecas de la carpeta. HCL pide después reiniciar el registro de eventos de WCM y el servidor.
   Con IDs nuevos: con la sesión iniciada en el portal,
   `/wps/wcm/myconnect[/<vp>]?MOD=data&processLibraries=false&taskType=import-copy&input.dir=<carpeta>`
   (`import-wcm-data` no tiene modo copia).

## Lo que todavía no va en un paquete

Temas (una versión posterior, `parts.theme`), portlets propios (sin el Java Transition Module no funcionan en
Compose), reglas de personalización (PZN), DAM, definiciones de portales virtuales y URL mappings. La importación en
el tradicional está escrita según la documentación de HCL y se va a validar en un DX 9.5 CF230 sobre WebSphere.
