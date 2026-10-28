# Spec Delta

## MODIFIED Requirements

### Requirement: Confirmation before changing the cluster
The system SHALL show the target context, its API server, the platform artifact, the chart and
the host name pattern, and SHALL ask `Proceed? [y/N]` before applying anything; only `y` or
`yes` (case-insensitive) proceeds, and `--yes`/`-y` skips the question.

#### Scenario: Confirmed
- **WHEN** the user answers `y`
- **THEN** the installation runs

#### Scenario: Declined or no answer
- **WHEN** the user answers anything other than `y`/`yes`, or input ends without an answer
- **THEN** the command exits non-zero with "aborted; nothing was changed" and the cluster is untouched

#### Scenario: Non-interactive
- **WHEN** `--yes` is passed
- **THEN** no question is asked and the installation runs

#### Scenario: Apps without a verified artifact
- **WHEN** the platform is upgraded to one that verifies, and apps run artifacts that are not verified
- **THEN** the confirmation lists those apps and says that each needs one build with the current workflow before it gets new deploys, and that their running pods keep running

### Requirement: FluxInstance syncing the platform
The system SHALL apply a FluxInstance `flux` in `flux-system` with the pinned Flux distribution
from `ghcr.io/fluxcd`, the components source-, kustomize-, helm- and notification-controller,
network policies enabled, and a sync of the platform artifact as an OCIRepository at path `./`
every `1m`, verified against the cluster's signer; the sync Kustomization MUST wait for
everything it applied and substitute variables from `flux-system/shelf-config`.

#### Scenario: Insecure dev registry
- **WHEN** `--insecure-registry` is passed
- **THEN** the platform OCIRepository is patched to `insecure: true` and `SHELF_INSECURE_REGISTRY` is `true`

#### Scenario: Flux ready
- **WHEN** the FluxInstance has been applied
- **THEN** the command waits until it reports Ready for its current generation

#### Scenario: Unsigned platform
- **WHEN** `shelf init cluster --platform` names a platform artifact without a valid signature
- **THEN** the platform OCIRepository reports a failed verification, and the command fails while waiting for the platform with that message

### Requirement: Platform contents
The platform artifact SHALL contain Traefik (Helm chart from `oci://ghcr.io/traefik/helm/traefik`
in namespace `traefik`, ClusterIP Service, ingress class `traefik` that is not the default,
Ingress and CRD providers enabled), the Sigstore policy-controller in `cosign-system`, and, in
`shelf-system`, the ResourceSet `apps`, which generates every app from the providers labelled
`shelf.dev/app`, and the ResourceSet `previews`, which generates every preview from those
labelled `shelf.dev/previews`.

#### Scenario: Platform applied
- **WHEN** the installation completes
- **THEN** Traefik runs in `traefik` reachable at `traefik.traefik.svc.cluster.local:80` the ResourceSets `shelf-system/apps` and `shelf-system/previews` exist, and the policy-controller's webhook is ready

### Requirement: System namespace and cluster settings
The system SHALL apply the namespace `shelf-system` (labelled
`app.kubernetes.io/managed-by: shelf`) and the ConfigMap `flux-system/shelf-config` with the keys
`SHELF_HOST_SUFFIX`, `SHELF_CHART_URL`, `SHELF_CHART_TAG`, `SHELF_INSECURE_REGISTRY` and the
signer keys `SHELF_SIGNER_MODE`, `SHELF_SIGNER_ISSUER` and `SHELF_SIGNER_SUBJECT`; the cluster has
no domain setting.

#### Scenario: Settings written
- **WHEN** `shelf init cluster --host-suffix -dev` completes
- **THEN** `shelf-config` holds `SHELF_HOST_SUFFIX=-dev` and the chart URL and tag that were chosen

#### Scenario: No cluster domain flag
- **WHEN** `--domain` is passed
- **THEN** the command fails with "unknown flag: --domain"
