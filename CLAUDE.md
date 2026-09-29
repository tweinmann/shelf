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
Phase 3 complete: `shelf init cluster` (Flux Operator, FluxInstance, platform artifact with
Traefik), dev registry, `just smoke-init`. Results and decisions in docs/plan.md.
Phase 4 complete: deploy artifact, ResourceSet `apps`, `shelf app add`/`rm`, reusable workflow,
level 2 in CI. Accepted with the tenant repo `tweinmann/shelf-hello`: a push reached the app in
173 s.
Phase 4b complete: `build: ./web`, `shelf build-plan`, `shelf render --image`, `release.yml`;
releases `v0.1.0` and `v0.2.0` published. A tenant repo holds `app.yaml` plus one boilerplate
workflow; everything of an app lives under `ghcr.io/<owner>/<app>` in the registry.
Phase 5 complete and approved: `shelf init expose` (Cloudflare tunnel, cloudflared, DNS
records written by shelf), `just smoke-expose`. `greeter-dev.<domain>` is reachable over HTTPS
from the dev cluster. Results in docs/plan.md.
Target product revised on 2026-09-20, after Phase 5: shelf becomes an appliance with an admin UI
(host service on the mini, LAN with a login, one-command install plus a browser wizard), and the
phases from 6 on were re-cut — the old Phase 6 (Mac mini) is now Phase 9. Decisions and the new
phase plan in docs/plan.md; the work happens on the branch `appliance`, so `main` still carries
the old path.
Phase 6 complete: `internal/ops` holds the operations both the CLI and the server call,
`internal/progress` replaces the `io.Writer` progress pattern with typed events, `cli.New` takes
injected dependencies instead of package variables, and the CLI tests run in parallel under
`-race`. The output did not change.
Phase 7 complete: `shelf serve` is the admin UI — claim with a setup code, password login, and a
read-only dashboard that lists the apps and says which step is broken. `cluster.AppStates` and
the five-stage diagnosis back both the pages and `shelf app status`.
Phase 8 complete: apps are added, pointed at another tag, deployed again and
removed from the browser. Each change is a job with a live log that reads like the command
line's output; one change at a time. `shelf app redeploy` and `shelf app secrets` keep the CLI
level with the UI. The app page also lists the app's components with their addresses, the routed
ones as links. Results in docs/plan.md.
Phase 8b in progress: connections instead of platform credentials. Registry logins
(`shelf-system/connection-registry-<name>`) and Cloudflare tokens (`~/.shelf/connections/cloudflare/`)
are defined once with `shelf connection add` or on the UI page `/connections`, and chosen per app
with `--registry`/`--cloudflare`. An exposed app has its own tunnel in the connection's account and
cloudflared in its namespace; `shelf init expose` and the shared login and tunnel are gone. Level 1
green; level 2 and 3 not run yet, because migrating the dev cluster takes `greeter-dev.tobile.ch`
off the shared tunnel.
Also in 8b (2026-09-29): `name` in app.yaml names the packages only; the app's name is chosen at
`shelf app add`, so one artifact can run as several apps. Level 1 green; release v0.5.0 open.
Next: Phase 9 (Mac mini: host setup, Colima, `shelf doctor`, `shelf destroy`).

## Working agreements

- Work phase by phase as laid out in docs/plan.md. At the end of each phase, stop, show the
  result, and wait for approval before starting the next.
- Ask about open design decisions instead of assuming. Record the answer in docs/plan.md.
- Keep this file and docs/plan.md current at the end of every phase.
- Everything in the repo is English: code, comments, docs, commit messages, CLI output.
  Conversation with the maintainer may be German or English.

## Guardrails

- The registry is the only interface between build and platform. The platform never talks to
  Git or forge APIs. That rule is about the cluster: the host service may call the GitHub API for
  convenience (checking for a newer release), as long as no deploy path depends on it.
- Every action the admin UI offers exists as a CLI command, and both call the same function in
  `internal/ops`. The UI is a second face on one operations layer, never a second implementation.
- The platform is generic: no knowledge of databases or specific services. Everything is a
  component.
- Apps are isolated from each other. The only thing apps share is a connection the user assigned
  to several of them; namespace, secret values, tunnel and cloudflared are every app's own.
- Prefer standard building blocks used by larger platforms (Flux, Helm, Traefik,
  Cloudflare Tunnel) over custom code.
- `shelf init cluster` must work against any kubecontext. No Colima or macOS assumptions
  outside `internal/host`.

## Dev environment

All development happens inside the devcontainer on Docker Desktop. It runs its own Docker
daemon (Docker-in-Docker), which also hosts the k3d cluster. Nothing is installed on the host
Mac. Tool versions are pinned in `.devcontainer/Dockerfile`.

| Command | Run from | Purpose |
|---|---|---|
| `just test` | devcontainer | level-1 checks, same as CI (gofmt, vet, `go test -race`, helm lint, kubeconform, examples) |
| `just golden` | devcontainer | rewrite golden files and `schema/app.schema.json` after an intended change |
| `just build` | devcontainer | build `bin/shelf` (linux) |
| `just cluster-up` | devcontainer | create or start k3d cluster `shelf-dev` with its registry `shelf-registry:5000` (added to `/etc/hosts`), write kubeconfig (run after every container restart or rebuild) |
| `just cluster-stop` | devcontainer | stop the cluster to free memory |
| `just cluster-down` | devcontainer | delete the cluster |
| `just cluster-reset` | devcontainer | delete and recreate the cluster |
| `just platform-push` | devcontainer | push `platform/` to the dev registry as `oci://shelf-registry:5000/shelf/platform:dev` |
| `just chart-push` | devcontainer | push the chart as `oci://shelf-registry:5000/shelf/charts/shelf-app:0.0.0-dev` |
| `just serve` | devcontainer | run the admin UI against the dev cluster on port 8080 (forwarded to the Mac's browser); prints the setup code on the first start |
| `just init-cluster [--yes]` | devcontainer | `shelf init cluster` against the dev cluster with the dev platform and chart, domain `dev.local` |
| `just flux-operator-update <version>` | devcontainer | replace the embedded Flux Operator manifest |
| `just smoke-secrets` | devcontainer | check `$(VAR)` expansion from `secretKeyRef` |
| `just smoke-registry <image>` | devcontainer | check a private GHCR pull via `imagePullSecret` |
| `just smoke-chart` | devcontainer | install `examples/hello` with the chart, check the Phase 2 acceptance criteria (needs Docker Hub) |
| `just smoke-init` | devcontainer | Phase 3 acceptance: init twice, re-push, routing and `stripPrefix` through Traefik (run `just cluster-reset` first; needs network) |
| `just smoke-apps` | devcontainer | Phase 4: `shelf app add`/`rm`, rollout by polling, tampered artifact refused, restore from backup (needs network) |
| `just smoke-tenant <app> <artifact>` | devcontainer | Phase 4 acceptance with a real tenant repo and GHCR; asks for the GHCR login, waits for a push |
| `just smoke-expose <app> <domain>` | devcontainer | Phase 8b acceptance: the app gets the Cloudflare connection `smoke`, its own tunnel, DNS record and HTTPS, then is taken off again; asks for the Cloudflare API token; needs a cluster with a host suffix |
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
- `internal/host` (brew, pmset, colima, launchctl) cannot run in the devcontainer. Test it
  through the fake command runner. The launchd service is a level-3 concern too.
- Secret values never appear in rendered manifests, logs or golden files (golden files may
  hold obviously fake values such as `not-a-real-token`). Tokens are read from `GHCR_TOKEN` and
  `CF_API_TOKEN` only, never from a flag, and only by `shelf connection add`. The same holds for
  the files under `~/.shelf`: a token never goes into the launchd plist, into a log line or into a
  rendered page, and a form never sends one back. `/connections` is the only page with token fields.
- A Cloudflare token lives in `~/.shelf/connections/cloudflare/<name>.yaml` and never in the
  cluster. A registry token lives in `shelf-system/connection-registry-<name>` and is copied into
  the namespaces of the apps that chose that connection only.
- Tunnels of the dev cluster are named `shelf-dev-<app>`; never delete a Cloudflare tunnel or
  record by anything but its exact name, and never one without the `-dev` suffix.
- `shelf app rm` deletes an app's volumes; never run it against an app you did not create in
  this session.
- `shelf init cluster` with a different `--host-suffix` moves every app to another host name, a
  different `--domain` every app without a domain of its own, and strands their DNS records. It
  refuses to do that while such apps exist unless `--move-hosts` is passed. The smoke tests install `dev.local` and refuse to run against a
  cluster that serves anything else — run `just cluster-reset` first, or restore the domain
  afterwards.

## Conventions

- API group `shelf.dev/v1alpha1`; system namespace `shelf-system`; app namespace = app name
- A component has either `image:` or `build: ./dir`; package names are the workflow's business,
  never `app.yaml`'s: the deploy artifact is `ghcr.io/<owner>/<name>`, a built image
  `ghcr.io/<owner>/<name>/<component>`
- `name` in app.yaml is the package name only. An app is named at `shelf app add <app>`
  (`ops.CheckAppName`), so one artifact can run as several apps; the artifact's ConfigMap is
  `shelf-values`, and the HelmRelease passes the app's name as `values`
- Reading a deploy artifact uses the login of the app's registry connection, and only for the
  registry it is for (`ghcr.io`); `ops.Env.Pull` overrides it, the Docker keychain is the last
  resort. A machine running shelf as a service has no Docker config
- Per app in `shelf-system`, all labelled `shelf.dev/app`: the provider `<app>` (inputs `name`,
  `url`, `tag`, `insecure`, `domain`, `tunnel`, `registry`, `cloudflare` — the last two are
  connection names), `app-<app>` (secret values), `tunnel-<app>`. Registry connections are
  `connection-registry-<name>` (label `shelf.dev/connection=registry`), and `registry-anonymous`
  is what an app without one gets. An app's tunnel is `shelf<host-suffix>-<app>`, its host
  `<app><host-suffix>.<own domain or the cluster's>`
- Secret env prefix `SHELF_SECRET_<NAME>`; secret values live in the Secret `shelf-secrets` in
  the app namespace, one key per secret name; labels `shelf.dev/app`, `shelf.dev/component`;
  OCI annotations `dev.shelf.*`
- Go: standard layout (`cmd/`, `internal/`), table-driven tests, golden files in `testdata/`
  (`-update` via `just golden`); the registry is behind `render.Resolver`, so tests never need
  the network
- `internal/cluster` talks to the API server only (client-go, server-side apply with field
  manager `shelf`); it must not shell out to kubectl, helm or flux, and it knows nothing about
  `~/.shelf`, HTTP or the operator's machine. Orchestration lives in `internal/ops`, flags and
  prompts in `internal/cli`, routes and templates in `internal/server`
- Operations report through `progress.Reporter`, never to an `io.Writer`: the CLI renders the
  events as text (`progress.Writer`), the server keeps them as a job log. The exact wording of
  a line is decided in `internal/progress` alone, with a golden file over every kind of event
- What an operation needs from the machine it runs on (backup directory, tokens, registry
  authenticator) goes into `ops.Env`; what a test replaces (cluster, registry, Cloudflare) is a
  field of `ops.Ops`. No package-level variable is a test seam — tests must be able to run in
  parallel, which also rules out `t.Setenv`: the CLI reads the environment through
  `cli.Options.Getenv`
- `internal/server` renders `html/template` pages from `internal/server/ui`, embedded in the
  binary; every page has a golden file in `internal/server/testdata`, rendered against a fake
  `Platform` with a fixed clock, so its tests need neither cluster nor network
- A change the admin UI makes runs as a job in `internal/server`: it reports `progress.Event`s,
  the page renders them as the text the CLI prints, and the browser follows the rest over
  server-sent events. One change runs at a time, and every one is written to `~/.shelf/audit.log`
- Files under `~/.shelf` belong to `internal/hostcfg`: mode 0600 in a 0700 directory, written
  atomically. Passwords are PBKDF2-SHA256 with the algorithm in the stored string
- Chart tests run `helm template` from Go (`internal/chart`); they read the chart files
  themselves so that go test's cache notices chart changes
- Adding a Go module in a devcontainer built before the `/go/pkg` fix (see Phase 1 results):
  `GOPATH=$HOME/go GOMODCACHE=/go/pkg/mod go get …`
- Shell: `hack/*.sh` use bash with `set -euo pipefail` and source `hack/lib.sh`;
  `hack/nuke.sh` is POSIX `sh` because it runs on the host; hack scripts require
  `SHELF_DEVCONTAINER=1`
