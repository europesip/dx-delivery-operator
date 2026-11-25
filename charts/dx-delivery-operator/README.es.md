<!-- SPDX-License-Identifier: MIT -->
<!-- Copyright (c) 2026 EUROPESIP -->
# DX Delivery Operator, by EUROPESIP

[English](https://github.com/europesip/dx-delivery-operator/blob/main/charts/dx-delivery-operator/README.md) · **Español**

Un operador de Kubernetes que entrega aplicaciones y contenido de HCL DX desde Git, de forma declarativa, y los
mantiene en DX tal y como están declarados (corrección de cambios): Script Applications en WCM, sus páginas y ficheros
del DAM, la base de datos de su backend, librerías WCM, temas, páginas, XMLAccess y paquetes completos de portal que se
mueven entre portales como artefactos OCI, módulos de mantenimiento de WCM y extensiones Java (portlets, librerías
compartidas) sobre la imagen oficial del Web Engine de DX Compose. Funciona con HCL DX Compose y DX Core en Kubernetes
(probado en k3s; diseñado para OpenShift, todavía sin probar allí) y con DX tradicional en máquina virtual.

## Requisitos

- Kubernetes 1.28+ y Helm 3.8+.
- La imagen del operador incluye **HCL DXClient**, software de HCL con su propia licencia: léela y acéptala con
  `dxclient.acceptLicense=true`; si no, el chart no se instala.
- Un usuario de DX (editor de la Script Application Library; un administrador de DX para páginas, DAM y XMLAccess).

## Instalación

```bash
helm upgrade --install dxop oci://ghcr.io/europesip/charts/dx-delivery-operator \
  -n dxop-system --create-namespace --set dxclient.acceptLicense=true
```

Después, en el namespace de tus aplicaciones: un Secret con el usuario de DX, un `DXConnection` y tus recursos
(`DXScriptApplication`, `DXPage`…). Paso a paso y ejemplos: [documentación](https://github.com/europesip/dx-delivery-operator/blob/main/README.es.md).

## Valores principales

| Valor | Por defecto | Qué hace |
|---|---|---|
| `dxclient.acceptLicense` | `false` | Aceptar la licencia de HCL DXClient (obligatorio) |
| `watchNamespaces` | `[]` | Namespaces que vigila (vacío: todos) |
| `javaExtensions.enabled` | `false` | Extensiones Java (`DXJavaExtension`) con el webhook de admisión |
| `javaExtensions.certificate.provider` | `auto` | Certificado del webhook: cert-manager o el service CA de OpenShift |
| `javaExtensions.startupHook.enabled` | `true` | Segundo modo de entrega por la carpeta de arranque de cliente de HCL |
| `podSecurityContext` / `containerSecurityContext` | neutros | Sin UID fijo: funciona en cualquier Kubernetes y en OpenShift |
| `resources`, `workDir.sizeLimit` | ver values.yaml | Memoria/CPU y tamaño de la carpeta de trabajo |

Todos los valores: [values.yaml](https://github.com/europesip/dx-delivery-operator/blob/main/charts/dx-delivery-operator/values.yaml).

## Licencia

MIT. Quien redistribuya debe conservar el aviso de copyright y la licencia. El DX Delivery Operator no es un producto
de HCL; HCL, HCL DX y HCL DX Compose son marcas de HCL Technologies.
