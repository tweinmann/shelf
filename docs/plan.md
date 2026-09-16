# shelf – Mini App Platform

## Context

A Mac mini (Apple Silicon) is to run as a personal app platform, published as a GitHub product
named **shelf**. Users build their apps outside the platform (GitHub Actions) and deliver images
plus a deploy artifact to a registry. The platform watches the artifact and deploys
automatically. Apps are reachable through Cloudflare Tunnel at `<app>.<domain>`.
Learning project for platform engineering, not for production use.

The repository directory is empty — greenfield start.

This plan is the validated version of an earlier draft. The technically risky assumptions were
checked against sources (see "Validated assumptions"), open decisions were settled with the
maintainer, and several gaps were closed (GitOps drift during app registration, missing
external-dns target annotation, non-working reference example).

**Ordering:** installation on the Mac mini moves to the end. Everything is developed and tested
first inside a devcontainer on the MacBook against a local k3d cluster; the mini joins only as
the target machine once the platform works.

**Language:** everything in the repository is English — code, comments, docs, commit messages,
CLI output. Conversation with the maintainer may be German or English. This plan is written in
English because it is committed to the repo as `docs/plan.md`.

## Decisions

| Question | Decision |
|---|---|
| Rendering | **Helm chart, rendered in-cluster** via `HelmRelease` — no kro |
| Generated secrets | **CLI generates + host backup** at `~/.shelf/apps/<app>/secrets.yaml` (0600) |
| GHCR pulls | **Classic PAT** with `read:packages`, used as `imagePullSecret` |
| arm64 builds | **GitHub runner `ubuntu-24.04-arm`** (available in private repos since 01/2026) |
| Ordering | **Mac mini installation last**, base platform first |
| Dev cluster | **k3d on the Docker Desktop engine** — no local Colima, no Docker Desktop Kubernetes |
| Dev environment | **Devcontainer + Docker-outside-of-Docker**, from Phase 0 — nothing installed on the Mac |
| Language | **English** for all repo content; `CLAUDE.md` records the working agreements |

Decided during planning (deviations from the original draft, stated explicitly):

- API group `shelf.dev/v1alpha1`, CLI `shelf`, system namespace `shelf-system`,
  env prefix `SHELF_SECRET_`, OCI annotations under `dev.shelf.*`
- App namespace is `<app>`, **not** `<app>-main` — per-branch environments are not in the MVP,
  and the suffix would suggest a capability that does not exist
- **No `registries.yaml`** — one `imagePullSecret` per namespace covers images *and* the Flux
  `OCIRepository` with a single credential path, and survives a cluster rebuild
- `resources` and `health` move **into the MVP** (rationale under Schema)
- Reference app is **Postgres**, not MongoDB with a replica set
- **CI**: level-1 tests from Phase 1; k3d cluster tests from Phase 4

## Validated assumptions

These close three of the four original spikes:

1. **`$(VAR)` expansion with secret values works.** In `makeEnvironmentVariables`
   ([kubelet_pods.go](https://github.com/kubernetes/kubernetes/blob/release-1.33/pkg/kubelet/kubelet_pods.go))
   *every* resolved variable is stored in `tmpEnv` — including those from
   `valueFrom.secretKeyRef` and from `envFrom`. Later `value:` entries expand against it.
   `ExpandContainerCommandAndArgs` uses the fully resolved env list for `args`/`command`.
   The `SHELF_SECRET_<NAME>` approach therefore works in both places. The frequently cited
   counterexample (Azure/AKS#836) only shows that `kubectl describe` prints the spec.
2. **Colima**: `--kubernetes-disable traefik --kubernetes-disable servicelb`.
   `--k3s-arg=--disable=…` is unreliable according to abiosoft/colima#1222.
3. **external-dns + tunnel**: without servicelb, Traefik has no LB address and external-dns
   would have nothing to publish. The Ingress must carry
   `external-dns.alpha.kubernetes.io/target: <tunnel-uuid>.cfargotunnel.com` and
   `external-dns.alpha.kubernetes.io/cloudflare-proxied: "true"`.
4. **GHCR fine-grained PATs** still lack package-read permission for Docker pulls
   (community discussion #177617, open as of January 2026) → classic PAT.
5. **kro could iterate** (`forEach` since 0.9.x, Simple Schema supports `map[string]MyType`),
   but is not used: nested collections (`volumeClaimTemplates`, env lists with prepended secret
   variables, port lists) plus the Deployment/StatefulSet branch turn into a templating
   language inside CEL strings.

## Development and test environment

### Three levels

**Level 1 — devcontainer, no cluster (seconds).** `go test`, `helm template` against golden
files, `kubeconform`. Covers schema, validation, renderer and most of the chart's correctness.
Most progress happens here. Also runs in CI.

**Level 2 — devcontainer, k3d cluster `shelf-dev` (seconds).** Real k3s — the same distribution
the mini will run — just without exposure. Covers everything that needs a real cluster: PVC
survival, secret expansion in a running pod, Flux reconciliation, HelmRelease upgrade on
ConfigMap change, `shelf app add`/`rm`.
Without a tunnel, ingress is checked via `kubectl port-forward svc/traefik 8080:80` and
`curl -H "Host: hello.dev.local" localhost:8080` — this fully exercises the Traefik rules;
Cloudflare only adds DNS and TLS on top.

**Level 3 — Mac mini over SSH (Phase 6 onward).** Only what level 2 cannot do: host preflight on
real hardware, Cloudflare Tunnel against the real domain, first run on an untouched machine.

### Design consequence: `shelf init` is split into layers

For level 2 to be possible at all, the installer is split into individually callable,
idempotent steps:

```
shelf init host     # brew, pmset, Colima profile     → target Mac only (Phase 6)
shelf init cluster  # Flux Operator, platform charts  → against the current kubecontext
shelf init expose   # Cloudflare Tunnel, cloudflared, external-dns
shelf init          # wrapper around all three
```

`shelf init cluster` must contain **no Colima assumptions** and only use the kubecontext.
In development, `cluster` runs (and from Phase 5 optionally `expose`); `host` never does.

### Devcontainer with Docker-outside-of-Docker

Nothing is installed on the Mac beyond what is already there: Docker Desktop and VS Code with
the Dev Containers extension. The entire toolchain lives versioned in the repo.

**What DooD isolates and what it does not.** The devcontainer gets the host daemon's socket.
Isolated are the *toolchain* (Go, k3d, kubectl, helm, flux, just, kubeconform — pinned in the
image) and the *kubeconfig* (lives in the container; the Mac's `~/.kube/config` is not mounted).
Docker itself is **not** isolated: k3d containers are siblings of the existing Docker Desktop
containers, sharing daemon, image store and RAM budget. Isolation there comes from fixed names
and complete cleanup (see footprint below).

`.devcontainer/devcontainer.json` sets a fixed container name (`shelf-devcontainer`, needed
for `docker network connect` and exact-name cleanup), the DooD feature with `"moby": false`
(its default `moby-cli` has no packages for Debian trixie; the Docker CE CLI does), `KUBECONFIG` and
`LOCAL_WORKSPACE_FOLDER`, three named volumes, port 8080 forwarding, and the Go, YAML and
Claude Code extensions. The Claude Code extension and a volume for `~/.claude` keep the
assistant available and logged in across container rebuilds.

`.devcontainer/Dockerfile` is the single source of truth for versions. It downloads tool
binaries at `ARG`-pinned versions and selects the architecture via
`dpkg --print-architecture` — `arm64` on the M2 and in CI. Go module and build caches live in
named volumes because the workspace bind mount over VirtioFS is noticeably slower for builds.
The Dockerfile pre-creates and chowns every volume mount point: a new named volume copies
ownership from the image directory, and without it the volumes would be root-owned.

Pinned versions (September 2026):

| Tool | Version | Note |
|---|---|---|
| Base image | `mcr.microsoft.com/devcontainers/go:2-1.27-trixie` | Go 1.27 |
| k3s | `rancher/k3s:v1.36.4-k3s1` | k3s stable channel |
| kubectl | 1.36.4 | follows the k3s minor version |
| k3d | 5.9.0 | |
| Helm | 4.3.0 | Helm 4 — major version bump since the original draft |
| Flux CLI | 2.9.5 | |
| just | 1.58.0 | |
| kubeconform | 0.8.0 | |

**Pitfall 1 — path translation.** Every bind mount that k3d or `docker run` passes on is
resolved by the *host* daemon. Inside the container `$PWD` is `/workspaces/shelf`, which does
not exist on the Mac; Docker then silently creates an empty directory. Rule: **no host bind
mounts in k3d.** If one is needed, the host path comes from `$LOCAL_WORKSPACE_FOLDER` (the
approach documented by the DooD feature). Every `hack/` script aborts through
`require_devcontainer` if the variable is missing, if `KUBECONFIG` is not the dev kubeconfig,
or if the shell is not running in `shelf-devcontainer`.

**Pitfall 2 — API server reachability.** k3d publishes the API server on the host's
`127.0.0.1`. Inside the devcontainer `127.0.0.1` is its own loopback — `kubectl` would find
nothing. Fix: the devcontainer joins the k3d network and addresses the server container by name
through Docker's embedded DNS. The container name is added explicitly as a TLS SAN so
verification passes without `insecure-skip-tls-verify`. (Microsoft's reference devcontainer for
k3d sidesteps this with DinD and `--privileged`; with DooD this route is required.)

### Dev cluster: `just cluster-up`

`hack/cluster-up.sh`, idempotent:

```
k3d cluster create shelf-dev \
  --image "$SHELF_K3S_IMAGE" \
  --k3s-arg "--disable=traefik@server:*" \
  --k3s-arg "--tls-san=k3d-shelf-dev-server-0@server:*" \
  --no-lb --api-port 127.0.0.1:6445 \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false

docker network connect k3d-shelf-dev shelf-devcontainer

k3d kubeconfig get shelf-dev > "$KUBECONFIG"
kubectl config set-cluster k3d-shelf-dev --server=https://k3d-shelf-dev-server-0:6443
```

If the cluster exists it is started instead of created, and an existing network attachment is
accepted. If the server name does not resolve inside the devcontainer, the script falls back to
the server's IP on the k3d network and sets `tls-server-name` to the container name, so TLS is
still verified against the SAN. Phase 0 step 3 shows which path is taken.

`network connect` must be repeated after every rebuild (new network), so everything lives in one
script. `just cluster-down` (`hack/cluster-down.sh`) **disconnects the devcontainer first** and
then runs `k3d cluster delete shelf-dev`: Docker refuses to remove a network that still has
endpoints, so k3d would otherwise leave `k3d-shelf-dev` behind.

For the ingress test, `kubectl port-forward svc/traefik 8080:80` runs in the devcontainer; VS
Code forwards port 8080 to the Mac, so `curl -H "Host: hello.dev.local" localhost:8080` also
works from the Mac.

**Why not Docker Desktop's built-in Kubernetes:** it is kubeadm-based (recently optionally kind)
with a `hostpath` StorageClass — a different distribution from the k3s on the mini. Differences
in StorageClass name, `WaitForFirstConsumer` binding and k3s defaults would only surface on the
mini, and a working LoadBalancer there would mask dependencies that do not exist on the mini.

**Why not Colima locally:** k3d provides the same k3s, creates and discards clusters in 20–30 s
instead of minutes, needs no second VM, and is exactly what runs in CI from Phase 4 — one
cluster setup instead of two. Colima is only needed on the mini (Phase 6), because Docker
Desktop is a GUI app that needs a logged-in session and is the wrong choice for a machine
operated over SSH.

### Footprint on the existing Docker Desktop setup

Neither the devcontainer nor k3d touches Docker Desktop settings, daemon configuration, existing
containers/images/volumes/networks, or `/etc/hosts` on the Mac. The kubeconfig lives only in the
devcontainer; the `--kubeconfig-*=false` flags additionally stop k3d from touching any default
kubeconfig. A misconfigured `shelf init cluster` therefore cannot hit `docker-desktop`, and later
cannot hit the mini.

Complete inventory of what gets created:

| Object | Name | Created by | Removed by `hack/nuke.sh` |
|---|---|---|---|
| Image | `vsc-shelf-…` (generated name) | devcontainer build | yes, resolved via the container |
| Container | `shelf-devcontainer` | devcontainer | yes |
| Volumes | `shelf-gomodcache`, `shelf-gocache`, `shelf-claude-config` | devcontainer | yes |
| Image | `mcr.microsoft.com/devcontainers/go:2-1.27-trixie` | devcontainer build | only with `--with-shared-images` |
| Image | `rancher/k3s:v1.36.4-k3s1` (~250 MB) | k3d | only with `--with-shared-images` |
| Image | `ghcr.io/k3d-io/k3d-tools:5.9.0` | k3d | only with `--with-shared-images` |
| Container | `k3d-shelf-dev-server-0` | k3d | yes |
| Container | `k3d-shelf-dev-tools` (runs as long as the cluster exists) | k3d | yes, if left over |
| Network | `k3d-shelf-dev` | k3d | yes |
| Volume | `k3d-shelf-dev-images` | k3d | yes |

Workload images inside the cluster (e.g. `busybox` for smoke tests) are pulled by k3s's own
containerd inside the node container, not into the host's image store; they disappear with the
cluster.

`hack/nuke.sh` is a POSIX `sh` script run **from the Mac** — only `docker` is needed, and `just`
is not installed there. It also removes the devcontainer itself. It lists what it found and asks
for confirmation (`--yes` skips that). Deleting by prefix match or with `docker … prune` is
forbidden — the script names every object explicitly, so a foreign container that happens to
start with `shelf-` is never caught. The devcontainer image has a generated name and is resolved
through the container, never by name; if the container is already gone, matching images are
only listed.

Shared third-party images are kept by default because other projects may use them; the baseline
taken on 2026-09-16 shows none of them were present on this Mac, so `--with-shared-images` is
safe here.

Deliberately left in place, and reported by the script:

- The **`vscode` volume**, which VS Code shares across all devcontainers and which already
  existed on this Mac.
- **Build cache** from the devcontainer build. It cannot be removed selectively without `prune`,
  and Docker's build cache garbage collection keeps it bounded.
- **Untagged intermediate images** from the build, if any. They cannot be attributed to shelf
  reliably, so they are only pointed out.

Three pitfalls:

- **Do not set `--disable=servicelb`.** k3d issues #742 and #1241: cluster creation then hangs
  waiting for k3d's own load balancer. Use `--no-lb` instead and never use servicelb. Parity
  with the mini comes from the chart: Traefik pinned to `ClusterIP`, plus a golden-file test
  asserting that `type: LoadBalancer` is never rendered.
- **local-path data lives inside the node container.** It survives pod restarts — exactly what
  Phase 2 tests — but not `k3d cluster delete`. If that is ever wanted:
  `--volume "$LOCAL_WORKSPACE_FOLDER/.k3d-storage:/var/lib/rancher/k3s/storage@all"` —
  **not** `$PWD`, see pitfall 1.
- **The Docker Desktop VM needs headroom.** A k3s node plus Flux, Traefik and test apps wants
  ~3–4 GB *inside* the VM. If the VM is capped at 4 GB, other containers come under pressure and
  the OOM killer picks victims. Before Phase 0, check Settings → Resources for ~8 GB (no issue
  with 24 GB on the host), and run `just cluster-stop` when not working on it.
  Disabling Docker Desktop's built-in Kubernetes saves another ~2 GB — optional, both coexist.

The throwaway reset (`just cluster-down && just cluster-up`) is the metric Phase 3 optimizes for.

### Mac mini access (Phase 6 onward)

Almost nothing runs over SSH — instead the API server is brought into the devcontainer:

```
# .devcontainer/ssh_config (template, HostName adjustable locally)
Host mini
  HostName mini.local
  ControlMaster auto
  ControlPath ~/.ssh/cm-%r@%h:%p
  ControlPersist 10m

ssh -N -f -L 6444:127.0.0.1:6443 mini
```

Colima binds the k3s API to `127.0.0.1:6443` on the mini. The forward runs in the devcontainer;
the k3s server certificate has `127.0.0.1` as an IP SAN and the port is irrelevant for
verification, so TLS works without `insecure-skip-tls-verify`. The mini's kubeconfig is a
separate file `~/.kube/shelf-mini.yaml` in the container; switching happens only explicitly via
`KUBECONFIG`, never via a context switch in a shared file. `kubectl`, `helm`, `flux`, `stern`
then run from the devcontainer against the mini. Only `shelf init host` and `colima` remain for
SSH.

VS Code forwards the Mac's SSH agent into the devcontainer automatically, so keys never leave the
Mac. The Mac's `~/.ssh/config` is **not** mounted — hence the template in the repo.

The binary is copied rather than syncing source. **Caution:** the devcontainer is
`linux/arm64`, the mini is `darwin/arm64`. A plain `go build` produces a Linux binary that fails
on the mini with "exec format error". `just push-mini` therefore builds explicitly with
`GOOS=darwin GOARCH=arm64` and copies via `scp`.

## Architecture

### Runtime (Mac mini, Phase 6 onward)
Colima profile `shelf`:
`--vm-type vz --vz-rosetta --runtime containerd --kubernetes
 --kubernetes-disable traefik --kubernetes-disable servicelb`.
Storage: local-path on the VM disk.
In development, k3d plays the same role — see the development environment above.

> **local-path does not enforce capacity.** The `size` field in the schema is documentation,
> not a limit — a pod can fill the VM disk. This must be stated in the docs.

### Cluster components
| Concern | Building block |
|---|---|
| GitOps | Flux, installed via Flux Operator (`FluxInstance`) |
| App rendering | Helm chart `shelf-app`, rendered by helm-controller |
| App registration | Flux Operator `ResourceSet` + `ResourceSetInputProvider` |
| Ingress | Traefik (Helm, ClusterIP — no LoadBalancer) |
| Exposure | `cloudflared` in-cluster, one catch-all rule pointing at Traefik |
| DNS | external-dns, Cloudflare provider |

Platform components are installed by Flux from an OCI artifact of the shelf release.

**cloudflared configuration**: locally managed tunnel with exactly one rule,
`service: http://traefik.traefik.svc.cluster.local:80`. Cloudflare is then *only* DNS per app;
the tunnel is never touched again after setup. Host routing is done entirely by Traefik based on
the forwarded `Host` header.

## Rendering pipeline

```
CI:   app.yaml ──shelf render──► resolved app.yaml ──► OCI artifact (ConfigMap manifest)
                                                              │
Cluster:                                                      ▼
   ResourceSetInputProvider (per app, from CLI)    OCIRepository ──► Kustomization
                    │                                                     │
                    ▼                                                     ▼
              ResourceSet ──────────────► HelmRelease           ConfigMap <app>-values
                                               │  valuesFrom ◄──────────┘
                                               ▼
                                  chart shelf-app (OCI, version pinned by the platform)
                                               │
                                               ▼
                      Deployment / StatefulSet / Service / Ingress / PVC
```

The chart version belongs to the platform, the values belong to the app. Rendering bugs can be
fixed centrally without tenants rebuilding. `helm-controller` watches ConfigMaps referenced in
`valuesFrom` — the ConfigMap must be generated with `disableNameSuffixHash: true` so its name
stays stable.

## Handover contract: deploy artifact

For every build, the tenant workflow pushes:

1. Images for `linux/arm64` to GHCR
2. An OCI artifact `ghcr.io/<owner>/<app>-deploy`
   - Content: a ConfigMap manifest holding the resolved `app.yaml` under the key `app.yaml`
     - images pinned by digest
     - `${<component>.host}` / `${<component>.port}` / `${<component>.ports.<name>}` substituted
     - `${secrets.*}` left unresolved (resolved by the chart)
   - Tags: moving `main`, immutable `sha-<shortsha>`
   - Annotations: `org.opencontainers.image.{source,revision,created}`, custom ones under
     `dev.shelf.*`
3. A call to the Flux webhook receiver (from Phase 4; before that, `OCIRepository` polling every
   1m is sufficient)

Unknown `apiVersion` → the artifact is rejected.

## Schema `shelf.dev/v1alpha1`

```yaml
apiVersion: shelf.dev/v1alpha1
name: shop

components:
  web:
    image: ghcr.io/<owner>/shop-web@sha256:…
    port: 8080
    route: /
    instances: 2
    health: { path: /healthz }
    resources: { cpu: 500m, memory: 512Mi }

  api:
    image: ghcr.io/<owner>/shop-api@sha256:…
    port: 3000
    route: /api
    env:
      DATABASE_URL: postgres://app:${secrets.db-password}@${db.host}:${db.port}/shop
    volumes:
      avatars: { path: /data/avatars, size: 20Gi }

  db:
    image: postgres:16
    port: 5432
    env:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: ${secrets.db-password}
      POSTGRES_DB: shop
    volumes:
      data: { path: /var/lib/postgresql/data, size: 20Gi }

secrets:
  db-password: { generate: true }
```

### Fields
- `image`, `command`, `args`, `env` — as in Kubernetes
- `port` (single port) or `ports` (map `name → number`)
- `route`: short form `route: /path`; with multiple ports
  `route: { path: /path, port: <name>, stripPrefix: false }`
- `instances`: default 1
- `volumes`: map `name → { path, size }`
- `health`: `{ path, port?, initialDelay? }` → readiness and liveness probe
- `resources`: `{ cpu, memory }` → `requests`; `limits` only for memory
- Top-level `secrets`: map `name → { generate: true }`

**Why `health` and `resources` are in the MVP:** on a single-node mini, an app without a memory
limit can take down the whole cluster. And without a readiness probe, Flux `wait: true` reports
"ready" too early — the auto-deploy chain loses its most important signal. Both are optional with
conservative defaults (`requests: 50m/64Mi`, no limit unless specified).

### Semantics
- Without `volumes` → Deployment, rolling update
- With `volumes` → StatefulSet, one `volumeClaimTemplate` per volume, plus a headless Service
  (`db-0.db` addressable);
  `persistentVolumeClaimRetentionPolicy: { whenScaled: Retain, whenDeleted: Delete }`;
  PVC name derives from the volume name, not the path
- Every component with a port gets a Service named after it
- Components with a `route` are externally reachable, all others internal only
- `instances` does not form clusters — that remains the app's job
- Generated secrets are created once per app and stay stable
- `route` passes the path through unchanged; a Traefik `Middleware` only with `stripPrefix: true`

### Secret embedding
For each referenced secret, the chart renders a variable `SHELF_SECRET_<NAME>` via
`secretKeyRef` **before** all other env entries and rewrites `${secrets.name}` to
`$(SHELF_SECRET_<NAME>)`. No secret values appear in manifests.

Two pitfalls the renderer must handle:
- A literal `$` in user `env`/`args` must be escaped to `$$`, otherwise kubelet consumes `$(…)`
- Generated passwords must be **URL-safe** (no ``@ : / ? # [ ] ``), otherwise every connection
  URI breaks

### Validation (`shelf validate`, in CLI and CI)
Errors: `route` without `port`/`ports`; multiple `ports` and `route` without a port;
`${x.port}` on a component with multiple ports; reference to an unknown component/port/secret;
reserved names `secrets`, `app`, `shelf`; duplicate volume names; overlapping paths; colliding
`route` paths across components.

Warnings: `port` differs from `EXPOSE` in the image; changed volume definition of an existing
component (`volumeClaimTemplates` are immutable, local-path cannot grow).

## App registration without GitOps drift

The `ResourceSet` is platform code managed by Flux — CLI edits to it would be reverted on every
reconciliation. Instead:

- `shelf app add <name> <artifact-url>` creates a **`ResourceSetInputProvider`**
  (`fluxcd.controlplane.io/v1`, `spec.type: Static`) in `shelf-system`, labelled
  `shelf.dev/app`, plus the generated secret and its host backup
- The platform `ResourceSet` collects all providers via `spec.inputsFrom[].selector.matchLabels`
  and generates per app: Namespace, `imagePullSecret`, `OCIRepository`, `Kustomization`
  (`targetNamespace`, `prune: true`, `wait: true`), `HelmRelease`, and later the `Receiver`
- Removing the provider lets ResourceSet garbage collection clean up the objects

In the devcontainer, the host backup `~/.shelf/` lives in the ephemeral container home and
disappears on rebuild. That is intended: the dev cluster is just as ephemeral, so backup and
cluster live and die together. On the mini, `~/.shelf/` is the real home directory.

## CLI `shelf` (Go)

- `shelf validate <app.yaml>`
- `shelf render <app.yaml>` — resolved file, PVC names, secret *names*; values only with `--reveal`
- `shelf init cluster` / `shelf init expose` / `shelf init host` / `shelf init`
- `shelf app add <name> <artifact-url>` / `shelf app rm <name>`
- `shelf doctor` — preflight plus runtime (VM, tunnel, Flux status), reporting per layer
- `shelf destroy` — remove profile, tunnel and DNS records

## Repository layout

```
shelf/
  CLAUDE.md                   working agreements for Claude (see below)
  .devcontainer/
    devcontainer.json         DooD, volumes, remoteEnv
    Dockerfile                Go base + pinned tool binaries (single source of versions)
    ssh_config                template for mini access (Phase 6)
  hack/
    lib.sh                    shared helpers, require_devcontainer guard
    cluster-up.sh             create/start dev cluster, join network, write kubeconfig
    cluster-down.sh           detach and delete dev cluster
    nuke.sh                   host-side cleanup by exact name (POSIX sh)
    smoke/                    Phase 0 smoke tests
  cmd/shelf/                  CLI entry point
  internal/
    schema/                   app.yaml types, JSON Schema generation
    validate/                 validation rules
    render/                   app.yaml → resolved app.yaml → ConfigMap manifest
    preflight/                host checks
    host/                     brew, pmset, colima — behind a command-runner interface
    cloudflare/               tunnel and DNS API
    cluster/                  apply/wait helpers
  charts/shelf-app/           the generic app chart
  platform/                   Flux-managed platform manifests (→ OCI artifact)
    flux/ traefik/ cloudflared/ external-dns/
    resourcesets/app.yaml     the ResourceSet
  .github/workflows/
    build.yml                 reusable tenant workflow
    release.yml               CLI binaries, chart push, platform artifact
  examples/hello/
  schema/app.schema.json
  docs/
    plan.md                   this plan
  justfile
```

## `CLAUDE.md`

[`CLAUDE.md`](../CLAUDE.md) holds the working agreements for Claude: phase-by-phase workflow,
language rule, guardrails, the command table, safety rules and naming conventions. It is
deliberately short and points here for design detail. It also carries a short status section,
because a new Claude session inside the devcontainer has no memory of earlier conversations.
It is updated at the end of every phase; the command table grows with it (`just test` in
Phase 1, `just push-mini` in Phase 6).

## Phases

Stop after each phase, show the result, wait for approval.
Phases 0–5 run entirely in the devcontainer on the MacBook; the mini joins in Phase 6.

### Phase 0 – Devcontainer, dev cluster, smoke tests
Precondition on the Mac: check the Docker Desktop VM for ~8 GB; record a baseline with
`docker ps -a`, `docker network ls`, `docker volume ls`, `docker images`.

The baseline is stored in `.local/baseline/` (git-ignored): containers, networks, volumes,
images, `docker system df`, and a SHA-256 of `~/.kube/config`. Taken 2026-09-16; Docker Desktop
28.1.1 with 17.5 GB for the VM.

1. `git init`; `CLAUDE.md`; `docs/plan.md` (this plan); `.devcontainer/` (JSON, Dockerfile);
   `justfile`; `hack/` scripts including `nuke.sh` and the smoke tests
2. Open the devcontainer ("Reopen in Container"); check `go version`, `k3d version`,
   `kubectl version --client`, `helm version`, `flux version --client`, `just --version`,
   `kubeconform -v`; check that `/go/pkg/mod` and `~/.claude` are owned by `vscode`
3. `just cluster-up`; verify Traefik is absent, note whether the API server was reached by
   name or by IP fallback, and that `kubectl` answers without TLS errors
4. `just smoke-secrets`: `$(VAR)` from `secretKeyRef` in `env` and `args`, `$$` escaping, no
   secret value in the pod spec (confirms the source analysis on a live system)
5. `just smoke-registry ghcr.io/<owner>/<image>:<tag>` with a classic PAT
6. `just cluster-reset`, timed
7. `hack/nuke.sh --with-shared-images` from the Mac; repeat the baseline and compare

Results (2026-09-16):

- Step 2: all tools at the pinned versions (Docker CLI 29.8.1 against daemon 28.1.1); the
  volume mount points are owned by `vscode`. `k3d version` reports k3s v1.35.5 as its default —
  irrelevant, because `cluster-up.sh` always passes `SHELF_K3S_IMAGE`.
- Step 3: cluster up in 14 s; the API server is reached **by name** through Docker DNS, no IP
  fallback needed; no Traefik; `kubectl` verifies TLS; StorageClass `local-path` with
  `WaitForFirstConsumer`.
- Step 4: green after two fixes to the test itself. (a) The expected values lived in the
  container's `args`, so the secret value was always in the pod spec and the check could never
  pass; they are now assembled from two parts. (b) The `$$` check could never fail: kubelet also
  expands `args`, so both sides of the comparison were unescaped alike, and the escaped
  reference pointed at an undefined variable, which kubelet leaves alone anyway. It now
  references the defined `SHELF_SECRET_PASSWORD`, builds the expected value without `$$`/`$(`,
  and checks that the spec keeps the `$$` form. Verified negatively: without `$$` the test fails.
- Step 5: green with a classic PAT against the private image
  one of the maintainer's private GHCR images. Negative check: the same pod without
  `imagePullSecrets` fails with `401 Unauthorized`, so the image is really private.
- Step 6: `just cluster-reset` in 13 s.
- Step 7: pending (run on the Mac).

**Acceptance:** all tools at the pinned versions; both smoke tests green;
`just cluster-reset` in under a minute; after `hack/nuke.sh` the baseline is identical to
before, apart from build cache entries; the Mac's `~/.kube/config` hash is unchanged.

### Phase 1 – Scaffold, schema, renderer
Repository layout, JSON Schema for `app.yaml` (editor autocomplete), `shelf validate`,
`shelf render`, `just test`, CI with `go test` + `kubeconform`.
**Acceptance:** the example validates, every error case has a test, `$` escaping and URL-safe
password generation are tested.

### Phase 2 – Chart `shelf-app`
Deployment vs. StatefulSet, Services, headless Services, Ingress, PVCs, secret env prefixing,
probes, resources. `helm template` golden files in CI.
**Acceptance:** the example `app.yaml` installed via `helm install` in the dev cluster; Postgres
data survives `kubectl delete pod`; the API reaches the DB through the generated `DATABASE_URL`;
`kubectl exec … printenv` shows the expanded value, `kubectl get deploy -o yaml` does not.

### Phase 3 – `shelf init cluster`
Flux Operator, `FluxInstance`, Traefik as ClusterIP, platform OCI artifact, all against the
current kubecontext. No Colima assumptions in the code.
**Acceptance:** runs in one command on a freshly reset dev cluster, twice in a row without
changes; example app reachable via `port-forward` + `Host` header.

### Phase 4 – Delivery
Deploy artifact format, `ResourceSet` + `ResourceSetInputProvider`, `shelf app add`/`rm`,
Flux `Receiver`, reusable workflow. From here, level-2 tests also run in CI
(`ubuntu-24.04-arm`) — the same k3d as locally, so Flux and ResourceSet regressions surface in
PRs.
**Acceptance:** a push to an example repo → the app in the dev cluster updates without manual
intervention; `shelf app rm` cleans up completely.

### Phase 5 – `shelf init expose`
Cloudflare Tunnel via API, cloudflared with a catch-all rule, external-dns with `target` and
`cloudflare-proxied` annotations. Tested **from the dev cluster** against a test domain — the
mini is not needed for this.
**Acceptance:** the example app is reachable from outside over HTTPS from the dev cluster;
`dig` shows a CNAME to `<uuid>.cfargotunnel.com`.

### Phase 6 – Mac mini
`shelf init host`: preflight (Apple Silicon, RAM, disk, macOS version, Rosetta, Homebrew, tool
versions, existing profile, energy settings), tool installation, disable sleep, Colima profile.
Plus `shelf doctor`, `shelf destroy`, the `shelf init` wrapper, `just push-mini`, and the SSH
setup above.
Since the devcontainer is Linux, `internal/host` cannot run there for real: it is tested with a
fake command runner (expected `brew`/`pmset`/`colima` calls and their order) and executed for
real only on the mini.
**Acceptance:** one command on the mini brings the platform up; `shelf destroy` followed by
`shelf init` works; kubectl from the devcontainer through the SSH forward.

### Phase 7 – Reference apps
`examples/hello`, then the maintainer's own apps as the first real tenant, shop after that.

## Open items

Due in Phase 5:
- **Test DNS isolation**: separate Cloudflare zone or a subdomain of the same zone? With the same
  zone, dev and prod instances strictly need their own `--txt-owner-id` and `--domain-filter`,
  otherwise each treats the other's records as orphaned and deletes them.

Due in Phase 6:
- **Mini hardware** (chip, RAM, macOS version) determines Colima sizing defaults and whether the
  "vanilla Mac" first run can be tested in a `tart` VM. Apple's Virtualization framework only
  supports nested virtualization from M3 and macOS 15; on M1/M2 the substitute is a tested
  `shelf destroy --purge` ("back to vanilla" instead of "start from vanilla").

Later:
- MongoDB with auth + replica set needs a keyfile → the schema would need secrets as file mounts
- Build via `path`, CronJobs, init/migration jobs, `${app.url}`, secret rotation

## Explicitly not in the MVP

Per-branch environments, scale-to-zero, supply-chain security (signatures, scans, policies),
monitoring, own registry, backups, seed data, devcontainer integration *for tenant apps* (the
devcontainer for shelf itself is part of Phase 0), overview page, autostart after power loss,
catalog of managed services.
