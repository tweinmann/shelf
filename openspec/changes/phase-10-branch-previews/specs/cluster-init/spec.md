# Spec Delta

## MODIFIED Requirements

### Requirement: Platform contents
The platform artifact SHALL contain Traefik (Helm chart from `oci://ghcr.io/traefik/helm/traefik`
in namespace `traefik`, Service of type ClusterIP, ingress class `traefik` that is not the
default, Kubernetes Ingress and CRD providers enabled) the ResourceSet `apps` in
`shelf-system`, which generates every app from the ResourceSetInputProviders labelled
`shelf.dev/app`, and the ResourceSet `previews` in `shelf-system`, which generates every preview
from the providers labelled `shelf.dev/previews`.

#### Scenario: Platform applied
- **WHEN** the installation completes
- **THEN** Traefik runs in `traefik` reachable at `traefik.traefik.svc.cluster.local:80` and the ResourceSets `shelf-system/apps` and `shelf-system/previews` exist
