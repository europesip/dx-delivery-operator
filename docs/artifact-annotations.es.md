# Atributos de los artefactos

[English](artifact-annotations.md) · **Español**

Cada artefacto que el operador sube a un registro (las exportaciones WCM de `DXWCMLibraryExport` y las respuestas de
`DXXMLAccess`) se describe a sí mismo con **anotaciones del manifiesto**: las estándar `org.opencontainers.image.*` y
las nuestras, `com.europesip.dx.*`. Los registros las muestran (Harbor: *Annotations* del artefacto;
`oras manifest fetch <ref>`), y una herramienta puede listar o filtrar artefactos por ellas sin descargar los ficheros.
No se pone *artifact type*: las exportaciones WCM se reconocen por la carpeta `wcm/` de su repositorio y por
`com.europesip.dx.kind`.

Versión del esquema: `com.europesip.dx.schema: "1"`. Se pueden añadir claves nuevas; las que hay mantienen su
significado.

| Anotación | Valor |
|---|---|
| `org.opencontainers.image.created` | Cuándo se subió (RFC 3339, UTC) |
| `org.opencontainers.image.title` / `.description` | Nombre corto / una línea |
| `org.opencontainers.image.source` | El DX de origen |
| `org.opencontainers.image.vendor` | `EUROPESIP` |
| `com.europesip.dx.schema` | Versión de esta convención (`1`) |
| `com.europesip.dx.kind` | `wcm-export` o `xmlaccess-output` |
| `com.europesip.dx.resource` | El recurso que lo creó (namespace/nombre) |
| `com.europesip.dx.source.server` / `.version` / `.connection` / `.portal` | Servidor DX, versión que da DX, DXConnection, portal virtual (vacío: el portal base) |
| `com.europesip.dx.libraries` / `.libraries.count` | Exportaciones WCM: los títulos (separados por ` \| `) y cuántas |
| `com.europesip.dx.requested-by` | Quién lo pidió (anotación `dx.europesip.com/changed-by` del recurso) |
| `com.europesip.dx.reason` | Por qué (anotación `dx.europesip.com/reason`) |
| `com.europesip.dx.tool.operator` | Este operador y su versión |
| `com.europesip.dx.tool.created-with` | Con qué se creó el recurso (anotación `dx.europesip.com/created-with`: Backstage pone su versión y la del plugin) |
| `com.europesip.dx.attr.<clave>` | Atributos libres: `spec.destination.attributes` de un `DXWCMLibraryExport` |

Atributos libres: claves en minúsculas, dígitos, puntos y guiones (63 caracteres como mucho), valores de 256 caracteres
como mucho, 32 por artefacto. Los vacíos no se escriben. Claves sugeridas: `environment`, `customer`, `project`,
`ticket`, `release`.

Lo mismo va en `EXPORT-INFO.txt` de una exportación WCM, para quien solo tenga los ficheros.
