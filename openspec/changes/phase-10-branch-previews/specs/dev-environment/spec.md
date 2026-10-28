# Spec Delta

## MODIFIED Requirements

### Requirement: Dev cluster
`just cluster-up` SHALL create, or start if it exists, the k3d cluster `shelf-dev` with the k3s
image pinned in the Dockerfile, without k3s's own Traefik and without k3d's load balancer, its API
server on the container's loopback, and the plain-HTTP registry `shelf-registry:5000`, reachable
by that name from pods and from the devcontainer, which allows deleting manifests. It MUST write the kubeconfig only to
`KUBECONFIG` and never change a default kubeconfig or switch a context.

#### Scenario: After a container restart
- **WHEN** the devcontainer was rebuilt or restarted and `just cluster-up` runs
- **THEN** the existing cluster is started, and its kubeconfig is written again

#### Scenario: Reset
- **WHEN** `just cluster-reset` runs
- **THEN** cluster and registry are deleted and created empty, and `just init-cluster` pushes the platform and chart again before installing them

#### Scenario: Deleting a tag
- **WHEN** a smoke test deletes a branch tag from `shelf-registry:5000`
- **THEN** the registry accepts the deletion, so a preview can be removed at level 2 as on GHCR

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
- **THEN** CI runs `smoke-secrets`, `smoke-chart`, `smoke-init`, `smoke-apps`, `smoke-agent` and `smoke-previews` with `SMOKE_FULL=1`

#### Scenario: main fails
- **WHEN** CI fails on `main`
- **THEN** it opens an issue labelled `ci-main-red`, or comments on the one that is open, with the commit and the run

#### Scenario: Release gate
- **WHEN** a release tag is pushed for a commit whose CI run on `main` did not succeed
- **THEN** `release.yml` stops before it publishes anything
