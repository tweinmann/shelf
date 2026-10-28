# app-delivery Specification

## Purpose
App delivery is the path from a tenant's CI to a running app: the tenant workflow builds images
and pushes a deploy artifact to GHCR, and shelf registers an app that Flux deploys from that
artifact and keeps updated. The registry is the only interface between build and platform. This
capability covers the deploy artifact contract, the tenant workflows, and the app lifecycle
commands (`shelf app add`, `rm`, `redeploy`, `tags`, `secrets`, `status`) with their per-app
objects in `shelf-system`.

## Requirements

### Requirement: Deploy artifact format
The deploy artifact SHALL be an OCI artifact as pushed by `flux push artifact`, with a layer of
media type `application/vnd.cncf.flux.content.v1.tar+gzip` holding exactly one ConfigMap named
`shelf-values` whose key `app.yaml` carries the resolved app.yaml.

#### Scenario: Valid artifact is read
- **WHEN** shelf reads an artifact whose content layer holds one ConfigMap `shelf-values` with a valid `app.yaml` of apiVersion `shelf.dev/v1alpha1`
- **THEN** shelf accepts it and uses the app it describes, including its declared secret names

#### Scenario: Artifact without a content layer
- **WHEN** the referenced OCI image has no layer of the Flux content media type
- **THEN** shelf refuses it with a message that it is not a deploy artifact

#### Scenario: Zero or several app ConfigMaps
- **WHEN** the tarball holds no ConfigMap with the key `app.yaml`, or more than one
- **THEN** shelf refuses the artifact, reporting how many it found

### Requirement: Artifact content validation
Before registering an app, shelf MUST refuse a deploy artifact whose app.yaml has an unknown
apiVersion, fails validation, or comes in a ConfigMap not named `shelf-values`.

#### Scenario: Unsupported apiVersion
- **WHEN** the app.yaml in the artifact has an apiVersion other than `shelf.dev/v1alpha1`
- **THEN** shelf refuses it, naming the apiVersion it understands

#### Scenario: Artifact rendered before v0.5.0
- **WHEN** the ConfigMap carrying `app.yaml` has a name other than `shelf-values`
- **THEN** shelf refuses it and says the artifact must be pushed again with a newer shelf

#### Scenario: Invalid app.yaml
- **WHEN** the app.yaml in the artifact has validation errors
- **THEN** shelf refuses it and lists the error findings

### Requirement: Tampered artifact refusal in the cluster
The platform SHALL apply a deploy artifact only with an app-namespace service account that may
manage nothing but ConfigMaps in the app's namespace, so an artifact holding any other object
fails instead of being applied.

#### Scenario: Artifact with an extra cluster object
- **WHEN** a pushed artifact contains, besides the ConfigMap, an object such as a ClusterRoleBinding
- **THEN** the app's deploy Kustomization fails with a forbidden error
- **AND** the extra object is not created

### Requirement: Resolved values in the artifact
The app.yaml in a deploy artifact SHALL be the output of `shelf render`: images pinned by digest,
host and port references substituted, and `${secrets.*}` references left unresolved for the
chart to resolve in the cluster.

#### Scenario: Built component image passed in
- **WHEN** CI renders with `--image <component>=<reference>` for a component with `build:`
- **THEN** the rendered app.yaml uses that image pinned to its digest

#### Scenario: Secrets stay unresolved
- **WHEN** an app.yaml references a secret
- **THEN** the rendered artifact contains the reference, never a secret value

### Requirement: Build plan for tenant CI
`shelf build-plan <app.yaml>` SHALL print JSON `{package, builds}`, where `package` is app.yaml's
`name` and `builds` lists each component with a `build` directory and its context relative to
the working directory; with `--branch <name>` it SHALL also print `tag`, the branch tag of that
branch.

#### Scenario: Component with a build directory
- **WHEN** app.yaml declares a component with `build: ./web` and that directory exists
- **THEN** `builds` contains that component with the context path of `./web` relative to the working directory

#### Scenario: Missing build directory
- **WHEN** a component's build directory does not exist
- **THEN** the command fails naming the component and the directory

#### Scenario: Branch tag
- **WHEN** the workflow runs `shelf build-plan app.yaml --branch feature/Login`
- **THEN** the JSON holds `"tag": "branch-feature-login"`

### Requirement: Reusable tenant workflow
The reusable workflow `.github/workflows/build.yml` SHALL validate the app.yaml, build every
`build:` component for `linux/arm64` as `ghcr.io/<owner>/<package>/<component>`, render the
app.yaml, and push the deploy artifact `ghcr.io/<owner>/<package>` tagged `sha-<shortsha>` and
`main` for the default branch or `branch-<slug>` for another branch, annotated with
`dev.shelf.package=<package>` and the commit's source and revision. When a branch is deleted, it
SHALL delete that branch's artifact.

#### Scenario: Push to the default branch
- **WHEN** the workflow runs for a push to the repository's default branch
- **THEN** it pushes the images and the deploy artifact, and outputs `oci://ghcr.io/<owner>/<package>:main`
- **AND** its job summary shows a `shelf app add` command for that artifact

#### Scenario: Push to another branch
- **WHEN** the workflow runs for a push to the branch `feature/login`
- **THEN** it pushes the images and the deploy artifact tagged `sha-<shortsha>` and `branch-feature-login`, and never `main`

#### Scenario: Pull request or other branch
- **WHEN** the workflow runs for a pull request, from a fork or not
- **THEN** it builds the images but pushes neither images nor a deploy artifact
- **AND** a push to another branch is no longer such a case: it publishes, as in "Push to another branch"

#### Scenario: Branch deleted
- **WHEN** the workflow runs for the deletion of the branch `feature/login`
- **THEN** it deletes the package version that carries `branch-feature-login` from `ghcr.io/<owner>/<package>`, and nothing else

#### Scenario: Pinned shelf release
- **WHEN** the caller sets the input `shelf-version`
- **THEN** the workflow installs that release's linux/arm64 binary after checking its checksum, and otherwise the latest release

### Requirement: Build cache in the tenant workflow
The reusable workflow SHALL reuse image layers between runs through a build cache in the
registry, `ghcr.io/<owner>/<package>/<component>:buildcache`, which only runs that publish write.

#### Scenario: Build cache
- **WHEN** a push changes only the source of one component
- **THEN** the build of every component reads its layers from `ghcr.io/<owner>/<package>/<component>:buildcache`, a publishing run writes that cache back, and a pull request only reads it

### Requirement: Release publication
Pushing a `v*` tag MUST publish a shelf release: CLI binaries for linux and darwin (amd64 and
arm64) with checksums, the `shelf-app` chart, and the platform artifact, all at the release
version, and then move the major tag (for example `v0`) before marking the release as latest.

#### Scenario: Tenant on the major tag
- **WHEN** a tenant uses `tweinmann/shelf/.github/workflows/build.yml@v0` and a new v0 release is published
- **THEN** the tenant's next build uses the new workflow together with the matching latest binary

### Requirement: App naming
The system SHALL name an app at `shelf app add <app>`, independent of app.yaml's `name`, and MUST
reject a name that is not a DNS label of at most 40 characters, that contains `--` (which separates
an app from its previews), or that is reserved for the platform (`default`, `flux-system`,
`traefik`, `cloudflared`, or a name starting with `kube-` or `shelf-`).

#### Scenario: Reserved name
- **WHEN** a user runs `shelf app add shelf-web oci://ghcr.io/acme/web:main`
- **THEN** the command fails saying the name is reserved for the platform, before contacting the cluster

#### Scenario: Invalid name
- **WHEN** the name contains upper-case letters or is longer than 40 characters
- **THEN** the command fails saying it must be a DNS label of at most 40 characters

#### Scenario: Double hyphen
- **WHEN** a user runs `shelf app add hello--copy oci://ghcr.io/acme/hello:main`
- **THEN** the command fails saying `--` is reserved for previews, before contacting the cluster

### Requirement: One artifact as several apps
The system SHALL allow the same deploy artifact to be added under several app names, each in its
own namespace, with the app's name from shelf overriding the package name in app.yaml.

#### Scenario: Second app from the same artifact
- **WHEN** an artifact already running as app `hello` is added again as `hello-two`
- **THEN** both apps run side by side in namespaces `hello` and `hello-two`, each with its own secrets

### Requirement: Artifact reference
`shelf app add` SHALL take the artifact as `oci://<registry>/<repository>:<tag>` and store the
URL and the tag separately; `--insecure-registry` allows a registry without TLS.

#### Scenario: Reference without scheme or tag
- **WHEN** the reference does not start with `oci://` or has no valid tag
- **THEN** the command fails explaining the expected form

### Requirement: App registration objects
`shelf app add` SHALL register an app as a `ResourceSetInputProvider` named after the app in
`shelf-system`, labelled `shelf.dev/app`, with type `Static` and the inputs `name`, `url`,
`tag`, `insecure`, `domain`, `tunnel`, `quick`, `registry` and `cloudflare`, plus a Secret
`app-<app>` in `shelf-system` holding the app's secret values; the platform ResourceSet then
generates the app's namespace and deploy objects from the provider.

#### Scenario: Provider and secret created
- **WHEN** `shelf app add hello oci://ghcr.io/acme/hello:main` succeeds
- **THEN** `shelf-system` holds the provider `hello` with `url` `oci://ghcr.io/acme/hello` and `tag` `main`, and the Secret `app-hello`
- **AND** the namespace `hello` carries the label `shelf.dev/app=hello` and a Secret `shelf-secrets` copied from `app-hello`

#### Scenario: Access options accepted
- **WHEN** `shelf app add` is given `--registry`, `--domain`, `--cloudflare` or `--quick`
- **THEN** the chosen connection names, domain and exposure are stored in the provider inputs (connections and exposure are specified separately)

### Requirement: Registry access for an app
An app SHALL pull its deploy artifact and images with the login of its registry connection, and
an app without one MUST get the login-less `registry-anonymous` Secret instead; shelf itself
reads the artifact at add time with the connection's login, offered only to `ghcr.io`, falling
back to the Docker keychain.

#### Scenario: App with a registry connection
- **WHEN** an app is added with `--registry acme`
- **THEN** its namespace's `shelf-registry` Secret is copied from `shelf-system/connection-registry-acme`

#### Scenario: App without a registry connection
- **WHEN** an app is added without `--registry`
- **THEN** its `shelf-registry` Secret is copied from `shelf-system/registry-anonymous` and the registry is read anonymously

#### Scenario: Unknown registry connection
- **WHEN** `--registry` names a connection that does not exist
- **THEN** the add fails before reading the artifact

### Requirement: Secret generation and backup
`shelf app add` SHALL give every secret the app declares a value: a value already in the cluster
is kept, a value only in the host backup `~/.shelf/apps/<app>/secrets.yaml` is restored, and any
other is generated; the backup MUST be written before the cluster, and the source of each value
is reported by name without the value.

#### Scenario: Restore after cluster rebuild
- **WHEN** an app is added to a fresh cluster and its backup holds a value for a declared secret
- **THEN** the value is restored from the backup and reported as "restored from the backup"

#### Scenario: Cluster and backup differ
- **WHEN** the cluster and the backup hold different values for the same secret
- **THEN** the cluster value is kept and the backup is updated to it

#### Scenario: Secret added to app.yaml later
- **WHEN** the app is added again after a new secret was declared
- **THEN** only the new secret is generated, and existing values are unchanged

### Requirement: Add waits for the rollout
`shelf app add` SHALL wait, up to `--timeout` (default 5 minutes), for the deploy artifact, the
app values and the app's release to become ready, asking each Flux controller to reconcile at
once, and SHALL report each step as progress.

#### Scenario: Registry refuses the artifact
- **WHEN** the registry refuses the app's login while Flux fetches the deploy artifact
- **THEN** the add fails with a hint to give the app a registry connection that may read it, or a new token to the one it has

### Requirement: Re-adding and pointing at another tag
Running `shelf app add` again for an existing app SHALL update its artifact reference, keep every
existing secret value, and keep its domain and connections unless others are given; this is how
an app is pointed at another tag.

#### Scenario: Move to another tag
- **WHEN** `shelf app add hello oci://ghcr.io/acme/hello:sha-1a2b3c4` runs for an app on `main`
- **THEN** the provider's `tag` becomes `sha-1a2b3c4` and the app is rolled out from that tag
- **AND** its registry connection and domain stay as they were

### Requirement: Continuous delivery from the tag
The platform SHALL poll each app's deploy artifact every minute under the registered tag and roll
out a new artifact pushed under that tag without any shelf command.

#### Scenario: New push under main
- **WHEN** the tenant workflow pushes a new artifact and moves `main` to it
- **THEN** Flux deploys the new artifact to every app registered with that artifact and tag

### Requirement: Redeploy
`shelf app redeploy <app>` SHALL ask Flux to fetch the app's artifact again under its registered
reference and wait for the rollout, changing nothing about the app.

#### Scenario: Redeploy an existing app
- **WHEN** a user runs `shelf app redeploy hello`
- **THEN** the artifact is fetched again and rolled out, and the provider is unchanged

#### Scenario: Redeploy an unknown app
- **WHEN** no app of that name exists
- **THEN** the command fails saying the app does not exist

### Requirement: App removal
`shelf app rm <app>` SHALL ask for confirmation unless `--yes` is given, delete the app's
provider and its preview provider, wait until its namespace and every preview namespace with all
objects and volumes are gone, and delete `app-<app>` and `tunnel-<app>` from `shelf-system`,
while keeping the host secret backup and saying where it is.

#### Scenario: Remove an app
- **WHEN** a user confirms `shelf app rm hello`
- **THEN** the namespace `hello`, its volumes and the app's Secrets in `shelf-system` are deleted
- **AND** the output names the kept backup file, so a later `shelf app add hello` restores the old secret values

#### Scenario: Remove an app with previews
- **WHEN** a user confirms `shelf app rm hello` and `hello` has the previews `hello--feature-login` and `hello--fix-typo`
- **THEN** both preview namespaces and their volumes are deleted along with the app

#### Scenario: Namespace not created by the platform
- **WHEN** a namespace named after the app exists without the label `shelf.dev/app=<app>`
- **THEN** removal fails saying the namespace does not belong to the app

#### Scenario: Unknown app
- **WHEN** neither provider, namespace nor secrets exist for the name
- **THEN** the command fails saying the app does not exist

### Requirement: Reading generated secrets
`shelf app secrets <app>` SHALL list the names of the app's generated secrets and print their
values only with `--reveal`.

#### Scenario: Without reveal
- **WHEN** a user runs `shelf app secrets hello`
- **THEN** only the secret names are printed, with a note on stderr to pass `--reveal` for the values

### Requirement: Listing artifact tags
`shelf app tags <app>` SHALL list the tags of the app's deploy artifact, newest first, read from
the GitHub API with the app's registry connection, marking the tag the app runs with `*` and
omitting `sha256-` signature and attestation tags; no deploy path depends on this listing.

#### Scenario: App without a registry connection
- **WHEN** the app has no registry connection
- **THEN** the command fails saying shelf cannot list packages without one

#### Scenario: Current tag marked
- **WHEN** the app runs tag `main`
- **THEN** the `main` line is marked with `*`

### Requirement: Five-stage diagnosis
The system SHALL diagnose an app as five ordered stages: the deploy artifact (OCIRepository
`deploy`), the app values (Kustomization `deploy`), the chart (OCIRepository `shelf-app`), the
app (HelmRelease), and the pods; each stage is Ready, Working, Failed or Missing, and the app's
state is that of the first stage that is not Ready.

#### Scenario: Image cannot be pulled
- **WHEN** all Flux stages are Ready and a pod waits in `ImagePullBackOff`
- **THEN** the pods stage is Failed with a hint that the image cannot be pulled, and the app is Failed

#### Scenario: Secret missing from the app's Secret
- **WHEN** a pod fails with `CreateContainerConfigError` because a key is missing in `shelf-secrets`
- **THEN** the hint names the secret and says to deploy the app again so shelf generates it

#### Scenario: Not yet created
- **WHEN** the platform has not created the app's objects yet
- **THEN** the first stage is Missing with the message that the platform has not created the app yet

### Requirement: App status command
`shelf app status` SHALL list every app with its state and, when not Ready, the failing stage and
reason in one line; `shelf app status <app>` SHALL print the app's address, artifact, registry
connection, running revision, the git commit that revision was built from, deploy time,
components, every stage with its message and, if not Ready, its hint, and the app's previews with
their branch tag, state and commit; `--preview <branch>` SHALL print the same for one preview.

#### Scenario: No apps
- **WHEN** the cluster has no registered apps
- **THEN** `shelf app status` prints "No apps yet."

#### Scenario: One app in detail
- **WHEN** a user runs `shelf app status hello` for a healthy app
- **THEN** the output shows `hello: Ready`, the artifact reference, the revision, and all five stages as Ready

#### Scenario: Commit of the running artifact
- **WHEN** the app's deploy artifact was pushed by the reusable workflow for commit `42a964eb0b4b…`
- **THEN** `shelf app status hello` shows that commit, read from the artifact's revision annotation as the cluster reports it, without calling a forge API

#### Scenario: Artifact without a commit
- **WHEN** the deploy artifact carries no git revision
- **THEN** the commit line is left out

#### Scenario: App with previews
- **WHEN** `hello` has previews for `feature/login` and `fix-typo`
- **THEN** `shelf app status hello` lists both with their tags, states and commits, and the list of all apps shows them under `hello`, not as apps of their own

#### Scenario: One preview
- **WHEN** a user runs `shelf app status hello --preview feature/login`
- **THEN** the output shows the preview's namespace `hello--feature-login`, its host, artifact tag, commit, components and five stages

### Requirement: App operations require the platform
`shelf app add`, `shelf app credentials` and `shelf app redeploy`, and their admin UI jobs, MUST
fail at once when the ResourceSet `apps` is missing from `shelf-system`, before writing anything to
the cluster, the host backup or Cloudflare. The error SHALL say that the cluster has no platform and
that `shelf init cluster` installs it.

#### Scenario: Adding an app to a cluster without the platform
- **WHEN** `shelf app add greeter oci://ghcr.io/owner/greeter:main --cloudflare home --domain example.com` runs against a cluster where `shelf init cluster` never installed the platform
- **THEN** it fails within seconds, saying the cluster has no platform and to run `shelf init cluster`
- **AND** no Secret or provider exists in `shelf-system` for `greeter`, no backup file was written, and nothing was changed in Cloudflare

#### Scenario: Redeploying in a cluster without the platform
- **WHEN** `shelf app redeploy greeter` runs and the ResourceSet `apps` is missing
- **THEN** it fails at once with the same message instead of waiting for the deploy artifact until its timeout

#### Scenario: Cluster with the platform
- **WHEN** the ResourceSet `apps` exists
- **THEN** the operation runs as before, and its output is unchanged
