# dev-environment Specification

## Purpose
Describes how shelf is developed and checked without touching the maintainer's Mac: a pinned
devcontainer with its own Docker daemon, a disposable k3d cluster, three test levels, and an exact,
removable footprint on the host Docker daemon.

## Requirements

### Requirement: Pinned toolchain in the devcontainer
Every tool used to build, test and run shelf in development SHALL come from the devcontainer
image, with its version pinned in `.devcontainer/Dockerfile`, which is the single source of tool
versions; the Docker engine version is pinned in `.devcontainer/devcontainer.json`.

#### Scenario: A tool version changes
- **WHEN** a tool such as k3d, kubectl, Helm, Flux, just, kubeconform, Node or OpenSpec is upgraded
- **THEN** the change is an `ARG` in `.devcontainer/Dockerfile`, and CI picks it up because it runs inside that image

### Requirement: A Docker daemon of its own
The devcontainer SHALL run its own Docker daemon (Docker-in-Docker) and MUST NOT mount or reach
the host's Docker socket, so the dev cluster and its images never appear on the host daemon.

#### Scenario: docker inside the container
- **WHEN** `docker info` runs inside the devcontainer
- **THEN** it reports the container's own host name, not the Docker Desktop VM

### Requirement: Guard in the hack scripts
Every script in `hack/` except `hack/nuke.sh` MUST abort unless `SHELF_DEVCONTAINER=1`,
`KUBECONFIG` is `/home/vscode/.kube/shelf-dev.yaml`, and the Docker daemon reports the
container's own host name.

#### Scenario: Run outside the devcontainer
- **WHEN** a `hack/` script runs where `SHELF_DEVCONTAINER` is not set
- **THEN** it stops with "run this inside the shelf devcontainer" before doing anything

#### Scenario: A host daemon reached through a socket
- **WHEN** `docker` in the container talks to a daemon whose name differs from the container's host name
- **THEN** the script stops and names both

### Requirement: Dev cluster
`just cluster-up` SHALL create, or start if it exists, the k3d cluster `shelf-dev` with the k3s
image pinned in the Dockerfile, without k3s's own Traefik and without k3d's load balancer, its API
server on the container's loopback, the plain-HTTP registry `shelf-registry.local:5000` that allows
deleting manifests, and a dev cosign key pair under `.local/cosign/`. It MUST write the
kubeconfig only to `KUBECONFIG` and never change a default kubeconfig or switch a context.

#### Scenario: After a container restart
- **WHEN** the devcontainer was rebuilt or restarted and `just cluster-up` runs
- **THEN** the existing cluster is started, and its kubeconfig is written again

#### Scenario: Reset
- **WHEN** `just cluster-reset` runs
- **THEN** cluster and registry are deleted and created empty, and `just init-cluster` pushes the platform and chart again before installing them

#### Scenario: Deleting a tag
- **WHEN** a smoke test deletes a branch tag from `shelf-registry.local:5000`
- **THEN** the registry accepts the deletion, so a preview can be removed at level 2 as on GHCR

#### Scenario: Dev artifacts are signed
- **WHEN** `just init-cluster`, `just platform-push`, `just chart-push` or a smoke test pushes an artifact or a built image to `shelf-registry.local:5000`
- **THEN** it is signed with the dev key without a transparency log entry, and the cluster, installed with `--signer-key .local/cosign/cosign.pub`, verifies it

### Requirement: Three test levels
Checks SHALL be split into level 1 (no cluster: `just test`, which runs gofmt, `go vet`,
`go test -race`, Helm lint, kubeconform, example validation and `openspec validate --all --strict`),
level 2 (the k3d cluster: the `just smoke-*` recipes), and level 3 (the Mac mini and real
Cloudflare), and a task that needs level 2 or 3 MUST say so.

#### Scenario: Level 1 in CI
- **WHEN** a commit is pushed to `main` or a pull request is opened or pushed to
- **THEN** CI runs `just test` inside the devcontainer image on an arm64 runner

#### Scenario: Level 2 in CI
- **WHEN** a pull request is opened or reopened as ready, leaves draft, or is pushed to or labelled while it carries the label `level-2`
- **THEN** CI also runs the smoke tests its changed paths need, chosen by `.github/scripts/select-smokes.sh`, each against a fresh k3d cluster of its own, side by side, with `SMOKE_FULL=0`

#### Scenario: Documentation only
- **WHEN** a pull request changes only planning, documentation or examples
- **THEN** CI runs no smoke test, and a path the selection does not know runs all of them

#### Scenario: Level 2 in full
- **WHEN** a commit is pushed to `main`, or a pull request carries the label `level-2-full`
- **THEN** CI runs `smoke-secrets`, `smoke-chart`, `smoke-init`, `smoke-apps`, `smoke-agent`, `smoke-previews` and `smoke-verify` with `SMOKE_FULL=1`

#### Scenario: main fails
- **WHEN** CI fails on `main`
- **THEN** it opens an issue labelled `ci-main-red`, or comments on the one that is open, with the commit and the run

#### Scenario: Release gate
- **WHEN** a release tag is pushed for a commit whose CI run on `main` did not succeed
- **THEN** `release.yml` stops before it publishes anything

### Requirement: Footprint on the host Docker daemon
The only objects shelf development SHALL create on the host Docker daemon are the devcontainer
`shelf-devcontainer`, its image, the base image, and the volumes `shelf-gomodcache`,
`shelf-gocache`, `shelf-claude-config`, `shelf-docker` and `shelf-containerd`. Every new host
object MUST be added to this requirement, to `hack/nuke.sh` and, for volumes, to
`.devcontainer/devcontainer.json` in the same change.

#### Scenario: The inventory
- **WHEN** `hack/nuke.sh` lists what shelf left on the host daemon
- **THEN** it finds at most these objects:

| Object | Name | Removed by `hack/nuke.sh` |
|---|---|---|
| Container | `shelf-devcontainer` | yes |
| Image | the devcontainer image (`vsc-shelf-…`, generated name) | yes, resolved through the container |
| Image | the base image named in the Dockerfile's `FROM` | only with `--with-shared-images` |
| Volumes | `shelf-gomodcache`, `shelf-gocache`, `shelf-claude-config` | yes |
| Volumes | `shelf-docker`, `shelf-containerd` (the inner daemon, with the k3d cluster and registry) | yes; volumes on these targets are also resolved through the container |

#### Scenario: Everything k3d creates
- **WHEN** the dev cluster, its registry and workload images exist
- **THEN** they live in the inner daemon, inside `shelf-docker`, and none of them is an object on the host daemon

#### Scenario: Left in place on purpose
- **WHEN** `hack/nuke.sh` has run
- **THEN** the shared `vscode` volume, the build cache and untagged intermediate images are still there, and the script reports them instead of removing them

### Requirement: Cleanup by exact name
`hack/nuke.sh` SHALL be a POSIX `sh` script that needs only `docker`, MUST refuse to run inside a
container, MUST list what it found and ask before removing it unless `--yes` is given, and MUST
remove objects by exact name or by resolving them through the devcontainer, never by prefix and
never with `docker … prune`.

#### Scenario: Run inside the devcontainer
- **WHEN** `hack/nuke.sh` runs where `/.dockerenv` exists or `SHELF_DEVCONTAINER` is set
- **THEN** it stops with "run this in a terminal on the host Mac, not inside a container"

#### Scenario: A foreign container with a similar name
- **WHEN** the host has a container `shelf-other` that belongs to another project
- **THEN** `hack/nuke.sh` neither lists nor removes it
