# cluster-init Specification

## Purpose
`shelf init cluster` installs the shelf platform into the Kubernetes cluster of a chosen
kubecontext: the Flux Operator, a FluxInstance that syncs the platform OCI artifact (Traefik and
the ResourceSet `apps`), the namespace `shelf-system` and the cluster settings. It is the cluster
layer of the installer, works against any kubecontext without host assumptions, is idempotent,
and refuses changes that would silently move apps to different host names.

## Requirements

### Requirement: Target cluster selection
The system SHALL install into the cluster of the current kubecontext of `$KUBECONFIG` (or
`~/.kube/config`), and SHALL let `--kubeconfig` and `--context` pick the target explicitly,
without relying on Colima, macOS or any other property of the machine hosting the cluster.

#### Scenario: Explicit context
- **WHEN** `shelf init cluster --context other` is run
- **THEN** the installation targets the API server of the context `other`

#### Scenario: Unknown context
- **WHEN** `--context` names a context that does not exist in the kubeconfig
- **THEN** the command fails before anything is applied

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

### Requirement: Platform and chart artifacts
The system SHALL take the platform artifact from `--platform` and the shelf-app chart from
`--chart`, each as `oci://<registry>/<repository>:<tag>`; without the flags a release build MUST
default to `oci://ghcr.io/tweinmann/shelf/platform:<version>` and
`oci://ghcr.io/tweinmann/shelf/charts/shelf-app:<version without leading v>`.

#### Scenario: Release defaults
- **WHEN** a release build `v0.5.0` runs without `--platform` and `--chart`
- **THEN** it uses `oci://ghcr.io/tweinmann/shelf/platform:v0.5.0` and `oci://ghcr.io/tweinmann/shelf/charts/shelf-app:0.5.0`

#### Scenario: Development build without platform
- **WHEN** a development build runs without `--platform`
- **THEN** the command fails with a message to pass `--platform`, before anything is applied

#### Scenario: Malformed reference
- **WHEN** `--platform` or `--chart` does not start with `oci://` or lacks registry or tag
- **THEN** the command fails naming the flag, before anything is applied

### Requirement: Flux Operator installation
The system SHALL apply the embedded Flux Operator manifest (pinned version) with server-side
apply under the field manager `shelf`, applying namespaces and CRDs first and waiting until they
are established, then the remaining objects, and SHALL wait until the operator Deployment is
ready.

#### Scenario: Fresh cluster
- **WHEN** the command runs against a cluster without Flux
- **THEN** the Flux Operator objects are created and the output reports how many were created, configured or unchanged

### Requirement: FluxInstance syncing the platform
The system SHALL apply a FluxInstance `flux` in `flux-system` with the pinned Flux distribution
from `ghcr.io/fluxcd`, the components source-, kustomize-, helm- and notification-controller,
network policies enabled, and a sync of the platform artifact as an OCIRepository at path `./`
every `1m`; the sync Kustomization MUST wait for everything it applied and substitute variables
from the ConfigMap `flux-system/shelf-config`.

#### Scenario: Insecure dev registry
- **WHEN** `--insecure-registry` is passed
- **THEN** the platform OCIRepository is patched to `insecure: true` and `SHELF_INSECURE_REGISTRY` is `true`

#### Scenario: Flux ready
- **WHEN** the FluxInstance has been applied
- **THEN** the command waits until it reports Ready for its current generation

### Requirement: Platform contents
The platform artifact SHALL contain Traefik (Helm chart from `oci://ghcr.io/traefik/helm/traefik`
in namespace `traefik`, Service of type ClusterIP, ingress class `traefik` that is not the
default, Kubernetes Ingress and CRD providers enabled) and, in `shelf-system`, the ResourceSet
`apps`, which generates every app from the providers labelled `shelf.dev/app`, and the
ResourceSet `previews`, which generates every preview from those labelled `shelf.dev/previews`.

#### Scenario: Platform applied
- **WHEN** the installation completes
- **THEN** Traefik runs in `traefik` reachable at `traefik.traefik.svc.cluster.local:80` and the ResourceSets `shelf-system/apps` and `shelf-system/previews` exist

### Requirement: System namespace and cluster settings
The system SHALL apply the namespace `shelf-system` (labelled
`app.kubernetes.io/managed-by: shelf`) and the ConfigMap `flux-system/shelf-config` with the keys
`SHELF_HOST_SUFFIX`, `SHELF_CHART_URL`, `SHELF_CHART_TAG` and `SHELF_INSECURE_REGISTRY`; the
cluster has no domain setting.

#### Scenario: Settings written
- **WHEN** `shelf init cluster --host-suffix -dev` completes
- **THEN** `shelf-config` holds `SHELF_HOST_SUFFIX=-dev` and the chart URL and tag that were chosen

#### Scenario: No cluster domain flag
- **WHEN** `--domain` is passed
- **THEN** the command fails with "unknown flag: --domain"

### Requirement: Anonymous registry secret
The system SHALL write the Secret `shelf-system/registry-anonymous`, a docker config without any
login, which apps without a registry connection pull with.

#### Scenario: Written on every run
- **WHEN** the installation runs
- **THEN** `shelf-system/registry-anonymous` exists and holds no credentials

### Requirement: Host suffix
The system SHALL accept `--host-suffix` only when `a<suffix>` is a valid DNS label, and SHALL
announce the host pattern `<app><host-suffix>.<domain>` for apps with a domain and
`<app>.shelf.internal` for apps without one, which carries no suffix.

#### Scenario: Valid suffix
- **WHEN** `--host-suffix -dev` is passed
- **THEN** the output shows `hosts     <app>-dev.<domain>, or <app>.shelf.internal without a domain`

#### Scenario: Invalid suffix
- **WHEN** `--host-suffix -a_b` is passed
- **THEN** the command fails naming `--host-suffix`, before anything is applied

### Requirement: Refusal to move app host names
The system MUST refuse, before applying anything, an installation on an already installed
cluster whose new host suffix gives existing apps with a domain a different host name, unless
`--move-hosts` is passed; `--yes` does not override this. Apps without a domain never count as
moving, and the first installation never counts as a move.

#### Scenario: Suffix change with apps
- **WHEN** a cluster serving `<app>-dev.<domain>` with the app `greeter` on a domain is initialised with no suffix and without `--move-hosts`
- **THEN** the command fails with a message that it serves `<app>-dev.<domain>`, changing it to `<app>.<domain>` moves app `greeter`, and to pass `--move-hosts`
- **AND** nothing is installed

#### Scenario: Stranded records named
- **WHEN** a moving app is exposed through a tunnel under a public domain
- **THEN** the refusal adds that the records under the old names stay behind

#### Scenario: Move allowed
- **WHEN** the same change is made with `--move-hosts`
- **THEN** the installation proceeds and warns with the old and new host names of each exposed app whose DNS record stays behind in Cloudflare

### Requirement: Former cluster domain kept on apps
The system SHALL read a former cluster domain (`SHELF_DOMAIN` in `shelf-config`) and, if it is a
public domain, give it to every app without a domain of its own as that app's domain, reporting
each such app; a reserved domain (such as `.local`, `.internal`, `.test`, `.localhost`,
`.invalid`, `.example`, `.home.arpa`) MUST NOT be kept, and those apps answer at
`<app>.shelf.internal`. The key is no longer written.

#### Scenario: Public former domain
- **WHEN** `shelf-config` holds `SHELF_DOMAIN=example.com` and the app `greeter` has no domain
- **THEN** `greeter` gets `example.com` as its own domain and the output says it keeps the cluster's former domain

#### Scenario: Reserved former domain
- **WHEN** `shelf-config` holds `SHELF_DOMAIN=dev.local`
- **THEN** no app is given that domain and the apps without one answer at `<app>.shelf.internal`

### Requirement: Migration of apps registered earlier
The system SHALL give every app provider that lacks any of the inputs `domain`, `tunnel`,
`quick`, `registry`, `cloudflare` those inputs, with no tunnel and `quick` off, so that an
upgrade never puts an app on the internet; a former shared registry Secret `shelf-system/registry`
MUST become the registry connection `ghcr` assigned to those apps and then be deleted, and the
former shared tunnel (`expose` provider and `tunnel` Secret) MUST be removed with a warning to
expose each app again.

#### Scenario: Shared login migrated
- **WHEN** the cluster has the Secret `shelf-system/registry` with a ghcr.io login and apps registered before connections existed
- **THEN** `shelf-system/connection-registry-ghcr` is written, those apps get `registry: ghcr`, and `shelf-system/registry` is deleted

#### Scenario: Shared tunnel switched off
- **WHEN** the cluster has the provider `shelf-system/expose`
- **THEN** it is deleted and a warning says every app now needs its own tunnel and that the old tunnel and records stay in Cloudflare

### Requirement: Waiting for the applied platform
The system SHALL wait until the platform OCIRepository points at the chosen artifact, request a
reconciliation and wait until it has fetched it, then request a reconciliation of the sync
Kustomization and wait until it is Ready with exactly that artifact revision, all within
`--timeout` (default 5 minutes).

#### Scenario: Moving tag re-pushed
- **WHEN** the same tag was pushed again with new content and the command is rerun
- **THEN** it reports success only after the new revision has been applied

#### Scenario: Private platform artifact
- **WHEN** the registry refuses the anonymous read of the platform artifact
- **THEN** the command fails at once, saying the platform artifact is read without a login and has to be public

### Requirement: Idempotency
The system SHALL be safe to run repeatedly: a second run with the same flags MUST change nothing
and report the applied objects as unchanged, and a run with changed flags updates only what
changed.

#### Scenario: Init twice
- **WHEN** `shelf init cluster` is run twice in a row with the same flags
- **THEN** the second run succeeds and reports its objects as unchanged

### Requirement: CLI only
The system SHALL offer the cluster installation as the CLI command `shelf init cluster`, reporting
progress as text lines; the admin UI does not offer it.

#### Scenario: Progress output
- **WHEN** the installation runs
- **THEN** the output reports the operator, Flux and the platform as steps with the time each took, and the applied platform revision
