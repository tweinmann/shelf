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
| Dev environment | **Devcontainer + Docker-in-Docker** (Docker-outside-of-Docker until Phase 0b) — nothing installed on the Mac |
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

Decided in Phase 1 (2026-09-16):

| Question | Decision |
|---|---|
| Digest pinning | **`shelf render` resolves tags** through the registry (go-containerregistry, Docker keychain). Tests use a fake resolver and an in-memory registry. The same lookup requires a `linux/arm64` variant and feeds the `EXPOSE` warning. |
| Literal `$` | **docker compose rules**: `${…}` is a reference, `$$` is a literal `$`, any other `$` is literal too (`$HOME` works in shell commands) |
| CI | **Inside the devcontainer image** (`devcontainers/ci`, `ubuntu-24.04-arm`), running `just test`; tool versions stay in the Dockerfile only |

Decided in Phase 2 (2026-09-17):

| Question | Decision |
|---|---|
| Workload kind | **Deployment without volumes, StatefulSet with volumes**, as planned. "Always StatefulSet" was considered and rejected: a StatefulSet replaces pods delete-first (every deploy of a single-instance component is a short outage), and after a broken rollout it waits for a manual pod delete ("forced rollback"), which breaks the auto-deploy chain. |
| Services | **`<component>` is always a ClusterIP Service**, for Deployments and StatefulSets alike; a StatefulSet additionally gets **`<component>-headless`** as its `serviceName`. Adding or removing volumes therefore never touches `${<component>.host}`, and the upgrade does not fail on the immutable `clusterIP`. Component names must not end with `-headless`. |
| Secret object | **`shelf-secrets`** in the app namespace, one key per secret name. The chart only references it; `shelf app add` creates it (Phase 4). |
| Ingress | **One Ingress per routed component**, all on `<app>.<domain>`; Traefik attaches middlewares per Ingress, so `stripPrefix` stays per route. The Ingress is Traefik's routing table and external-dns's source, so it is needed despite the tunnel. |
| Platform values | **`platform` block** next to the app values: `domain`, `ingressClassName` (default `traefik`), `imagePullSecret`. The ResourceSet sets it inline in the HelmRelease (Phase 4); later additions such as the external-dns target go here too. |

Decided in Phase 3 (2026-09-17):

| Question | Decision |
|---|---|
| Installation path | **Flux Operator + `FluxInstance` with `spec.sync`** pointing at the platform artifact. The operator creates the `OCIRepository` and `Kustomization` `flux-system`; shelf adjusts them only through `spec.kustomize.patches` (`wait: true`, and `insecure: true` for the dev registry, since `spec.sync` has no such field). |
| Flux Operator install | **Official `install.yaml` embedded** in the binary (`go:embed`), applied with server-side apply. No helm on the target, no download at runtime; upgrading the operator means a new shelf build (`just flux-operator-update`). |
| API access | **client-go in Go** (dynamic client, server-side apply, field manager `shelf`); no kubectl or flux on the target. |
| Safety | `shelf init cluster` **shows context, API server and platform and asks** `Proceed? [y/N]`; `--yes` skips; `--context`/`--kubeconfig` pick the target explicitly. |
| Dev artifact | **Local k3d registry** `shelf-registry`, created with the cluster. `just platform-push` pushes `platform/` as tag `dev`; in the cluster it is `oci://shelf-registry:5000/shelf/platform:dev`. Releases use `oci://ghcr.io/tweinmann/shelf/platform:<version>`, the default of `--platform`. |

Decided in Phase 4 (2026-09-17):

| Question | Decision |
|---|---|
| Example tenant | **Separate repo `tweinmann/shelf-hello`**, using `tweinmann/shelf/.github/workflows/build.yml@main` and building its own image, exactly like a real tenant. Its files are kept in `examples/tenant/`. |
| shelf visibility | **The shelf repo becomes public**, so other repos can call the reusable workflow and `go install` shelf without a token. |
| GHCR credential | **One classic PAT for the platform.** `shelf init cluster` stores it in `shelf-system`; the ResourceSet copies it into every app namespace (`copyFrom`) for images and the deploy artifact. |
| shelf in tenant CI | **Built from source** (`go install …@<ref>`) in the reusable workflow; release binaries come later. |
| Webhook receiver | **Moved to Phase 5**, where the tunnel makes it reachable and testable. Phase 4 relies on `OCIRepository` polling every minute. |

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

### Devcontainer with Docker-in-Docker

Nothing is installed on the Mac beyond what is already there: Docker Desktop and VS Code with
the Dev Containers extension. The entire toolchain lives versioned in the repo.

**What DinD isolates.** The devcontainer runs its own Docker daemon (the `docker-in-docker`
feature), and the k3d cluster lives in that daemon. Isolated are the *toolchain* (Go, k3d,
kubectl, helm, flux, just, kubeconform — pinned in the image), the *kubeconfig* (lives in the
container; the Mac's `~/.kube/config` is not mounted) and *Docker itself*: k3s images, cluster
containers and the cluster network never appear on the host daemon, and `docker` inside the
container cannot see or touch the maintainer's other containers. Shared with the host remain the
RAM budget of the Docker Desktop VM and the few objects in the footprint below.

The feature makes the container **privileged**, i.e. root in the Docker Desktop VM (not on the
Mac). That is no real step down from the earlier Docker-outside-of-Docker setup: access to the
host's Docker socket was already root-equivalent in the same VM.

The switch from Docker-outside-of-Docker (Phase 0) happened on 2026-09-17 (see Phase 0b). It
removed two workarounds the shared daemon needed: bind-mount paths had to be translated to host
paths via `$LOCAL_WORKSPACE_FOLDER`, and the API server, published on the host's loopback, had
to be reached by joining the k3d network, with an extra TLS SAN and an IP fallback.

`.devcontainer/devcontainer.json` sets a fixed container name (`shelf-devcontainer`, for
exact-name cleanup), the `docker-in-docker` feature with `"moby": false` (moby has no packages
for Debian trixie; Docker CE does) and Docker pinned to 29.8.1, `KUBECONFIG` and the marker
`SHELF_DEVCONTAINER=1`, five named volumes, port 8080 forwarding, and the Go, YAML and Claude
Code extensions. The Claude Code extension and a volume for `~/.claude` keep the assistant
available and logged in across container rebuilds. The feature brings its own volumes for
`/var/lib/docker` and `/var/lib/containerd` with generated names; `devcontainer.json` mounts
`shelf-docker` and `shelf-containerd` on the same targets instead, so the names are fixed.

`.devcontainer/Dockerfile` is the single source of truth for tool versions. It downloads tool
binaries at `ARG`-pinned versions and selects the architecture via
`dpkg --print-architecture` — `arm64` on the M2 and in CI. Go module and build caches live in
named volumes because the workspace bind mount over VirtioFS is noticeably slower for builds.
The Dockerfile pre-creates every volume mount point: a new named volume copies ownership and
mode from the image directory, and without it the volumes would be root-owned.

Pinned versions (September 2026):

| Tool | Version | Note |
|---|---|---|
| Base image | `mcr.microsoft.com/devcontainers/go:2-1.27-trixie` | Go 1.27 |
| Docker engine | 29.8.1 | in `devcontainer.json` (feature option) |
| k3s | `rancher/k3s:v1.36.4-k3s1` | k3s stable channel |
| kubectl | 1.36.4 | follows the k3s minor version |
| k3d | 5.9.0 | |
| Helm | 4.3.0 | Helm 4 — major version bump since the original draft |
| Flux CLI | 2.9.5 | |
| just | 1.58.0 | |
| kubeconform | 0.8.0 | |

Pinned in the platform (Phase 3), not in the Dockerfile:

| Component | Version | Where |
|---|---|---|
| Flux Operator | v0.60.0 | `internal/cluster/manifests/flux-operator.yaml`, `FluxOperatorVersion` |
| Flux | 2.9.5 | `FluxVersion` in `internal/cluster`; matches the Flux CLI |
| Traefik chart | 41.6.0 (Traefik v3.7.13) | `platform/traefik/release.yaml` |

**Guard.** Every `hack/` script calls `require_devcontainer`, which aborts unless
`SHELF_DEVCONTAINER=1`, `KUBECONFIG` is the dev kubeconfig, and `docker info` reports the
container's own host name — a daemon reached through a mounted host socket would report the
Docker Desktop VM instead.

### Dev cluster: `just cluster-up`

`hack/cluster-up.sh`, idempotent:

```
k3d cluster create shelf-dev \
  --image "$SHELF_K3S_IMAGE" \
  --k3s-arg "--disable=traefik@server:*" \
  --no-lb --api-port 127.0.0.1:6445 \
  --registry-create shelf-registry:127.0.0.1:5000 \
  --kubeconfig-update-default=false --kubeconfig-switch-context=false

k3d kubeconfig get shelf-dev > "$KUBECONFIG"
```

If the cluster exists it is started instead of created. The API server is published on the
devcontainer's own loopback, where `kubectl` runs, so the kubeconfig from k3d works unchanged.
The registry `shelf-registry` holds the dev platform, chart and deploy artifacts. Pods reach it
as `shelf-registry:5000` (k3d adds the name to CoreDNS's `NodeHosts`; the entry takes a few
seconds to become resolvable after cluster creation), and so does the devcontainer, where
`cluster-up.sh` maps the name to the published loopback port in `/etc/hosts`. It is plain HTTP
and is deleted together with the cluster, so after `just cluster-reset` run
`just platform-push` again.
`just cluster-down` (`hack/cluster-down.sh`) runs `k3d cluster delete shelf-dev` and removes the
kubeconfig.

Stopping or rebuilding the devcontainer stops the inner daemon and with it the cluster. Its
state survives in the `shelf-docker` volume, and the inner daemon starts the node container
again by itself (k3d sets a restart policy). The kubeconfig does not survive, because the home
directory is not a volume; `just cluster-up` writes it again (about 2 s).

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

Complete inventory of what gets created on the host daemon:

| Object | Name | Created by | Removed by `hack/nuke.sh` |
|---|---|---|---|
| Image | `vsc-shelf-…` (generated name) | devcontainer build | yes, resolved via the container |
| Image | `mcr.microsoft.com/devcontainers/go:2-1.27-trixie` | devcontainer build | only with `--with-shared-images` |
| Container | `shelf-devcontainer` | devcontainer | yes |
| Volumes | `shelf-gomodcache`, `shelf-gocache`, `shelf-claude-config` | devcontainer | yes |
| Volumes | `shelf-docker`, `shelf-containerd` (inner Docker daemon: k3s images, cluster, a few GB) | devcontainer | yes; volumes on these two targets are also resolved via the container, in case the feature used its own generated names |

Everything k3d creates — the k3s and k3d-tools images, the node containers with their anonymous
volumes, the `k3d-shelf-dev` network, the `k3d-shelf-dev-images` volume and the registry
container `shelf-registry` — lives in the inner daemon and therefore inside `shelf-docker`. Workload images (e.g. `busybox` for smoke tests) are
pulled by k3s's own containerd inside the node container.

`hack/nuke.sh` is a POSIX `sh` script run **in a terminal on the Mac** — only `docker` is
needed, and `just` is not installed there. It refuses to run inside a container. It removes the
devcontainer and lists what it found and asks for confirmation (`--yes` skips that). Deleting
by prefix match or with `docker … prune` is forbidden — the script names every object
explicitly, so a foreign container that happens to start with `shelf-` is never caught. The
devcontainer image has a generated name and is resolved through the container, never by name;
if the container is already gone, matching images are only listed.

The shared base image is kept by default because other projects may use it; the baseline taken
on 2026-09-16 shows it was not present on this Mac, so `--with-shared-images` is safe here.

Deliberately left in place, and reported by the script:

- The **`vscode` volume**, which VS Code shares across all devcontainers (mounted at `/vscode`)
  and which already existed on this Mac.
- **Build cache** from the devcontainer build. It cannot be removed selectively without `prune`,
  and Docker's build cache garbage collection keeps it bounded.
- **Untagged intermediate images** from the build, if any. They cannot be attributed to shelf
  reliably, so they are only pointed out. This includes earlier devcontainer images left
  untagged by a rebuild; check `devcontainer.metadata` with `docker image inspect` for the
  `shelf-*` mounts and remove a match by ID (seen in Phase 0b step 7).

Three pitfalls:

- **Do not set `--disable=servicelb`.** k3d issues #742 and #1241: cluster creation then hangs
  waiting for k3d's own load balancer. Use `--no-lb` instead and never use servicelb. Parity
  with the mini comes from the chart: Traefik pinned to `ClusterIP`, plus a golden-file test
  asserting that `type: LoadBalancer` is never rendered.
- **local-path data lives inside the node container.** It survives pod restarts — exactly what
  Phase 2 tests — but not `k3d cluster delete`. If that is ever wanted:
  `--volume "$PWD/.k3d-storage:/var/lib/rancher/k3s/storage@all"` (with DinD, container paths
  are resolved by the inner daemon, so `$PWD` is correct).
- **The Docker Desktop VM needs headroom.** A k3s node plus Flux, Traefik and test apps wants
  ~3–4 GB *inside* the VM. If the VM is capped at 4 GB, other containers come under pressure and
  the OOM killer picks victims. Before Phase 0, check Settings → Resources for ~8 GB (no issue
  with 24 GB on the host), and run `just cluster-stop` or stop the devcontainer when not
  working on it. Disabling Docker Desktop's built-in Kubernetes saves another ~2 GB — optional,
  both coexist.

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

Platform components are installed by Flux from an OCI artifact of the shelf release: the
directory `platform/` (a kustomization), pushed with `flux push artifact`. `shelf init cluster`
installs the Flux Operator and a `FluxInstance` whose `spec.sync` points at that artifact; from
then on Flux keeps the platform in sync. Traefik runs in namespace `traefik` with a ClusterIP
Service on port 80, IngressClass `traefik` (not the default class), the Kubernetes Ingress and
CRD providers, and no `websecure` port, because TLS ends at Cloudflare.

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
- `image`, `command`, `args`, `env` — as in Kubernetes; `env` is a map, and scalar values
  (`PORT: 8080`) are taken as text
- `port` (single port) or `ports` (map `name → number`)
- `route`: short form `route: /path`; with multiple ports
  `route: { path: /path, port: <name>, stripPrefix: false }`
- `instances`: default 1
- `volumes`: map `name → { path, size }`
- `health`: `{ path, port?, initialDelay? }` → readiness and liveness probe; `port` is a port
  name, `initialDelay` is in seconds
- `resources`: `{ cpu, memory }` → `requests`; `limits` only for memory
- Top-level `secrets`: map `name → { generate: true }`

References in `env`, `command` and `args`: `${<component>.host}` (the Service name),
`${<component>.port}` (only for a component with exactly one port),
`${<component>.ports.<name>}`, `${secrets.<name>}`. Anything else in `${…}` is an error.

Name rules: app names are DNS-1123 labels, component names DNS-1035 labels, both at most 40
characters so suffixes (`-values`, StatefulSet pod names, revision hashes) still fit into 63.
Secret names allow no `_`, so `SHELF_SECRET_<NAME>` cannot collide. Env names are C
identifiers, and the prefix `SHELF_SECRET_` is reserved.

### Resolved app.yaml
`shelf render` produces the values file of the `shelf-app` chart. Compared with the input:
images pinned by digest (the tag stays for readability, e.g. `postgres:16@sha256:…`; for
multi-platform images the index digest); host and port references substituted; every literal
`$` escaped as `$$` for kubelet; `${secrets.<name>}` left in place. The shape is normalized so
the chart needs no case analysis: `ports` is always a map (a single `port` becomes
`ports.main`), `route.port` and `health.port` are always set, `instances` is always set.
Because every literal `$` is doubled, the chart finds secret references safely by splitting a
string on `$$` and rewriting `${secrets.x}` in each part.

**Why `health` and `resources` are in the MVP:** on a single-node mini, an app without a memory
limit can take down the whole cluster. And without a readiness probe, Flux `wait: true` reports
"ready" too early — the auto-deploy chain loses its most important signal. Both are optional with
conservative defaults (`requests: 50m/64Mi`, no limit unless specified).

### Semantics
- Without `volumes` → Deployment, rolling update
- With `volumes` → StatefulSet, one `volumeClaimTemplate` per volume, plus a headless Service
  `<component>-headless` (pods addressable as `db-0.db-headless`);
  `persistentVolumeClaimRetentionPolicy: { whenScaled: Retain, whenDeleted: Delete }`;
  PVC name derives from the volume name, not the path
- Every component with a port gets a ClusterIP Service named after it, whether Deployment or
  StatefulSet; adding or removing volumes keeps it unchanged (removing volumes deletes the PVCs)
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
  URI breaks. They come from Go's `crypto/rand.Text`: 26 characters of `A–Z2–7`, 130 bits.

### Validation (`shelf validate`, in CLI and CI)
Errors: `route` without `port`/`ports`; multiple `ports` and `route` without a port;
`${x.port}` on a component with multiple ports; reference to an unknown component/port/secret;
reserved names `secrets`, `app`, `shelf` and the suffix `-headless`; duplicate volume names; overlapping paths; colliding
`route` paths across components.

Also errors: invalid names (see above), `port` together with `ports`, duplicate port numbers,
empty components, non-generated secrets, malformed paths and quantities.

Warnings: `port` differs from `EXPOSE` in the image (from `shelf render`, which looks at the
image); unreferenced secrets; changed volume definition of an existing component
(`volumeClaimTemplates` are immutable, local-path cannot grow) — this one needs the previous
deploy artifact and comes in Phase 4.

`shelf validate` never contacts a registry. Duplicate keys (e.g. two volumes with the same name)
are rejected by the parser, as are unknown fields and wrong types.

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

- `shelf validate <app.yaml>...`
- `shelf render <app.yaml> [-o configmap|app]` — the manifest on stdout; findings and a
  summary (objects, PVC names, secret *names*) on stderr. `--reveal` for secret values follows
  in Phase 4, when `shelf app add` creates them.
- `shelf schema` — the JSON Schema; `shelf version`
- `shelf init cluster [--platform oci://…:<tag>] [--insecure-registry] [--context …]
  [--kubeconfig …] [--yes] [--timeout 5m]` — shows the target and asks; prints what it
  created, changed or left unchanged, and how long each wait took
- `shelf init expose` / `shelf init host` / `shelf init`
- `shelf app add <name> <oci://…:tag> [--insecure-registry]` / `shelf app rm <name>`, both
  with `--context`, `--kubeconfig`, `--timeout`; `rm` asks unless `--yes`
- `shelf doctor` — preflight plus runtime (VM, tunnel, Flux status), reporting per layer
- `shelf destroy` — remove profile, tunnel and DNS records

## Repository layout

```
shelf/
  CLAUDE.md                   working agreements for Claude (see below)
  .devcontainer/
    devcontainer.json         Docker-in-Docker, volumes, remoteEnv
    Dockerfile                Go base + pinned tool binaries (single source of versions)
    ssh_config                template for mini access (Phase 6)
  hack/
    lib.sh                    shared helpers, require_devcontainer guard
    cluster-up.sh             create/start dev cluster and registry, write kubeconfig
    cluster-down.sh           delete dev cluster and registry
    platform-push.sh          push platform/ to the dev registry
    chart-push.sh             push the chart to the dev registry
    nuke.sh                   host-side cleanup by exact name (POSIX sh)
    smoke/                    smoke tests (Phase 0, chart in Phase 2, init in Phase 3, apps and
                              tenant in Phase 4)
  cmd/shelf/                  CLI entry point
  internal/
    cli/                      cobra commands
    schema/                   app.yaml types, parser, ${…} syntax, JSON Schema generation
    validate/                 validation rules
    render/                   app.yaml → resolved app.yaml → ConfigMap manifest; registry lookup
    chart/                    helm template golden-file tests for charts/shelf-app
    secrets/                  secret value generation, merge, host backup
    deploy/                   reading a deploy artifact from the registry
    testutil/                 golden-file helper
    preflight/                host checks
    host/                     brew, pmset, colima — behind a command-runner interface
    cloudflare/               tunnel and DNS API
    cluster/                  shelf init cluster: embedded Flux Operator manifest, FluxInstance,
                              server-side apply and readiness waits
  charts/shelf-app/           the generic app chart
  platform/                   Flux-managed platform manifests (→ OCI artifact)
    traefik/                  Phase 3; cloudflared/ and external-dns/ follow in Phase 5
    apps/                     the ResourceSet (Phase 4)
  .github/workflows/
    ci.yml                    level-1 checks and level-2 smoke tests in the devcontainer image
    build.yml                 reusable tenant workflow
    release.yml               CLI binaries, chart push, platform artifact
  examples/hello/
  examples/tenant/            files of the example tenant repo tweinmann/shelf-hello
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
- Step 5: green with a classic PAT against one of the maintainer's private GHCR images.
  Negative check: the same pod without
  `imagePullSecrets` fails with `401 Unauthorized`, so the image is really private.
- Step 6: `just cluster-reset` in 13 s.
- Step 7, first attempt: failed. `nuke.sh` was started in a terminal inside the devcontainer,
  removed `shelf-devcontainer` and thereby killed itself; network, volumes and images stayed.
  It also left four anonymous volumes behind, because the k3s image declares `VOLUME`s and the
  server container was removed without `-v`; they were removed by exact ID. Fixes: `nuke.sh`
  now refuses to run inside a container and removes containers with `rm -f -v`.
- Step 7, second attempt: green. `hack/nuke.sh --with-shared-images` run in a terminal on the
  Mac, then the devcontainer was reopened and the baseline repeated. Compared with the baseline,
  containers, networks, volumes and images differ only by the objects of the reopened
  devcontainer (`shelf-devcontainer`, its `vsc-shelf-…` image, the base image
  `devcontainers/go:2-1.27-trixie`, and the volumes `shelf-gomodcache`, `shelf-gocache`,
  `shelf-claude-config`). Nothing from k3d is left, no foreign object is missing, the
  anonymous-volume count matches. Build cache grew from 81 to 109 entries (expected, left in
  place). The two untagged images of the baseline still exist; Docker CLI 29 in the
  devcontainer lists them only with `docker images -a`. The SHA-256 of the Mac's
  `~/.kube/config` is unchanged.

Phase 0 is complete; all acceptance criteria are met.

**Acceptance:** all tools at the pinned versions; both smoke tests green;
`just cluster-reset` in under a minute; after `hack/nuke.sh` the baseline is identical to
before, apart from build cache entries; the Mac's `~/.kube/config` hash is unchanged.

### Phase 0b – Switch to Docker-in-Docker
Decided on 2026-09-17, after Phase 1 and before Phase 2 (rationale under "Devcontainer with
Docker-in-Docker"). Code from Phase 1 is not affected.

1. `devcontainer.json`: `docker-in-docker` feature instead of `docker-outside-of-docker`,
   volumes `shelf-docker` and `shelf-containerd`, marker `SHELF_DEVCONTAINER` instead of
   `LOCAL_WORKSPACE_FOLDER`; `hack/lib.sh` guard, `cluster-up.sh`, `cluster-down.sh` and
   `nuke.sh` simplified; docs updated
2. On the Mac: "Rebuild Container". Then, in a Mac terminal, check that the container mounts
   `shelf-docker` and `shelf-containerd` and that no `dind-var-lib-*` volume exists
3. In the container: `docker info` reports the container's host name and Docker 29.8.1;
   `just test` is green
4. `just cluster-up` (timed), `just smoke-secrets`, `just smoke-registry <image>`,
   `just cluster-reset` (timed); meanwhile `docker ps` on the Mac shows no k3d containers
5. Restart the devcontainer; `just cluster-up` brings the existing cluster back
6. CI is green with the privileged devcontainer
7. On the Mac: `hack/nuke.sh --with-shared-images`, reopen the devcontainer, compare with the
   baseline as in Phase 0 step 7

**Acceptance:** as for Phase 0 (smoke tests green, reset under a minute, baseline restored,
`~/.kube/config` unchanged), plus: no k3d object on the host daemon at any time, and the
cluster survives a devcontainer restart.

Results (2026-09-17):

- Step 1: done (commit `1eaea38`); the rebuild also updated `devcontainer-lock.json` to
  `docker-in-docker` 4.1.1.
- Step 2: the container mounts `shelf-docker` on `/var/lib/docker` and `shelf-containerd` on
  `/var/lib/containerd`, so the mounts in `devcontainer.json` do replace the feature's own; no
  `dind-*` volume exists. The shared `vscode` volume is mounted at `/vscode`, which is why
  `nuke.sh` resolves only the two daemon targets through the container.
- Step 3: `docker info` reports the container's host name, Docker 29.8.1, cgroup v2, storage on
  the `shelf-docker` volume (ext4); `just test` green. `/go/pkg` has mode `1777` after the
  rebuild; the existing `shelf-gomodcache` volume keeps its earlier owner `vscode`, which is
  fine locally.
- Step 4: `just cluster-up` in 14 s including the first k3s pull, `just cluster-reset` in 10 s
  (Phase 0: 13 s). kubectl reaches `https://127.0.0.1:6445` with the kubeconfig from k3d as is;
  nested k3s runs without extra flags. No Traefik; StorageClass `local-path` with
  `WaitForFirstConsumer`. `just smoke-secrets` green. While the cluster ran, `docker ps` on the
  Mac showed no k3d container. The guard rejects a missing marker, a foreign `KUBECONFIG` and an
  unreachable daemon, and leaves the cluster alone. `just smoke-registry` green against a
private GHCR image (run by the maintainer, because it prompts for the PAT).
- Step 5: after a rebuild of the devcontainer (new container, new host name) the node
  container was already running again, with the cluster 5 minutes old and the system pods
  restarted once. `~/.kube` was empty; `just cluster-up` rewrote the kubeconfig in 2 s.
- Step 6: CI green with the privileged docker-in-docker devcontainer (commit `d01173b`).
- Step 7: green. `hack/nuke.sh --with-shared-images` run in a terminal on the Mac removed
  `shelf-devcontainer`, the volumes `shelf-gomodcache`, `shelf-gocache`, `shelf-claude-config`,
  `shelf-docker` and `shelf-containerd`, the devcontainer image and the base image. The
  baseline was repeated before reopening the devcontainer, so no objects had to be discounted:
  containers, networks and volumes are identical. Nothing from k3d ever reached the host
  daemon, and the cluster went away with `shelf-docker`. The only difference was one untagged
  image, `9ba5023c09d5`: the Phase 0 devcontainer image (built 2026-09-16, labels show
  `docker-outside-of-docker` and the `shelf-*` mounts). The Phase 0b rebuild moved the
  `vsc-shelf-…` tag to the new image and left the old one untagged, and `nuke.sh` resolves
  only the image of the current container. It was identified with `docker image inspect` and
  removed by exact ID. A second image comparison, taken after the devcontainer had been
  reopened, differs from the baseline only by that devcontainer's images (the base image
  `devcontainers/go:2-1.27-trixie` and `vsc-shelf-…`, rebuilt from cache with the same ID
  `70085b94636e`). The SHA-256 of the
  Mac's `~/.kube/config` is unchanged.

Phase 0b is complete; all acceptance criteria are met. Lesson: every "Rebuild Container"
can leave the previous devcontainer image untagged. It is recognizable by the `shelf-*`
mounts in its `devcontainer.metadata` label and has to be removed by ID.

### Phase 1 – Scaffold, schema, renderer
Repository layout, JSON Schema for `app.yaml` (editor autocomplete), `shelf validate`,
`shelf render`, `just test`, CI with `go test` + `kubeconform`.
**Acceptance:** the example validates, every error case has a test, `$` escaping and URL-safe
password generation are tested.

Results (2026-09-16):

- Layout: `cmd/shelf`, `internal/{schema,validate,render,secrets,cli,testutil}`,
  `schema/app.schema.json`, `examples/hello`, `.github/workflows/ci.yml`. Dependencies: cobra,
  go.yaml.in/yaml/v3, invopop/jsonschema (generation), santhosh-tekuri/jsonschema (tests only),
  go-containerregistry, k8s.io/apimachinery v0.36 (quantities, port names).
- `examples/hello`: `web` (traefik/whoami, route `/`), `db` (Postgres with a volume and a
  generated password) and `check`, which runs `psql "$DATABASE_URL"` in a loop — the Phase 2
  proof that the generated `DATABASE_URL` reaches the database, and a live test of `$`
  escaping. It validates without findings, and `shelf render` against Docker Hub pins both
  images.
- `just test` is green: gofmt, `go vet`, `go test`, `kubeconform -strict` on the rendered
  ConfigMap, `shelf validate examples/*/app.yaml`. Every validation error has a table test
  with path and line. The committed schema is checked against the Go types, and it must accept
  the examples. Removing the `$` escaping makes the render and CLI tests fail (checked).
- Deferred to Phase 4: `render --reveal`, the changed-volume warning.
- Found: the Go directories in the image were not writable in two situations. Locally,
  `/go/pkg` was root-owned (`mkdir -p` runs as root, only `mod` was chowned), so `go get` of a
  new module failed writing `/go/pkg/sumdb`. In CI, the first run failed with
  `mkdir /go/pkg/mod/cache: permission denied`: `devcontainers/ci` changes `vscode`'s UID to the
  runner's (1001), `usermod` only re-owns the home directory, and the fresh module-cache volume
  copies the old owner (1000) from the image. Fix: `/go/pkg` and `/go/pkg/mod` are now mode
  `1777`, like `/go` in the base image, so any UID can write. Until the local devcontainer is
  rebuilt, add modules with `GOPATH=$HOME/go GOMODCACHE=/go/pkg/mod go get …`.

### Phase 2 – Chart `shelf-app`
Deployment vs. StatefulSet, Services, headless Services, Ingress, PVCs, secret env prefixing,
probes, resources. `helm template` golden files in CI.
**Acceptance:** the example `app.yaml` installed via `helm install` in the dev cluster; Postgres
data survives `kubectl delete pod`; the API reaches the DB through the generated `DATABASE_URL`;
`kubectl exec … printenv` shows the expanded value, `kubectl get deploy -o yaml` does not.

Results (2026-09-17):

- Chart `charts/shelf-app` 0.1.0: values are the resolved app.yaml plus the `platform` block.
  Templates: `workloads.yaml` (Deployment or StatefulSet), `services.yaml`, `ingresses.yaml`
  (Ingress, and a Traefik `Middleware` for `stripPrefix`), `checks.yaml` (fails early on a
  wrong `apiVersion`, missing values, a namespace other than the app name, a route without
  `platform.domain`, and `stripPrefix` without the Middleware CRD).
- Choices made without a separate decision: `enableServiceLinks: false` (otherwise a component
  `db` would get `DB_PORT=tcp://…` injected everywhere), `automountServiceAccountToken: false`,
  selector labels `shelf.dev/app` + `shelf.dev/component` only (chart labels stay out of the
  pod template, so a chart upgrade alone restarts nothing), `revisionHistoryLimit: 3`,
  StatefulSets with `podManagementPolicy: Parallel` (instances do not form a cluster),
  readiness probe with `failureThreshold: 3` and liveness with `6`, both every 10 s, default
  requests `50m`/`64Mi`, memory limit only when `resources.memory` is set.
- Secret references: for every secret the chart splits each string on `$$` and replaces
  `${secrets.<name>}` in the parts; a container gets `SHELF_SECRET_<NAME>` only for secrets it
  references outside an escaped `$$`, before all other env entries.
- Level 1: `internal/chart` renders `examples/hello` and `internal/chart/testdata/features.app.yaml`
  (several ports, `stripPrefix`, probe delay, secrets in `command`/`args`/`env`, literal `$`,
  a secret referenced only in escaped form, several instances with volumes, a StatefulSet
  without ports) through the real renderer and `helm template`, against golden files; plus
  targeted secret-reference checks, the error cases of `checks.yaml`, and "never
  `LoadBalancer`". `just test` adds `helm lint --strict` and kubeconform on the golden files
  (Middleware skipped: no schema in the default catalog). A server-side dry run in the dev
  cluster accepted all objects, including a headless Service without ports. Breaking the `$$`
  split makes the tests fail (checked). Found on the way: go test cached chart test results
  across chart changes, because only the helm subprocess read the chart; the test now reads
  the chart files itself (in the test, not in `TestMain`, which runs before go test starts
  recording file access).
- Level 2, `just smoke-chart` (about 2 min, mostly waiting for the `check` loop): installs
  `examples/hello` with a real `shelf render` and a random secret, ready in 6–17 s. Checks:
  `check` gets query results from `db` through the generated `DATABASE_URL`; `printenv` shows
  the expanded URL while no Deployment, StatefulSet, Pod, Service or Ingress contains the
  value and the Deployment keeps `$(SHELF_SECRET_DB_PASSWORD)`; `db` is ClusterIP and
  `db-headless` headless; Ingress host `hello.dev.local`; `web` answers `/health` and `/`
  through its Service; a marker row in Postgres survives `kubectl delete pod db-0`. Then the
  example is upgraded without the db volume (StatefulSet → Deployment, `db-headless` and PVC
  gone) and back (Deployment → StatefulSet): both upgrades succeed, Service `db` keeps its
  cluster IP, and `check` reconnects each time.
- Traefik itself is not installed yet (Phase 3), so the Ingress is only checked as an object.

### Phase 3 – `shelf init cluster`
Flux Operator, `FluxInstance`, Traefik as ClusterIP, platform OCI artifact, all against the
current kubecontext. No Colima assumptions in the code.
**Acceptance:** runs in one command on a freshly reset dev cluster, twice in a row without
changes; example app reachable via `port-forward` + `Host` header.

Results (2026-09-17):

- Design checked by hand first: operator via `kubectl apply`, a `FluxInstance` with the sync
  patches, Traefik from the platform artifact, `hello` reached through
  `kubectl port-forward svc/traefik` with a `Host` header (unknown host: 404). Then built in Go.
- `internal/cluster.Install`: applies namespaces and CRDs first and waits until they are
  established, resets the discovery cache, applies the rest, waits for the operator
  Deployment, applies the `FluxInstance` and waits for it, then waits for the platform. Each
  apply is classified as created, configured or unchanged by comparing `resourceVersion`
  (a server-side apply that changes nothing does not write).
- Readiness: kstatus for built-in kinds (CRDs, Deployments). For Flux objects kstatus is not
  enough: it reports a custom resource without any status as ready, which made the first
  version claim "Flux ready after 0s". Flux objects now need `Ready=True` for the current
  `generation`.
- The platform wait requests a reconciliation of the sync `OCIRepository`
  (`reconcile.fluxcd.io/requestedAt`, as `flux reconcile` does), waits until Flux handled it,
  and then until the `Kustomization` is ready with exactly that artifact revision. Without
  this, re-pushing the moving `dev` tag and rerunning `shelf init cluster` reported the old
  revision as ready.
- Level 1: golden files for the `FluxInstance` (with and without the insecure patch), the
  embedded manifest (its operator image must match `FluxOperatorVersion`), reference parsing,
  install order, apply classification, the Ready rule, and the CLI (confirm, decline, no
  answer, `--yes`, `--context`, unknown context, bad reference, dev build without
  `--platform`, default platform from the version). `just test` now validates custom
  resources against the CRDs-catalog (Flux, Flux Operator, Traefik `Middleware`, which was
  skipped before) and the kustomize build of `platform/`.
- Level 2, `just smoke-init` on a freshly reset cluster, green: first run 60–86 s (operator
  18–28 s, Flux 36–50 s including image pulls, platform 2–4 s), second run 6 s with all 13
  operator objects and the `FluxInstance` unchanged; a re-pushed artifact is applied within
  about 1 s; Traefik HelmRelease ready, Service ClusterIP, no `LoadBalancer` Service in the
  cluster; `hello.dev.local/` reaches `web`, an unknown host gets 404; an app with
  `route: { path: /api, stripPrefix: true }` receives `/api/items` as `/items`, and a path
  outside the route gets 404.
- The binary grew to about 37 MB (client-go); the darwin/arm64 build works.
- Open for Phase 4: private platform artifacts (`spec.sync.pullSecret`) are not needed, the
  release artifact is public; per-cluster settings for the platform (domain, tunnel) come
  with Phase 4/5, probably as `postBuild.substituteFrom` on the sync Kustomization.

### Phase 4 – Delivery
Deploy artifact format, `ResourceSet` + `ResourceSetInputProvider`, `shelf app add`/`rm`,
reusable workflow (the Flux `Receiver` moved to Phase 5, see decisions). From here, level-2
tests also run in CI (`ubuntu-24.04-arm`) — the same k3d as locally, so Flux and ResourceSet
regressions surface in PRs.

Design:

- `shelf init cluster` gains `--domain` and `--chart`, and reads `GHCR_USERNAME`/`GHCR_TOKEN`.
  It creates the namespace `shelf-system` with the Secret `registry` (dockerconfigjson; empty
  when no token is given, never overwritten by an empty one), and the ConfigMap `shelf-config`
  in `flux-system`, which the sync Kustomization uses for `postBuild.substituteFrom`
  (`SHELF_DOMAIN`, `SHELF_CHART_URL`, `SHELF_CHART_TAG`, `SHELF_INSECURE_REGISTRY`).
- The platform gets the ResourceSet `apps` in `shelf-system`. For every
  `ResourceSetInputProvider` labelled `shelf.dev/app` it generates, in the app namespace: the
  Namespace; Secrets `shelf-secrets` and `shelf-registry`, copied from `shelf-system`; a
  ServiceAccount with a Role limited to ConfigMaps, which the deploy Kustomization
  impersonates, so a tampered artifact cannot create anything else; the deploy
  `OCIRepository` (1m) and `Kustomization` (`targetNamespace`, `prune`, `wait`, label
  `reconcile.fluxcd.io/watch: Enabled` on the ConfigMap so helm-controller reacts to it); the
  chart `OCIRepository`; the `HelmRelease` with `valuesFrom` the ConfigMap and the `platform`
  values.
- `shelf app add <name> <oci://…:tag>` reads the artifact from the registry (Docker keychain),
  checks `apiVersion` and name, generates missing secrets and stores them in the Secret
  `app-<name>` in `shelf-system` and in `~/.shelf/apps/<name>/secrets.yaml` (0600); a backup
  restores values after a cluster rebuild. It creates the provider and waits for the
  HelmRelease. Running it again adds new secrets and keeps existing ones.
- `shelf app rm <name>` asks, deletes the provider and waits until the namespace is gone,
  then deletes `app-<name>`. The host backup stays.
- Deploy artifact: `shelf render` output pushed with `flux push artifact` as
  `ghcr.io/<owner>/<app>-deploy:sha-<short>`, tagged `main` on the default branch.
- Dev registry: reachable as `shelf-registry:5000` from pods and from the devcontainer (an
  `/etc/hosts` entry written by `cluster-up.sh`), so the same reference works for `flux push`,
  `shelf app add` and Flux.

Steps:

1. Dev registry under one name; `just chart-push` pushes the chart to it
2. `shelf init cluster`: `shelf-system`, registry Secret, `shelf-config`, substitution
3. Platform: ResourceSet `apps`
4. `shelf app add` / `shelf app rm`
5. Level 1 tests; level 2 `just smoke-apps`: add, route, update by polling, isolation, rm,
   restore from backup
6. Reusable workflow `build.yml`, example tenant files, repo `tweinmann/shelf-hello` created by
   the maintainer, acceptance with GHCR
7. Level 2 in CI
**Acceptance:** a push to an example repo → the app in the dev cluster updates without manual
intervention; `shelf app rm` cleans up completely.

Results (2026-09-17, steps 1–5 and 7):

- Step 1: `cluster-up.sh` publishes the registry on `127.0.0.1:5000` and adds
  `127.0.0.1 shelf-registry` to the devcontainer's `/etc/hosts`, so every tool uses
  `shelf-registry:5000`. `just chart-push` pushes the chart as `0.0.0-dev` (`helm push
  --plain-http`).
- Step 2: `shelf init cluster` requires `--domain`, takes `--chart` (release default
  `oci://ghcr.io/tweinmann/shelf/charts/shelf-app:<version without v>`), and prints the
  registry login it will store. The system namespace carries the label
  `app.kubernetes.io/managed-by: shelf`: without any field of its own, server-side apply
  dropped shelf's field manager, and the next run reported the namespace as changed.
- Step 3: checked by hand before writing Go: `copyFrom` copies both Secrets, the chart and the
  deploy artifact are fetched, deleting the provider removes the namespace including the
  volume (38 s). Found a chart bug on the way: Flux sets the chart version to
  `0.0.0-dev+<digest>`, and `+` is not allowed in the `helm.sh/chart` label, so every install
  failed. The label now replaces `+` with `_` and is cut to 63 characters, as `helm create`
  does; a chart test packages the chart with such a version. An artifact with a
  `ClusterRoleBinding` in it fails with "forbidden" for `system:serviceaccount:<app>:shelf-deploy`
  and creates nothing.
- Step 4: `internal/deploy` reads the artifact (Flux content layer), requires exactly one
  ConfigMap with `app.yaml`, the supported `apiVersion` and a valid app; `internal/secrets`
  merges cluster, backup and generated values (cluster wins, backup restores, nothing is
  dropped) and writes the backup atomically with 0600/0700; `internal/cluster` applies the
  Secret `app-<name>` and the provider, then asks Flux to reconcile the deploy
  `OCIRepository` and the `HelmRelease` and waits for each, with the Kustomization in between
  checked for the exact revision. A `Stalled` condition ends a wait at once. `shelf app rm`
  asks, deletes the provider in the foreground, waits for the namespace (refusing a namespace
  without the app label) and deletes the Secret; it fails for an unknown app.
- Step 5, level 1: golden files for the settings, registry Secret (with a fake token), app
  Secret and provider; artifact decoding (other files, multi-document YAML, zero or two apps,
  wrong `apiVersion`, invalid app, broken YAML) and fetching from an in-memory registry;
  secret merge and backup (modes, atomic replace, a backup of another app); CLI for `init
  cluster` settings (domain, chart, GHCR variables, the token never printed) and `app
  add`/`rm` with fakes (generate, keep, add a new secret, restore after a rebuild, no backup
  without secrets, name mismatch, fetch errors, confirm, decline, unknown app).
- Step 5, level 2, `just smoke-apps` (about 3 min): add in 10–16 s; the generated password
  reaches `DATABASE_URL` and is not printed; `hello.dev.local` is routed; a new artifact under
  `main` is rolled out by polling alone after 43–49 s; a second add reports the Secret and the
  provider unchanged; the tampered artifact is refused; `rm` takes 20–44 s and leaves no
  namespace, Secret, provider or PV, only the backup; removing again fails; a new add restores
  the password from the backup.
- Step 6: green, with the real tenant repository `tweinmann/shelf-hello` (private) and GHCR.
  A push of a changed `MESSAGE` reached the app after 173 s without any command: about two
  minutes for the tenant workflow, the rest for the `OCIRepository` poll. Three things came
  out of this run:
  - The platform ResourceSet copies the registry credential into the app namespaces, but only
    on its own interval, which defaults to one hour. A corrected token therefore never arrived,
    and Flux kept failing with `DENIED: denied` while `shelf-system` already held a working
    one. Both source Secrets now carry the label `reconcile.fluxcd.io/watch: Enabled`, which
    makes the operator copy a change at once, and the ResourceSet reconciles every 10m.
  - A wait for a registry that refuses the login ran into the five-minute timeout. Such a
    message (`denied`, `unauthorized`, `forbidden`) now ends the wait immediately and names the
    Secret to fix.
  - `just smoke-tenant` accepted any changed answer as success, including the `Gateway Timeout`
    that Traefik returned during a rollout. It now requires HTTP 200 and the same answer twice
    in a row. It also checks the GHCR login before touching the cluster, against
    `ghcr.io/token?scope=repository:<repo>:pull` (the `/v2/` endpoint answers 401 whatever is
    sent, so it says nothing), and writes the login into a Docker config of its own instead of
    running `docker login`, which had failed in the Docker-in-Docker daemon.
- Step 6 artifacts: `.github/workflows/build.yml` (reusable: installs shelf with `go install`,
  builds the images natively on `ubuntu-24.04-arm` and pushes them with the tags `sha-<short>`
  and `main` on the default branch only, validates, renders, pushes the artifact with
  `flux push artifact` and tags it `main`, writes the `shelf app add` line to the job
  summary); `examples/tenant` (app `greeter`, a small Go server built from `web/`, the calling
  workflow); `just smoke-tenant <app> <artifact>` stores the GHCR login, adds the app and waits
  for a pushed change to show up. The app is called `greeter` because app names must not start
  with `shelf-`. The web image builds and answers locally.
- Step 7: CI job `cluster` runs `cluster-up`, `smoke-secrets`, `smoke-chart`, `smoke-init`
  and `smoke-apps` in the devcontainer. The same sequence on a fresh local cluster takes about
  8 minutes. Running it exposed a race in `smoke-secrets` and `smoke-registry`: a pod created
  right after its namespace is rejected until the namespace's `default` ServiceAccount
  exists; `create_namespace` in `hack/lib.sh` now waits for it.
- License: Apache-2.0 (`LICENSE`), with a `NOTICE` for the redistributed Flux Operator
  manifest. Chosen over MIT for the explicit patent grant and because Flux, Helm and k3s use it
  too.
- Before publishing: the repository history was rewritten (`git filter-branch`) to remove the
  names of the maintainer's private projects and the machine path from `docs/plan.md`, and to
  replace the author address with `tweinmann@users.noreply.github.com`; `user.email` is set to
  that address for this repository. Commit hashes changed, and the two references to commits in
  this document were updated. The schema example is now a neutral `shop` app.
- The shelf repository is public, both CI jobs are green, and the history was rewritten before
  publishing (see above).
- Open for later: during one rollout of a single-instance app Traefik answered 504 for a
  moment, although a Deployment starts the new pod before it stops the old one; in a second
  run this did not happen. Worth a look when the platform is exposed for real (Phase 5).

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
`examples/hello` and `examples/tenant`, then the maintainer's own apps as the first real
tenants.

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
