# shelf

A self-hosted app platform for a single Apple Silicon Mac mini. Tenants build images in their
own CI and push a deploy artifact to GHCR; shelf watches the artifact and deploys it.
Learning project for platform engineering — not for production.

Design, decisions and phase plan: [docs/plan.md](docs/plan.md). Read it before changing
architecture.

## Status

Phase 0 complete: devcontainer, dev cluster, smoke tests, `hack/nuke.sh`.
Phase 0b complete and approved: switch from Docker-outside-of-Docker to Docker-in-Docker.
Phase 1 complete: `shelf validate`, `shelf render`, `shelf schema`, JSON Schema,
`examples/hello`, `just test`, CI.
Phase 2 complete: chart `charts/shelf-app`, golden-file tests in `internal/chart`,
`just smoke-chart`. Results and decisions in docs/plan.md.
In progress: Phase 3 (`shelf init cluster`).

## Working agreements

- Work phase by phase as laid out in docs/plan.md. At the end of each phase, stop, show the
  result, and wait for approval before starting the next.
- Ask about open design decisions instead of assuming. Record the answer in docs/plan.md.
- Keep this file and docs/plan.md current at the end of every phase.
- Everything in the repo is English: code, comments, docs, commit messages, CLI output.
  Conversation with the maintainer may be German or English.

## Guardrails

- The registry is the only interface between build and platform. The platform never talks to
  Git or forge APIs.
- The platform is generic: no knowledge of databases or specific services. Everything is a
  component.
- Apps are isolated from each other; nothing is shared between apps.
- Prefer standard building blocks used by larger platforms (Flux, Helm, Traefik,
  external-dns) over custom code.
- `shelf init cluster` must work against any kubecontext. No Colima or macOS assumptions
  outside `internal/host`.

## Dev environment

All development happens inside the devcontainer on Docker Desktop. It runs its own Docker
daemon (Docker-in-Docker), which also hosts the k3d cluster. Nothing is installed on the host
Mac. Tool versions are pinned in `.devcontainer/Dockerfile`.

| Command | Run from | Purpose |
|---|---|---|
| `just test` | devcontainer | level-1 checks, same as CI (gofmt, vet, tests, helm lint, kubeconform, examples) |
| `just golden` | devcontainer | rewrite golden files and `schema/app.schema.json` after an intended change |
| `just build` | devcontainer | build `bin/shelf` (linux) |
| `just cluster-up` | devcontainer | create or start k3d cluster `shelf-dev`, write kubeconfig (run after every container restart or rebuild) |
| `just cluster-stop` | devcontainer | stop the cluster to free memory |
| `just cluster-down` | devcontainer | delete the cluster |
| `just cluster-reset` | devcontainer | delete and recreate the cluster |
| `just smoke-secrets` | devcontainer | check `$(VAR)` expansion from `secretKeyRef` |
| `just smoke-registry <image>` | devcontainer | check a private GHCR pull via `imagePullSecret` |
| `just smoke-chart` | devcontainer | install `examples/hello` with the chart, check the Phase 2 acceptance criteria (needs Docker Hub) |
| `hack/nuke.sh` | host Mac terminal (refuses to run in a container) | remove every Docker object shelf created (only needs `docker`) |

## Safety rules

- The devcontainer sets `KUBECONFIG` to `~/.kube/shelf-dev.yaml`. Never write to a shared
  kubeconfig, never switch contexts, and never target a cluster other than `shelf-dev` unless
  explicitly told to. Scripts in `hack/` enforce this through `require_devcontainer`.
- `docker` inside the devcontainer talks to its own daemon; `require_devcontainer` checks that.
  Never mount the host's Docker socket or otherwise reach the host daemon from here.
- The host Docker daemon is shared with the maintainer's other containers and volumes. The only
  shelf objects on it are those in the footprint inventory in docs/plan.md (devcontainer,
  its images and volumes). Delete them by exact name — never by prefix, never with `prune`.
  New host objects go into the inventory, `hack/nuke.sh` and (for volumes)
  `devcontainer.json` together.
- A plain `go build` produces a Linux binary. Anything shipped to the mini needs
  `GOOS=darwin GOARCH=arm64`.
- `internal/host` (brew, pmset, colima) cannot run in the devcontainer. Test it through the
  fake command runner.
- Secret values never appear in rendered manifests, logs or golden files.

## Conventions

- API group `shelf.dev/v1alpha1`; system namespace `shelf-system`; app namespace = app name
- Secret env prefix `SHELF_SECRET_<NAME>`; secret values live in the Secret `shelf-secrets` in
  the app namespace, one key per secret name; labels `shelf.dev/app`, `shelf.dev/component`;
  OCI annotations `dev.shelf.*`
- Go: standard layout (`cmd/`, `internal/`), table-driven tests, golden files in `testdata/`
  (`-update` via `just golden`); the registry is behind `render.Resolver`, so tests never need
  the network
- Chart tests run `helm template` from Go (`internal/chart`); they read the chart files
  themselves so that go test's cache notices chart changes
- Adding a Go module in a devcontainer built before the `/go/pkg` fix (see Phase 1 results):
  `GOPATH=$HOME/go GOMODCACHE=/go/pkg/mod go get …`
- Shell: `hack/*.sh` use bash with `set -euo pipefail` and source `hack/lib.sh`;
  `hack/nuke.sh` is POSIX `sh` because it runs on the host; hack scripts require
  `SHELF_DEVCONTAINER=1`
