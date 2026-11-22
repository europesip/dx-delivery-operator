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
| `xmlaccess` | `<proyecto>/xmlaccess/<recurso>:<hora>` | respuestas de `DXXMLAccess` |
| `portal` | `<proyecto>/portal/<origen>-<qué>:<fecha>` | **reservado** para un portal completo (exportación release de XMLAccess). Aún no se genera. **Nunca** con credenciales: el `Export.xml` completo lleva configuración de administración y datos del almacén de credenciales |

## Anotaciones

| Anotación | Valor | Ejemplo |
|---|---|---|
| `org.opencontainers.image.title` / `.description` / `.authors` / `.created` / `.source` / `.vendor` | título, descripción, quién, cuándo, URL del DX, `EUROPESIP` | |
| `com.europesip.dx.schema` | `2` | |
| `com.europesip.dx.kind` | `wcm`, `site`, `xmlaccess` (`portal` reservado) | `wcm` |
| `com.europesip.dx.bundle.schema` / `.bundle.parts` | `dx-bundle/v1` / `themes,pages,portal-site,libraries` | |
| `com.europesip.dx.source.server` | **Nombre corto** del DX (`DXConnection.spec.sourceName`; en DX Transfer Studio la etiqueta del servidor); por defecto el nombre de la conexión | `wps01` |
| `com.europesip.dx.source.url` | URL del DX | `https://dx.example.com` |
| `com.europesip.dx.source.version` | Versión que da DX | `HCL Digital Experience/8.5 build …` |
| `com.europesip.dx.source.connection` | DXConnection o id del servidor | `dxop-core-sergio/sergio-cf230` |
| `com.europesip.dx.source.virtual-portal` | Portal virtual; vacío = portal base | `aeat` |
| `com.europesip.dx.source.portal` | Mismo valor (nombre del esquema 1, se mantiene una versión) | `aeat` |
| `com.europesip.dx.libraries` / `.libraries.count` | Títulos separados por ` \| ` / cuántas | `AEAT_DS \| AEAT_Contenido` / `2` |
| `com.europesip.dx.pages` | Unique names de las páginas raíz, separados por ` \| ` | `EuropeSip_web` |
| `com.europesip.dx.requested-by` | Quién (`spec.destination.authors` o la anotación `dx.europesip.com/changed-by`) | `andres` |
| `com.europesip.dx.reason` | Por qué (`spec.destination.reason`, la anotación `dx.europesip.com/reason` o el atributo `reason`) | `Migración CF230 → Compose` |
| `com.europesip.dx.attr.<clave>` | Atributos libres | `attr.customer: aeat` |
| `com.europesip.dx.tool.operator` / `com.europesip.dx.tool` | El operador / DX Transfer Studio y su versión | |

## Artefactos antiguos

Los lectores aceptan los tipos del esquema 1 y de la guía v1 (`wcm-export` → `wcm`; `dx-bundle`, `pages`, `theme` →
`site`; `xmlaccess-output` → `xmlaccess`), listas separadas por ` | ` o por comas, el portal virtual en
`source.virtual-portal` o `source.portal`, y `source.server` con una URL.
