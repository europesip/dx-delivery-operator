# Guía: llevar un portal a DX Compose y ejecutar XMLAccess

[English](migration-guide.md) · **Español**

Cómo usar los recursos de importación del operador, paso a paso: bibliotecas WCM (`DXWCMLibraryImport`), el contenido
de las páginas gestionadas (Portal Site) y peticiones XMLAccess propias (`DXXMLAccess`). Cada paso se puede escribir
como YAML (GitOps) o lanzar desde **DX Backstage** (nuestro plugin de Backstage), que escribe ese mismo YAML por ti.

## Antes de empezar

1. **Un `DXConnection`** al DX de destino, con un administrador de DX. Desde Backstage: pestaña **Connections** →
   *New connection* (queda en el namespace `dx-portals`, contraseña en OpenBao).
2. **Los exports en un registro** (Harbor), tal cual los entrega el portal de origen:
   ```bash
   oras login harbor.example.com -u <usuario>          # contraseña: tu CLI secret de Harbor
   oras push harbor.example.com/dx-wcm/bibliotecas-cf230:2026-11-18 export-europesip.tar.bz2 LEEME.txt
   ```
   O una URL (`.tar.bz2`, `.tar.gz`, `.zip`) con su SHA256.
3. **Un pull secret** (`kubernetes.io/dockerconfigjson`) del registro en el namespace de los recursos.

Orden de una migración: portal virtual → temas → bibliotecas WCM (sin Portal Site) → páginas → **Portal Site** →
permisos → mapeos de URL y resto. Siempre primero una **simulación**.

## 1. Bibliotecas WCM

**Desde Backstage:** pestaña **Web content**, elige entorno y portal en la cabecera → **Import WCM libraries**. El
asistente pide la fuente (la lista del proyecto de Harbor, lo más nuevo primero), las opciones (por defecto: solo
bibliotecas propias, saltar las que existen, Portal Site sin tocar, dxadmins Administrator), lanza una **simulación**
y enseña el inventario; **Import** la ejecuta de verdad. El progreso y el historial quedan en la pestaña.

**Como YAML:** mira el [ejemplo 10](https://github.com/europesip/dx-delivery-operator/tree/main/samples/10-wcm-library-import).

```bash
kubectl get dxwcmimport -n <ns>                         # Phase, Ready, Reason
kubectl get dxwcmimport <nombre> -o jsonpath='{.status.message}'
```

Después pon `dryRun: false` (o quítalo): la importación se ejecuta una vez. Borrar el recurso nunca borra bibliotecas.

## 2. Contenido de las páginas gestionadas (Portal Site)

Se importa **después de las páginas**. Desde Backstage: pestaña **Pages** → **Import page content (Portal Site)**
(aviso en rojo: sustituye el contenido de las páginas gestionadas de ese portal). Como YAML: un `DXWCMLibraryImport`
con `libraries.include: [Portal Site]`, `overwrite: true` y `portalSiteLibraryOverwrite: true`.

## 3. Páginas y temas

- **Página nueva:** pestaña **Pages** → *New page* (un `DXPage`: títulos, URL amigable, página padre, tema, acceso
  anónimo).
- **Tema nuevo:** pestaña **Themes** → *New theme* (un `DXTheme` copiado de Portal 8.5) o *Import theme files* (un zip o
  un artefacto ORAS con los ficheros de un tema, encima de la copia).
- Importar el árbol de páginas de un portal tradicional es `DXPageImport` (issue #13, lo siguiente).

## 4. Peticiones XMLAccess propias (`DXXMLAccess`)

Para todo lo que no tiene recurso propio. Ejemplos:

**Exportar algo y guardar la respuesta** (solo lectura, seguro):
```yaml
apiVersion: dx.europesip.com/v1alpha1
kind: DXXMLAccess
metadata: { name: export-url-mappings }
spec:
  connectionRef: { name: dx }
  xml: |
    <request xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="PortalConfig_8.5.0.xsd" type="export">
      <portal action="locate"><url-mapping-context action="export" objectid="*"/></portal>
    </request>
  output: { configMap: { name: url-mappings-export } }
```
```bash
kubectl get configmap url-mappings-export -o jsonpath='{.data.request\.xml}'
```

**Guardar el export en Harbor y aplicarlo en otro entorno.** Un artefacto OCI es el formato estándar de los registros
de contenedores: además de imágenes, puede guardar cualquier fichero, con versión (etiqueta), fecha y autor. Con
`output.oci` las respuestas de DX se suben ahí:
```yaml
  output:
    oci:
      repository: harbor.example.com/dx-exports/url-mappings
      tag: dev-${DATE}                         # por defecto ${DATE}: aaaammdd-hhmmss de la ejecución
      pushSecrets: [{ name: harbor-push }]     # dockerconfigjson con permiso de subida
```
`status.outputRef` dice dónde ha ido (`oras pull <ref>` baja los ficheros). La respuesta de una petición `export` es a
su vez una petición `update`, así que otro `DXXMLAccess`, en el entorno de destino, la aplica:
```yaml
spec:
  connectionRef: { name: dx }                  # el DX del entorno de destino
  source: { image: { ref: harbor.example.com/dx-exports/url-mappings:dev-20261117-214132, pullSecrets: [{ name: harbor-pull }] } }
  files: [request.xml]                         # results.json es el resumen, no una petición
  dryRun: true                                 # primero comprobar; después quitarlo
```

**Ejecutar los ficheros de una carpeta de un repositorio Git**, por orden de nombre (`01-…xml`, `02-…xml`), en un
portal virtual:
```yaml
spec:
  connectionRef: { name: dx }
  virtualPortal: { context: europesip }           # /wps/config/europesip
  source:
    url: { url: https://gitlab.example.com/acme/portal/-/archive/main/portal-main.tar.gz, path: portal-main/xmlaccess }
  variables: { SITE_HOST: www.acme.example }       # ${SITE_HOST} en el XML (nunca secretos)
  cleanup: { file: 99-cleanup.xml }                # opcional: se ejecuta al borrar el recurso
```
Para un repositorio privado usa la URL de un artefacto de CI, una URL prefirmada de S3, o sube la carpeta a Harbor con
ORAS (`source.image`).

**Reglas a recordar**
- Se ejecuta **una vez por cambio** (spec, revisión de la fuente o la anotación `dx.europesip.com/reconcile`). Para
  repetirlo sin cambios: `kubectl annotate dxxml <nombre> dx.europesip.com/reconcile="$(date +%s)" --overwrite`.
- Primero se comprueban todos los ficheros: con uno roto **no se envía nada**. `dryRun: true` solo comprueba y lista.
- La primera petición fallida para la ejecución (`continueOnError: true` para seguir). `status.results` trae el
  mensaje de DX.
- No detecta deriva y no deshace nada al borrarlo (salvo `cleanup`). Escribe peticiones idempotentes
  (`action="update"`).
- Las respuestas de DX solo se guardan con `output.configMap` (como mucho 900 KiB: si son mayores, la ejecución falla con `OutputTooLarge` y nunca se recortan) o `output.oci` (un registro, sin límite).

```bash
kubectl get dxxml -n <ns>
kubectl get dxxml <nombre> -o jsonpath='{range .status.results[*]}{.file}{"\t"}{.result}{"\t"}{.message}{"\n"}{end}'
kubectl get events -n <ns> --field-selector involvedObject.name=<nombre>
```

## Limitaciones

- Los **portlets propios** (JSR 168/286) del origen no funcionan en DX Compose sin el *Java Transition Module* de HCL;
  la página se importa, el portlet no funciona. Los portlets de HCL sí.
- DX Compose no tiene ConfigEngine ni PAA; las reglas de personalización no tienen línea de comandos.
- Mira [Restricciones y limitaciones](limitations.es.md).
