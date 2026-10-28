# Spec Delta

## MODIFIED Requirements

### Requirement: Platform contents
The platform artifact SHALL contain Traefik (Helm chart from `oci://ghcr.io/traefik/helm/traefik`
in namespace `traefik`, Service of type ClusterIP, ingress class `traefik` that is not the
default, Kubernetes Ingress and CRD providers enabled) and, in `shelf-system`, the ResourceSet
`apps`, which generates every app from the providers labelled `shelf.dev/app`, and the
ResourceSet `previews`, which generates every preview from those labelled `shelf.dev/previews`.

#### Scenario: Platform applied
- **WHEN** the installation completes
- **THEN** Traefik runs in `traefik` reachable at `traefik.traefik.svc.cluster.local:80` and the ResourceSets `shelf-system/apps` and `shelf-system/previews` exist
