# branch-previews Specification

## Purpose
Gives each branch of an app its own isolated, running copy of the app's complete stack on the
user's own hardware, discovered from the registry, reachable only from the operator's machine,
and removed when the branch is.

## Requirements

### Requirement: Previews are the operator's decision
`shelf app add` and `shelf app credentials` SHALL take `--previews[=N]`, which gives the app at
most N previews (3 without a number, at most 10), and `--no-previews`, which removes them all; an app has
no previews unless the operator chose them, and nothing in app.yaml or the tenant repository can
change that.

#### Scenario: Previews switched on
- **WHEN** a user runs `shelf app add hello oci://ghcr.io/acme/hello:main --previews`
- **THEN** the app gets previews for at most 3 branches

#### Scenario: Default is off
- **WHEN** an app is added without `--previews`
- **THEN** no branch of it gets a preview, whatever the registry holds

#### Scenario: Switched off again
- **WHEN** a user runs `shelf app credentials hello --no-previews`
- **THEN** every preview of `hello` is removed with its namespace and volumes, and the app itself keeps running

#### Scenario: Platform without previews
- **WHEN** `--previews` is given for a cluster whose platform has no ResourceSet `previews`
- **THEN** the command fails at once saying the platform predates previews and that `shelf init cluster` updates it, and writes nothing

#### Scenario: Limit out of range
- **WHEN** `--previews=0` or `--previews=11` is given
- **THEN** the command fails before contacting the cluster

#### Scenario: Number without `=`
- **WHEN** `--previews 5` is given
- **THEN** the command fails saying to write `--previews=5`, before contacting the cluster

### Requirement: Preview provider
An app with previews SHALL have a `ResourceSetInputProvider` `<app>--previews` in `shelf-system`,
labelled `shelf.dev/previews=<app>` and not `shelf.dev/app`, of type `OCIArtifactTag` on the
app's deploy artifact, filtered to tags `branch-*` and limited to N, reading the registry with
the app's registry connection, and checked every 30 seconds.

#### Scenario: Provider of an app with a registry connection
- **WHEN** `hello` with registry connection `ghcr` and artifact `oci://ghcr.io/acme/hello:main` gets previews
- **THEN** `shelf-system/hello--previews` has type `OCIArtifactTag`, url `oci://ghcr.io/acme/hello`, `includeTag` `^branch-`, limit 3 and the image pull secret `connection-registry-ghcr`

#### Scenario: Not an app
- **WHEN** the platform lists the apps
- **THEN** `hello--previews` is not among them

### Requirement: Branch tag
A branch SHALL be published as the tag `branch-<slug>`, where the slug is the branch name in
lower case with every run of characters other than letters and digits replaced by one `-`,
without leading or trailing `-`, and, when longer than 20 characters, its first 13 characters
followed by `-` and the first 6 hex digits of the SHA-256 of the branch name.

#### Scenario: Short branch name
- **WHEN** the branch is `feature/Login`
- **THEN** the tag is `branch-feature-login`

#### Scenario: Long branch name
- **WHEN** two branches share their first 20 characters after slugging
- **THEN** their tags still differ, and neither slug is longer than 20 characters

### Requirement: Preview naming
A preview of `<app>` for the tag `branch-<slug>` SHALL run in the namespace `<app>--<slug>`,
answer at `<app>--<slug>.shelf.internal` inside the cluster, and carry the labels
`shelf.dev/app=<app>` and `shelf.dev/preview=<slug>`.

#### Scenario: Preview of a branch
- **WHEN** `hello` has previews and the registry holds `branch-feature-login`
- **THEN** the namespace `hello--feature-login` exists with those labels, and its web component answers at `hello--feature-login.shelf.internal`

### Requirement: Preview contents
The platform SHALL give each preview the same deploy chain as an app (its deploy artifact at the
branch tag, checked every 30 seconds, the app values, the chart, the release), verified against
the cluster's signer, the app's registry login, a ResourceQuota (requests of 1 CPU, 2Gi memory,
5Gi storage) and a LimitRange that gives containers without requests a default; it MUST NOT give
a preview a tunnel, cloudflared or a DNS record.

#### Scenario: Preview of an exposed app
- **WHEN** an app exposed through a Cloudflare connection has a preview
- **THEN** the preview namespace has no cloudflared, and nothing outside the cluster reaches it

#### Scenario: Over the quota
- **WHEN** a preview's pods would request more than 2Gi memory together
- **THEN** the pods beyond it are refused, and the preview's pods stage says so

#### Scenario: Unsigned branch artifact
- **WHEN** an artifact without a signature is pushed as `branch-feature-login`
- **THEN** the preview `hello--feature-login` does not apply it, and its deploy artifact stage says the verification failed

#### Scenario: Built image of a preview
- **WHEN** a preview's values name an unsigned built image of the app's package
- **THEN** its pod is denied by the app's admission policy, as in the app's namespace

### Requirement: A preview's own state
A preview SHALL have its own volumes and its own secret values, generated in the cluster when the
preview is first created and kept for its lifetime; it MUST NOT share a volume, a secret value or a
database with the app or with another preview, and its secrets have no host backup.

#### Scenario: Two branches with a database each
- **WHEN** `hello` with a Postgres component has previews for two branches
- **THEN** each preview runs its own Postgres with its own volume and its own generated password, both different from the app's

#### Scenario: Preview updated
- **WHEN** a new commit is pushed to a branch that has a preview
- **THEN** the preview rolls out the new artifact and keeps its secret values and its data

### Requirement: Preview removal
The platform SHALL remove a preview, with its namespace and volumes, when its tag disappears from
the registry, when the app's previews are switched off, and when the app is removed.

#### Scenario: Branch deleted
- **WHEN** the tag `branch-feature-login` is deleted from the registry
- **THEN** within a few minutes the namespace `hello--feature-login` and its volumes are gone, and the app and its other previews are unchanged

### Requirement: Preview limit
An app SHALL have at most N previews at a time, chosen by the provider from the branch tags, and
`shelf app status <app>` SHALL say when the limit is reached.

#### Scenario: More branches than the limit
- **WHEN** `hello` has `--previews=2` and the registry holds three branch tags
- **THEN** exactly two previews exist, and `shelf app status hello` says the preview limit of 2 is reached

### Requirement: Previews are never exposed
`--cloudflare`, `--domain` and `--quick` SHALL apply to the app only; a preview MUST be reachable
only inside the cluster and through `shelf app open <app> --preview <branch>`.

#### Scenario: Quick tunnel app
- **WHEN** an app with a quick tunnel has previews
- **THEN** its previews have no quick tunnel and no `*.trycloudflare.com` address

### Requirement: Turnaround of a preview
A new commit on a branch with a preview SHALL be Ready in the preview within the CI run plus at
most 30 seconds plus its rollout, and a first push of a new branch within the CI run plus at most
60 seconds plus the first install; `smoke-previews` SHALL measure both without CI.

#### Scenario: Measured at level 2
- **WHEN** `just smoke-previews` pushes the first artifact of a branch and later a second one
- **THEN** it prints the seconds from each push to the preview's Ready verdict, and the results of the change record them
