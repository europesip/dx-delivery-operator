# DX Core (DX 9.5 tradicional sobre WebSphere)

[English](dx-core.md) · **Español**

Desde la issue [#44](https://github.com/europesip/dx-delivery-operator/issues/44) el operador también trabaja con
**DX Core**, el servidor tradicional de HCL DX 9.5 sobre WebSphere Application Server: el pod `core` del chart de HCL
(`applications.core`) y un DX tradicional instalado en una máquina virtual. DX Compose (el Web Engine) sigue siendo lo
predeterminado.

## Qué cambia

| | DX Compose (Web Engine) | DX Core |
|---|---|---|
| `DXConnection.spec.runtime` | `compose` (predeterminado) | `core` |
| Raíz de contexto | `/wps` | `/wps` en el chart de HCL; una máquina virtual puede tenerla **vacía**: poner `contentHandlerPath: /mycontenthandler` (XMLAccess va entonces a `/config` y el login a `/j_security_check`) |
| Login | Ring API, si no el formulario del portal | el formulario del portal (una máquina virtual no tiene Ring API); XMLAccess y WebDAV con autenticación básica |
| Exportar / importar librerías WCM | módulo de datos de WCM + `dxFileTransfer` | **DXConnect** (el servlet del perfil del Config Wizard que usa DXClient): en Core no existe `dxFileTransfer` (404) |
| `DXWCMLibraryImport` `mode: ImportCopy` | sí | no: DXConnect ejecuta `import-wcm-data`, que conserva los IDs |
| XMLAccess (páginas, temas, portales virtuales) | `/wps/config` | igual, en `<raíz de contexto>/config` |
| Temas (WebDAV) | `…/mycontenthandler/dav/fs-type1/themes/` | igual |

## DXConnection para DX Core

```yaml
apiVersion: dx.europesip.com/v1alpha1
kind: DXConnection
metadata: {name: dx}
spec:
  url: https://dx-core.example.com
  internalURL: http://dx-deployment-core.<namespace>.svc:10039   # el pod core, sin HAProxy
  runtime: core
  portalCredentialsSecret: {name: dx-core-wps}            # wpsadmin
  dxConnect:                                         # opcional: valores por defecto abajo
    url: https://dx-core.example.com                 # por defecto spec.url (HAProxy enruta /hcl/dxconnect)
    cwCredentialsSecret: {name: dx-core-configwizard} # administrador del Config Wizard (por defecto: portalCredentialsSecret)
    wasCredentialsSecret: {name: dx-core-was}        # administrador de WebSphere (por defecto: portalCredentialsSecret)
    profileName: wp_profile
```

Para un DX tradicional en máquina virtual con la raíz de contexto vacía:

```yaml
spec:
  url: https://dx-vm.example.com:10042
  runtime: core
  contentHandlerPath: /mycontenthandler
  dxConnect: {url: "https://dx-vm.example.com:10202"}   # solo si el servidor del Config Wizard (cw_profile) está arrancado
```

## Librerías WCM por DXConnect

Son las llamadas de DXClient 239 `wcm-library-export` / `wcm-library-import`
(`/hcl/dxconnect/processHandler?requestMethod=exportWCMLibrary|download|upload|importWCMLibrary`, con las cabeceras
`Authorization` (Config Wizard), `DXAuth` (DX) y `DXWASAuth` (WebSphere)). DXConnect ejecuta ConfigEngine
`export-wcm-data` / `import-wcm-data` en el servidor.

- **Exportar**: DXConnect devuelve un tar (con nombre `.tar.gz`, pero sin comprimir) con una carpeta por librería
  (`WCM_Library-<hora>/<librería>/<exportación JCR>`). El operador las junta en su zip habitual: una exportación JCR en
  la raíz, igual que el módulo de datos de DX Compose. Así lo exportado de DX Core se importa en DX Compose y al revés.
- **Importar**: el operador empaqueta cada bloque como lo espera DXConnect (el mismo tar, una carpeta por librería). Un
  zip plano lo rechaza DXConnect (`…/wcm/ilwwcm/system/import (No such file or directory)`, medido).
- **En una máquina virtual** DXConnect corre en el servidor del Config Wizard (`cw_profile`, puerto 10202). Si está
  parado, contesta el portal con una página HTML y el operador lo dice; entonces se exportan las librerías con
  ConfigEngine en el servidor.

## Temas y sitios entre portales (DXExport `parts.themes`)

`DXExport` exporta también temas: por cada nombre único, su **registro con sus propios skins** (XMLAccess,
`themes/theme-<nombre>.xml`) y su **carpeta WebDAV** (`themes/theme-<nombre>-webdav.zip`). Los skins de HCL (`ibm.*`,
`hcl.*`, `wp.*`) no se incluyen: están en todos los DX. El XML se limpia para importarlo (medido CF230 → CF239): se
quitan los `localedata` con idioma vacío (NullPointerException al importar) y el `<skin action="locate">` repetido
(EJPXA0140E, objectid duplicado).

Los portlets clonados (`<portlet>.$cloned.<id>`, por ejemplo un Web Content Viewer copiado con su propia configuración)
no existen en otro portal: la exportación mete ahora sus definiciones en `xmlaccess/01-pages.xml`, y las páginas se
importan.

Importar, en este orden:

1. `DXTheme` con `registration: FilesOnly`, `base.copyFiles: false` y el paquete como origen: solo los ficheros WebDAV
   (busca `themes/theme-<nombre>-webdav.zip` en el paquete y lo descomprime; los ficheros que el destino rechaza con
   HTTP 400 se avisan con un evento).
2. `DXXMLAccess` con `files: [themes/theme-<nombre>.xml, xmlaccess/01-pages.xml]`, `objectIDs: Keep`.

Ver el ejemplo `samples/15-dx-core`.

## Medido (22-nov-2026)

Desde un DX 9.5 CF230 tradicional en máquina virtual (solo lectura), a DX Core CF239 en Kubernetes (Oracle 21c XE) y a
DX Compose CF239: el sitio de EuropeSIP (41 páginas, tema con 467 ficheros WebDAV, un Web Content Viewer clonado) se
importó en los dos. WCM: Portal Site de DX Core a un portal virtual nuevo de DX Compose (dxFileTransfer), y Web
Resources v70 de DX Compose a un portal virtual nuevo de DX Core (DXConnect).
