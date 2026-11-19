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
xmlaccess/01-pages.xml     las páginas: una petición XMLAccess update, create-oids="true"
wcm/wcm-export-<nombre>.zip  las bibliotecas WCM y Portal Site: la exportación JCR del módulo de datos de WCM
```

Las anotaciones del manifiesto siguen los [atributos de los artefactos](artifact-annotations.es.md), con
`com.europesip.dx.kind: dx-bundle`, `com.europesip.dx.bundle.schema`, `com.europesip.dx.bundle.parts` y
`com.europesip.dx.pages`.

## Cómo se hace cada parte

**Páginas** (`parts.pages`): una exportación XMLAccess contra `/wps/config[/<vp>]` con
`<content-node action="export" uniquename="…" export-descendants="…"/>` por página. DX devuelve una petición update con
las páginas y los temas, skins y portlets que usan **localizados por nombre único**. El operador la convierte en una
petición para **otro** portal: `type="update" create-oids="true"` (DX da IDs nuevos), sin el `<status>`, sin los
atributos `build`/`version`, sin los comentarios (nombran el pod y su IP) y sin el `owner` del control de acceso (los
usuarios del origen no tienen por qué existir en el destino). Lo demás queda como lo escribió DX: títulos, parámetros,
layout, portlets y sus preferencias, permisos, nombres amigables y el mapeo de contenido WCM de las páginas
gestionadas. También lista lo que necesita el destino y avisa de padres sin nombre único y de páginas gestionadas sin
`parts.portalSite`.

**Portal Site y bibliotecas**: el módulo de datos de WCM, como `DXWCMLibraryExport`, en un solo zip.

## Importar un paquete

Orden: **primero las páginas, luego el zip de WCM** (Portal Site después de las páginas). El destino debe tener ya los
temas, skins y portlets que lista `EXPORT-INFO.txt`. Mismo CF que el origen o más nuevo.

**En DX Compose:** 1) `DXXMLAccess` con `source.image.ref: <paquete>` y `files: [xmlaccess/01-pages.xml]`, primero con
`dryRun: true` (Backstage: *Run XMLAccess*). 2) `DXWCMLibraryImport` con el paquete como fuente, con
`portalSiteLibraryOverwrite: true` si lleva Portal Site; prueba primero (Backstage: *Import WCM libraries*).

**En un DX tradicional sobre WebSphere:** `oras pull <paquete>`, copiar los ficheros al servidor y:

1. Páginas:
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

## Lo que todavía no va en un paquete

Temas (una versión posterior, `parts.theme`), portlets propios (sin el Java Transition Module no funcionan en
Compose), reglas de personalización (PZN), DAM, definiciones de portales virtuales y URL mappings. La importación en
el tradicional está escrita según la documentación de HCL y se va a validar en un DX 9.5 CF230 sobre WebSphere.
