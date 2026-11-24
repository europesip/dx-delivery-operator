# Why an operator

**English** · [Español](why.es.md)

On HCL DX Compose, custom applications are **Script Applications** (custom Java code goes through the Java Transition Module; the operator loads it onto the official image
with [DXJavaExtension](java-extensions.md)). HCL deploys them with **DXClient**, a command line tool: a push from a
workstation or a CI job. That works for one application, but a real one is more than a push:

- its **backend**: microservices and a database, which must be ready before the application is useful;
- its **pages** in DX, with the application on them (XMLAccess);
- its **images and files** in the DAM, and their URLs in the application;
- different values per environment (URLs, flags), without building it again.

EUROPESIP first wrapped the push in a Helm chart ([hcl-dx-scriptapp-helm](https://github.com/europesip/hcl-dx-scriptapp-helm):
a Job with DXClient as a Helm hook), then built [ÓRBITA](https://github.com/europesip/orbita), an intranet with
six Script Applications, a REST API, PostgreSQL, pages, a theme and DAM images, on the same model (an
after-push script). It works, but it showed the limits of an imperative step inside a declarative tool:

- **Nothing in Kubernetes knows the state in DX.** The Job runs once and is gone. If someone deletes or edits
  the application in WCM, or DX is reinstalled, nothing notices and nothing fixes it.
- **Every change is a full re-run.** Helm (and ArgoCD, which turns the hooks into sync hooks) runs the Job on
  every upgrade or sync, changed or not.
- **The result is only in a log**: the content ID, the pages, the DAM URLs.
- **Deleting is manual**: `helm uninstall` leaves the application, the pages and the files in DX.
- **Every new application needs its own chart and scripts** for pages, DAM and database.

The operator turns those steps into **resources with a desired state and a status**, reconciled continuously.

## Compared with the Helm chart and its Job

| | Helm chart with a DXClient Job | Operator |
|---|---|---|
| Push the application | On every install/upgrade, always | **Only when something changed** (hash of files and settings) |
| Know if it is in DX | No (log of a finished Job) | **Yes**: status, conditions, events, `kubectl get` |
| Deleted or edited in DX, DX reinstalled | Unnoticed | **Detected and restored** on the next check |
| Pages, DAM, database | Custom scripts per application | **Declarative resources**, reusable |
| Same build in every environment | Values baked into the build or the chart | `substitutions` per environment |
| Order (backend first) | Hook weights | `dependsOn`, waits and retries |
| Remove from DX on uninstall | Manual | `deletionPolicy: Delete` |
| GitOps (ArgoCD) | Hooks re-run on every sync, no health | Plain resources with **health** (see below) |
| Concurrent pushes to one DX | Jobs can collide | **Serialised** by the operator |

The Helm chart is still a good choice for a single application installed once; the operator pays off with
several applications, several environments, GitOps, or when DX must be kept as declared.
