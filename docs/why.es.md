# Por qué un operador

[English](why.md) · **Español**

En HCL DX Compose las aplicaciones a medida son **Script Applications** (el código Java a medida pasa por el Java Transition Module;
el operador lo carga sobre la imagen oficial con [DXJavaExtension](java-extensions.es.md)). HCL las despliega con **DXClient**, una herramienta de línea de
comandos: un push desde un puesto o un job de CI. Basta para una aplicación sencilla, pero una aplicación real es
más que un push:

- su **backend**: microservicios y una base de datos, que deben estar listos antes;
- sus **páginas** en DX, con la aplicación puesta (XMLAccess);
- sus **imágenes y ficheros** en el DAM, y sus URL dentro de la aplicación;
- valores distintos por entorno (URL, opciones), sin volver a compilarla.

EUROPESIP primero envolvió el push en un chart de Helm ([hcl-dx-scriptapp-helm](https://github.com/europesip/hcl-dx-scriptapp-helm):
un Job con DXClient como hook de Helm) y después construyó [ÓRBITA](https://github.com/europesip/orbita), una
intranet con seis Script Applications, una API REST, PostgreSQL, páginas, un tema e imágenes del DAM, con el
mismo modelo (un script posterior al push). Funciona, pero dejó claros los límites de un paso imperativo dentro
de una herramienta declarativa:

- **Nada en Kubernetes conoce el estado en DX.** El Job se ejecuta una vez y desaparece. Si alguien borra o
  edita la aplicación en WCM, o se reinstala DX, nadie se entera y nadie lo corrige.
- **Cada cambio es una ejecución completa.** Helm (y ArgoCD, que convierte los hooks en hooks de sync) ejecuta
  el Job en cada upgrade o sync, haya cambios o no.
- **El resultado solo está en un log**: el content ID, las páginas, las URL del DAM.
- **Borrar es manual**: `helm uninstall` deja en DX la aplicación, las páginas y los ficheros.
- **Cada aplicación nueva necesita su chart y sus scripts** para páginas, DAM y base de datos.

El operador convierte esos pasos en **recursos con un estado deseado y un status**, reconciliados continuamente.

## Frente al chart de Helm y su Job

| | Chart de Helm con un Job de DXClient | Operador |
|---|---|---|
| Subir la aplicación | En cada install/upgrade, siempre | **Solo si algo cambió** (hash de ficheros y ajustes) |
| Saber si está en DX | No (log de un Job terminado) | **Sí**: status, condiciones, eventos, `kubectl get` |
| Borrada o editada en DX, DX reinstalado | No se detecta | **Se detecta y se restaura** en la siguiente revisión |
| Páginas, DAM, base de datos | Scripts a medida por aplicación | **Recursos declarativos**, reutilizables |
| La misma compilación en todos los entornos | Valores dentro de la compilación o del chart | `substitutions` por entorno |
| Orden (primero el backend) | Pesos de los hooks | `dependsOn`, esperas y reintentos |
| Quitarla de DX al desinstalar | A mano | `deletionPolicy: Delete` |
| GitOps (ArgoCD) | Hooks en cada sync, sin salud | Recursos normales **con salud** (ver abajo) |
| Varios push a la vez contra un DX | Los Jobs pueden chocar | **Serializados** por el operador |

El chart de Helm sigue siendo buena opción para una aplicación que se instala una vez; el operador compensa con
varias aplicaciones, varios entornos, GitOps, o cuando hay que mantener DX tal como se declara.
