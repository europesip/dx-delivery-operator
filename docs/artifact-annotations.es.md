# Atributos de los artefactos

[English](artifact-annotations.md) · **Español**

Cada artefacto que sube el operador (exportaciones WCM de `DXWCMLibraryExport`, paquetes de `DXExport`, respuestas de
`DXXMLAccess`) se describe con **anotaciones del manifiesto**: las estándar `org.opencontainers.image.*` y las nuestras
`com.europesip.dx.*`. El mismo formato lo escribe **DX Transfer Studio** y lo lee Backstage.

**Esquema 2** (`com.europesip.dx.schema: "2"`). Los lectores aceptan también el esquema 1.

## Tipos y carpetas

| `com.europesip.dx.kind` | Carpeta | Qué lleva |
|---|---|---|
| `wcm` | `<proyecto>/wcm/<origen>-<qué>:<fecha>` | solo librerías WCM: `wcm-export-<nombre>.zip` + `EXPORT-INFO.txt` |
| `site` | `<proyecto>/sites/<origen>-<qué>:<fecha>` | un paquete (`dx-bundle/v1`): `EXPORT-INFO.txt`, `bundle.json`, `themes/`, `xmlaccess/01-pages.xml`, `wcm/`; qué lleva en `com.europesip.dx.bundle.parts` |
| `theme` | `<proyecto>/themes/<origen>-<tema>:<fecha>` | un paquete (`dx-bundle/v1`, piezas `themes`) con **solo temas**: `EXPORT-INFO.txt`, `bundle.json`, `themes/theme-<slug>.xml` (registro y skins, XMLAccess) y `themes/theme-<slug>-webdav.zip` (carpeta WebDAV). Desde 0.12.0 (#49): `DXExport` con solo `parts.themes` |
| `app` | `<proyecto>/apps/<origen>-<qué>:<fecha>` | aplicaciones (#50): `scriptapps/<slug>.zip` (Script Applications, en el formato de `dxclient deploy-scriptapplication`), `portlets/<slug>.xml` (XMLAccess del módulo web) y `portlets/<slug>.war` si se pudo obtener. Lo genera DX Transfer Studio 0.2.5; el operador lo reconoce pero aún no lo importa |
| `xmlaccess` | `<proyecto>/xmlaccess/<recurso>:<hora>` | respuestas de `DXXMLAccess` |
| `portal` | `<proyecto>/portal/<origen>-<qué>:<fecha>` | **reservado** para un portal completo (exportación release de XMLAccess). Aún no se genera. **Nunca** con credenciales: el `Export.xml` completo lleva configuración de administración y datos del almacén de credenciales |

## Anotaciones

| Anotación | Valor | Ejemplo |
|---|---|---|
| `org.opencontainers.image.title` / `.description` / `.authors` / `.created` / `.source` / `.vendor` | título, descripción, quién, cuándo, URL del DX, `EUROPESIP` | |
| `com.europesip.dx.schema` | `2` | |
| `com.europesip.dx.kind` | `wcm`, `site`, `theme`, `app`, `xmlaccess` (`portal` reservado) | `wcm` |
| `com.europesip.dx.bundle.schema` / `.bundle.parts` | `dx-bundle/v1` / `themes,pages,portal-site,libraries` | |
| `com.europesip.dx.source.server` | **Nombre corto** del DX (`DXConnection.spec.sourceName`; en DX Transfer Studio la etiqueta del servidor); por defecto el nombre de la conexión | `wps01` |
| `com.europesip.dx.source.url` | URL del DX | `https://dx.example.com` |
| `com.europesip.dx.source.version` | Versión que da DX | `HCL Digital Experience/8.5 build …` |
| `com.europesip.dx.source.connection` | DXConnection o id del servidor | `dxop-core-sergio/sergio-cf230` |
| `com.europesip.dx.source.virtual-portal` | Portal virtual; vacío = portal base | `aeat` |
| `com.europesip.dx.source.portal` | Mismo valor (nombre del esquema 1, se mantiene una versión) | `aeat` |
| `com.europesip.dx.libraries` / `.libraries.count` | Títulos separados por ` \| ` / cuántas | `AEAT_DS \| AEAT_Contenido` / `2` |
| `com.europesip.dx.pages` | Unique names de las páginas raíz, separados por ` \| ` | `EuropeSip_web` |
| `com.europesip.dx.themes` | Unique names de los temas, separados por ` \| ` | `custom.portal.theme.Orbita` | temas, y sitios que llevan tema |
| `com.europesip.dx.scriptapps` | Nombres de las Script Applications, separados por ` \| ` | `Hello DX \| Store Locator` | aplicaciones |
| `com.europesip.dx.portlets` | Nombres de los portlets / módulos web, separados por ` \| ` | `Order Status` | aplicaciones |
| `com.europesip.dx.portlets.jsr` | `168`, `286` o `mixed` | `286` | aplicaciones: los portlets propios JSR 168/286 necesitan el Java Transition Module de HCL en DX Compose |
| `com.europesip.dx.portlets.war` | `included` o `missing` | `included` | aplicaciones: si el WAR va en el artefacto |
| `com.europesip.dx.requested-by` | Quién (`spec.destination.authors` o la anotación `dx.europesip.com/changed-by`) | `andres` |
| `com.europesip.dx.reason` | Por qué (`spec.destination.reason`, la anotación `dx.europesip.com/reason` o el atributo `reason`) | `Migración CF230 → Compose` |
| `com.europesip.dx.attr.<clave>` | Atributos libres | `attr.customer: aeat` |
| `com.europesip.dx.tool.operator` / `com.europesip.dx.tool` | El operador / DX Transfer Studio y su versión | |

## Importar un artefacto `theme` o `app`

- **`theme`** (0.12.0): igual que los temas de un paquete de sitio. Con el operador: un `DXTheme` con
  `registration: FilesOnly` cuyo `source` lleva descomprimido `themes/theme-<slug>-webdav.zip`, y después un
  `DXXMLAccess` con `themes/theme-<slug>.xml` (conserva sus IDs y skins). DX Transfer Studio lo importa en un paso.
- **`app`** (#50): se reconoce (tipo, carpeta, anotaciones), el operador aún no lo importa. Script Applications:
  `DXScriptApplication` con el zip como origen; portlets: `DXXMLAccess` con `portlets/<slug>.xml` una vez desplegado
  el WAR (DX Core). En DX Compose los portlets propios JSR 168/286 necesitan el Java Transition Module de HCL: avisar
  antes de importar. DX Transfer Studio 0.2.5 importa los dos.

## Artefactos antiguos

Los lectores aceptan los tipos del esquema 1 y de la guía v1 (`wcm-export` → `wcm`; `dx-bundle`, `pages` → `site`;
`theme`, `themes` → `theme` y `app`, `apps` → `app` desde 0.12.0, los paquetes de solo temas anteriores siguen con
`kind: site` y `bundle.parts: themes`; `xmlaccess-output` → `xmlaccess`), listas separadas por ` | ` o por comas, el portal virtual en
`source.virtual-portal` o `source.portal`, y `source.server` con una URL.
