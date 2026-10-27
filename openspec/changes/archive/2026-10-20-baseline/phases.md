# Phases 0 to 8d

Moved verbatim from `docs/plan.md` on 2026-10-20 (change `retire-plan-md`): the development and
test environment, then the goals, acceptance criteria and results of every phase before OpenSpec.
Phases 9 to 13 became the draft changes `phase-9-mac-mini` to `phase-13-reference-apps`, and the
open acceptance runs of 8b and 8d the draft `phase-8-acceptance`.

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
In CI (decided 2026-09-29, when a merge took twenty minutes of CI) level 2 runs on pull requests
only, one job with a cluster of its own per smoke test, side by side; after a merge, `main` gets
level 1 alone.

**Level 3 — Mac mini over SSH (Phase 9 onward).** Only what level 2 cannot do: host preflight on
real hardware, Cloudflare Tunnel against the real domain, the launchd service, and the first run
on an untouched machine.

The admin UI is level 2: `shelf serve` runs in the devcontainer against the k3d cluster, and the
browser on the Mac reaches it through a forwarded port. Only the host parts — Colima, launchd,
`install.sh` — need the mini.

### Design consequence: `shelf init` is split into layers

For level 2 to be possible at all, the installer is split into individually callable,
idempotent steps:

```
shelf init host     # brew, pmset, Colima profile     → target Mac only (Phase 9)
shelf init cluster  # Flux Operator, platform charts  → against the current kubecontext
shelf init          # wrapper around both
```

`shelf init cluster` must contain **no Colima assumptions** and only use the kubecontext.
In development, `cluster` runs; `host` never does. (Until Phase 8b there was a third layer,
`shelf init expose`, for the tunnel all apps shared. Exposure is now part of an app:
`shelf app add --cloudflare`.)

The split pays off a second time from Phase 11 on: the setup wizard's steps *are* these three
operations, called from the server instead of from a terminal. That only works because they are
separate and idempotent, which is why re-running the wizard on a configured host changes nothing.

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
cluster setup instead of two. Colima is only needed on the mini (Phase 9), because Docker
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

### Mac mini access for development (Phase 9 onward)

This is how shelf is *developed and tested* against the mini, not how the mini is operated: since
2026-09-20 the mini is run through its admin UI (see "Components on the mini"). The SSH forward,
the separate kubeconfig and `just push-mini` stay as level-3 test tooling for the maintainer.

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

## Phases

Stop after each phase, show the result, wait for approval.
From Phase 9 on, every phase is an OpenSpec change `openspec/changes/phase-<n>-<slug>/`
(archived to `openspec/changes/archive/` when it ends); its section here keeps the roadmap entry
and gains a link to the change and a short summary of the result. Phases 0–8d keep their full
text here.
Phases 0–8 run entirely in the devcontainer on the MacBook; the mini joins in Phase 9.
Phases 6 and up were re-cut on 2026-09-20 when the target product changed: the old Phase 6
(Mac mini) is now Phase 9, the old Phase 7 (reference apps) is now Phase 13.

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
- `shelf app add <name> <oci://…:tag>` reads the artifact from the registry (with the login the
  cluster holds, falling back to the Docker keychain), checks `apiVersion` and name, generates missing secrets and stores them in the Secret
  `app-<name>` in `shelf-system` and in `~/.shelf/apps/<name>/secrets.yaml` (0600); a backup
  restores values after a cluster rebuild. It creates the provider and waits for the
  HelmRelease. Running it again adds new secrets and keeps existing ones.
- `shelf app rm <name>` asks, deletes the provider and waits until the namespace is gone,
  then deletes `app-<name>`. The host backup stays.
- Deploy artifact: `shelf render` output pushed with `flux push artifact` as
  `ghcr.io/<owner>/<app>:sha-<short>`, tagged `main` on the default branch.
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
  run this did not happen. Phase 5 did not see it again; it is under "Open items – Later" now.

### Phase 4b – Less shelf in the tenant repository
Decided after the Phase 4 acceptance (rationale in the decisions table). A tenant repository
should only know the `app.yaml` format and one boilerplate workflow.

1. Schema `build: ./web` as an alternative to `image:`; validation (exactly one of the two, a
   relative directory inside the repository)
2. `shelf build-plan` (JSON: component and build context) and
   `shelf render --image <component>=<reference>`
3. `release.yml`: binaries for linux and darwin with checksums, the chart as
   `oci://ghcr.io/<owner>/shelf/charts/shelf-app:<version without v>`, the platform artifact as
   `oci://ghcr.io/<owner>/shelf/platform:<tag>`, and the major tag moved to the release
4. `build.yml`: download the release binary instead of `go install`, build what `build-plan`
   lists, pin the digests from the build metadata, no `images` input any more
5. `examples/tenant` uses `build: ./web` and `@v0`; the tenant repository has no image names
6. Acceptance: release, then a push in `tweinmann/shelf-hello` reaches the app again

**Acceptance:** a tenant repository consists of `app.yaml` plus an unchanged workflow file, and
a push rolls out as in Phase 4.

Results (2026-09-17):

- Release `v0.1.0`: `release.yml` ran on the tag, published four binaries with checksums (the
  linux/arm64 one verifies and prints `shelf v0.1.0`), pushed the chart as
  `oci://ghcr.io/tweinmann/shelf/charts/shelf-app:0.1.0` and the platform as
  `oci://ghcr.io/tweinmann/shelf/platform:v0.1.0`, both publicly readable, and moved `v0` to the
  release commit.
- `tweinmann/shelf-hello` now holds only `app.yaml` (with `build: ./web`) and a workflow that
  is the same for every app. A push reached the app after **81 s**, less than half of the 173 s
  in Phase 4, because the workflow downloads the release binary instead of building shelf.
- Package naming was inconsistent at first: the artifact was named after the app
  (`greeter-deploy`), the image after the repository (`shelf-hello-web`), so nothing showed that
  they belong together. Everything of an app now lives under its name: the deploy artifact is
  `ghcr.io/<owner>/<app>`, an image `ghcr.io/<owner>/<app>/<component>`. A registry serves both
  names side by side (checked against the dev registry). `shelf build-plan` therefore reports
  `{app, builds}`; the workflow needs the name before it builds.
- Release `v0.2.0` carried that change. The tenant repository needed no edit at all: `@v0` moved
  to the new workflow, and the next push published `greeter` and `greeter/web`.
  `shelf app add greeter oci://ghcr.io/tweinmann/greeter:main` moved the running app over, and
  the rendered `app.yaml` now reads
  `ghcr.io/tweinmann/greeter/web:sha-905acce@sha256:…`, so the commit that built the image is
  visible in front of the digest.

### Phase 5 – `shelf init expose`
Cloudflare Tunnel via API, cloudflared with a catch-all rule, and one proxied CNAME per app.
Planned with external-dns as the record writer; the results below say why shelf writes the
records itself instead. Tested **from the dev cluster** against a test domain — the mini is not
needed for this.
**Acceptance:** the example app is reachable from outside over HTTPS from the dev cluster, and
its DNS record is a proxied CNAME to `<uuid>.cfargotunnel.com`. (A proxied record answers with
Cloudflare's addresses, so `dig` shows those; the CNAME itself is visible through the API, which
is what `just smoke-expose` checks.)

Results (2026-09-18):

- Green with the tenant app `greeter` in the dev cluster: `shelf init expose` in 4 s on a repeat
  run (33 s the first time, of which 8 s for the tunnel), the record is a proxied CNAME to the
  tunnel, and `https://greeter-dev.<domain>/` answers 200 with a valid certificate. Deliberately
  pointing the record somewhere else and running `shelf init expose` again corrects it.
- external-dns was built first and then removed (see the decisions). What gave it away: the
  chart wrote the annotation prefix `external-dns.alpha.kubernetes.io/`, while external-dns
  0.22 reads `external-dns.kubernetes.io/`. Nothing failed; it simply reported "All records are
  already up to date" and published nothing. The question why shelf needs a generic watcher at
  all followed from that.
- Two bugs came out of the same run: `shelf init expose` wrote the cluster settings back without
  reading `SHELF_INSECURE_REGISTRY`, which silently reset it and broke the chart source of every
  app; a round-trip test now covers the settings. And the platform only substitutes settings when
  its Kustomization runs, so `init cluster` and `init expose` now request that reconciliation
  instead of leaving a new domain or tunnel target to the next interval.
- In the dev loop the chart tag `0.0.0-dev` never changes, so `just chart-push` asks the chart
  source of every app to fetch again; without it a chart change waits an hour.
- A name that is queried before it exists stays negative in resolvers for 30 minutes (the zone's
  SOA minimum), which cost time twice. `just smoke-expose` therefore resolves through DoH.
- Changing the domain or the host suffix of a cluster moves every app to a new name, and the
  records under the old name stay behind: shelf writes records for the names it knows, and it no
  longer knows the old ones. `shelf init cluster` therefore lists them and says to delete them.
  Removing the apps before the change avoids the leftovers altogether.
- Found by CI afterwards: a cluster without `--host-suffix` stores an empty setting, which the
  platform substitutes as an empty value and YAML reads as null, so the chart rendered
  `hello%!s(<nil>).dev.local` and every install failed. Locally the suffix was always set, so
  only the `cluster` job saw it. The substitution is quoted and the chart defaults the suffix
  now, with a chart test for the null case.

Phase 5 is complete; all acceptance criteria are met. Approved on 2026-09-20. Closing it also
removed the last references to external-dns from the code comments, the CLI help and the README,
where the dropped design was still described.

Design:

- `shelf init cluster` gains `--host-suffix`; the settings carry `SHELF_HOST_SUFFIX` and
  `SHELF_TUNNEL_TARGET`, and the chart renders the host `<app><suffix>.<domain>`. A later
  `init cluster` keeps the tunnel target that `init expose` wrote.
- `internal/cloudflare` is a small API client (no SDK): accounts, find, create and delete
  tunnels, and find, create, update and delete DNS records. `shelf init expose` reads
  `CF_API_TOKEN` (and `CF_ACCOUNT_ID` when the token sees several accounts), stores the tunnel
  credentials as a Secret in `shelf-system`, writes the tunnel target into the settings, creates
  the input provider `expose` and publishes the apps that already run. The token itself stays on
  the operator's machine.
- `shelf app add` and `shelf app rm` write and remove the app's record, each through the same
  client and only when the cluster carries a tunnel target and the token is set; without either,
  the app still runs and is only not reachable from the internet.
- `platform/expose` turns that provider into cloudflared (one rule to
  `traefik.traefik.svc.cluster.local:80`, credentials from the Secret, restarted through
  `checksumFrom` when they change).

Steps:

1. Host suffix and tunnel target in settings, chart and ResourceSet
2. `internal/cloudflare`, `shelf init expose`, `platform/expose`
3. Level 1 tests; the platform without the provider generates nothing (checked in the dev
   cluster)
4. `just smoke-expose <app>`: expose, then DNS record and HTTPS, resolved through DoH

### Phase 6 – Operations layer
Lift the orchestration out of the cobra closures into `internal/ops`, and replace the `io.Writer`
progress pattern with `internal/progress.Reporter`. Nothing changes for the user; this is what
makes a second caller possible at all.

Today every user-facing operation is assembled inline in a `RunE` closure — `app add` and
`app rm` in `internal/cli/app.go`, `init cluster` in `init.go`, `init expose` in `expose.go`,
where `findOrCreateTunnel` even takes a `*cobra.Command`. The cluster library below it is already
clean: it takes a `*rest.Config`, reads no environment and no files.

Design:

- `internal/progress`: a `Reporter` interface and a typed `Event` (step started, step done with
  its duration, object applied with its action, DNS record, warning). `progress.Writer(w)` prints
  exactly what the CLI prints today; that is the regression test.
- `internal/ops`: `LoadTarget`, `PlanCluster`, `InitCluster`, `PlanExpose`, `Expose`, `AddApp`,
  `RemoveApp`, `Apps`. Everything the operator machine supplies — the secret backup directory,
  the registry and Cloudflare credentials, the registry authenticator — arrives in one `Env`
  struct instead of through environment reads and package-level variables. `Diagnose` belongs
  to this package too but is written in Phase 7, where the dashboard needs it.
- `internal/cluster` keeps its rule: Kubernetes API only. The one change is mechanical —
  `Out io.Writer` in the option structs becomes a `progress.Reporter`. This is an edit to
  approved code and worth naming as such: keeping the writer and having the server scrape its own
  output would throw away exactly the structure the UI needs.
- Confirmation stays in the CLI. `PlanCluster` returns the facts, and the caller asks in its own
  idiom. The "this tunnel exists but we hold no credentials" case becomes an option plus a
  sentinel error instead of a prompt inside the operation.
- The package-level test seams (`fetchApp`, `addApp`, `clusterSettings`, `newCloudflare`, …)
  become fields of `Env`, which is also what allows `t.Parallel()` in `internal/cli`.
- `deploy.Fetch` gets an explicit authenticator instead of only the Docker keychain (see the open
  items): the mini may have no `~/.docker/config.json` at all.

**Acceptance:** no golden file in `internal/cli/testdata` changes; no mutable package-level test
seam is left in `internal/cli`; `Out io.Writer` is gone from every option struct in
`internal/cluster`; `go test ./... -race` is green with the CLI tests running in parallel;
`just smoke-init`, `just smoke-apps` and `just smoke-expose` pass untouched.

Results (2026-09-20):

- Every acceptance criterion is met, except that `just smoke-expose` was not run: it needs the
  Cloudflare API token, which only the maintainer has. `just smoke-apps` and `just smoke-init`
  passed against the dev cluster, and their output is line for line what Phase 5 printed —
  including `DNS: skipped`, the secret sources and the backup path.
- The regression guard for the output is a golden file in `internal/progress/testdata`, which
  renders one event of every kind. It is the one place where the format of a line is decided
  now, so a change to it is visible in a diff instead of spread over four packages.
- The plan/apply split for `init expose` turned out better than the sentinel error it was
  designed as. `PlanExpose` creates a tunnel when there is none and otherwise reports
  `NeedsReplacement`; the caller asks and calls `ReplaceTunnel`. A sentinel error would have
  meant calling `PlanExpose` again after the confirmation, which repeats the API calls and the
  lines it printed. What this costs: the header lines of `init expose` now appear after the
  Cloudflare calls instead of before them, so an invalid token shows only the error. Nothing a
  test asserts, but worth knowing.
- `cli.New` takes an `Options` struct now: the resolver, the version, a `Getenv` and a factory
  for the operations. That is what makes the tests parallel — they used to swap package
  variables and call `t.Setenv`, and neither works under `t.Parallel()`. It also puts every
  environment read in one place, which is what the service in Phase 10 will replace wholesale.
- `go test -race` is part of `just test` from now on, since the tests run in parallel and the
  server will run operations concurrently.
- Not done here, on purpose: `ops.Ops` holds one cluster and is built per command. Whether the
  server keeps one per job or one per process is a Phase 7 question, and guessing it now would
  have added a lifetime nobody needs yet.

### Phase 7 – `shelf serve`: server, login, read-only dashboard
The HTTP server with embedded templates, the claim/password/session/CSRF model, and a dashboard
that lists the apps with their state. No mutating actions yet.

Design:

- `internal/server` depends on an interface, not on `ops` directly, so its tests need neither
  network nor cluster. Every rendered page gets a golden-file test against a fake.
- `cluster.AppStates` joins the input providers with the HelmReleases, Kustomizations,
  OCIRepositories and pods of the apps — five cluster-wide list calls regardless of how many
  apps there are. The objects of an app are found by the namespace they are in, not by a label:
  the ResourceSet stamps `shelf.dev/app` on what the deploy artifact carries, not on the
  objects it generates itself.
- `ops.Diagnose` walks the chain in order and reports the **first** stage that is not healthy:
  deploy `OCIRepository` (cannot pull, or authentication) → deploy `Kustomization` (artifact
  rejected by the downscoped `shelf-deploy` account) → chart `OCIRepository` → `HelmRelease` →
  workloads (`ImagePullBackOff`, `CrashLoopBackOff`, `OOMKilled`, failing probe). The same
  function backs `shelf app status` and `shelf doctor`, which gives it a golden-file test.
- Claim before anything: until the setup token is used, every route but the claim page and
  `/healthz` is refused, so a port scan on the LAN cannot take the box.
- `just serve` runs the server in the devcontainer against the dev cluster; `devcontainer.json`
  already forwards port 8080.

**Acceptance:** from the Mac's browser the dashboard lists the dev cluster's apps with URL,
revision and state, and names the reason for an app that is broken on purpose; every route except
the claim page and `/healthz` refuses an unauthenticated request; the claim cannot be completed
without the printed token; every rendered page has a golden-file test; `go test ./internal/server`
needs no network and no cluster.

Results (2026-09-20):

- All of it, against the dev cluster: the dashboard lists `greeter` as Ready with its host name
  and revision, and an app whose tag was pointed at something that does not exist shows
  `Failed`, the step that broke (`the deploy artifact`), the registry's own words
  (`MANIFEST_UNKNOWN: manifest unknown`) and shelf's explanation of what that means. Eleven
  golden files cover every page, including a cluster that does not answer and a platform that is
  not installed.
- The diagnosis is the piece worth keeping: five stages in the order things have to happen, and
  the first one that is not ready is the answer. A later stage that is also broken is a
  consequence — an app whose artifact cannot be pulled still has a Ready HelmRelease from the
  last version, and showing that as the state would be a lie. `shelf app status` renders the
  same chain as text, so the UI and the command line cannot drift.
- Writing it turned up a real mistake: "forbidden" from the step that *applies* the artifact
  means the downscoped `shelf-deploy` account refused an object, the opposite of a registry that
  refuses a login. The registry hint is now limited to the two steps that talk to a registry.
- A hand-written `ResourceSetInputProvider`, used to fake a broken app, took the whole `apps`
  ResourceSet down: it copies a Secret that only `shelf app add` creates, and its absence fails
  the reconciliation for *every* app. Deleting the provider fixed it within seconds. Worth
  knowing before the UI lets anyone write providers in Phase 8: the two objects belong together.
- Not built: the background status poller the architecture section described. A timeout on the
  request does the same job without a second source of truth. The section now says so.
- `cmd/shelf` now stops on SIGTERM as well as Ctrl-C. A service is stopped with SIGTERM, and
  until now that killed the process instead of letting it shut down.

### Phase 8 – Mutating actions and the job model
Add, retag, redeploy and remove apps from the browser.

Design:

- A job registry: `POST` returns 202 with a job id, the browser follows the job page, which
  renders the events so far and then attaches to a Server-Sent Events stream. Jobs live in memory
  only — the cluster is the source of truth, and the UI re-reads it after a restart.
- One mutating job at a time, globally; a second request gets 409 with a link to the running one.
- Changing the tag is `AddApp` with a different artifact reference, which is also the rollback
  path: point at `sha-905acce`, then back at `main`. Redeploy is a reconcile request on the
  app's `OCIRepository` and `HelmRelease`.
- Removing an app deletes volumes, so the UI asks for the app name to be typed, as the CLI asks
  for a confirmation.
- The secrets view lists names, reveals a value behind a re-entered password (the values are
  generated, and people legitimately need them for a database client) and writes an audit line.
  Rotation stays on the "Later" list for now.

**Acceptance:** in the dev cluster an app is added, rolled back to a `sha-` artifact and forward
again, redeployed and removed entirely from the browser; the job page survives a reload and shows
the same lines `shelf app add` prints; a second mutating request while a job runs gets 409 with a
link to it; removing an app requires typing its name; the output of `shelf app add` is still
byte-identical to Phase 5.

Results (2026-09-20):

- Every criterion, against the dev cluster: an app added from the browser, pointed at a second
  tag and back, deployed again, and removed — each one a job whose log is the text
  `shelf app add` prints, down to the line that says the secret backup stays behind. A second
  change during a running one answered 409 with a link to it.
- The job log is text on purpose. Each event is sent as the piece of text the command line
  would print, so the browser shows the same thing without a second renderer. A page is
  rendered with the log so far and the browser attaches from that index, which is why a reload
  in the middle of a five-minute change loses nothing. Without JavaScript the page still shows
  everything up to the moment it was loaded.
- `cluster.Redeploy` came out of splitting `AddApp`: the wait from the artifact to the running
  release is the same whether an app was just registered, pointed at another tag, or only asked
  to try again. Changing the tag *is* adding the app with a different reference, which is why a
  rollback needs no rollback machinery.
- Two commands were added so the guardrail holds: `shelf app redeploy` and `shelf app secrets`.
  The values are printed only with `--reveal`, as the UI asks for the password first.
- The tests went from 12 s to 6 s under `-race` by seeding the session into the admin file
  instead of claiming an instance per test: 600 000 PBKDF2 rounds are expensive on purpose, and
  a test that only needs to be logged in should not pay for them. The claim and the login keep
  their own tests that go through the pages.
- Left behind in the dev registry: a second tag `sha-test` on `smoke/hello-deploy`, made to
  prove the rollback. The dev registry has no delete endpoint; `just cluster-reset` clears it.

**The components of an app (2026-09-20).** Added after the acceptance, because the page said only
where the *app* answers and never what it is made of. `/apps/{name}` now lists every component
with its state and its address: a link where a browser reaches it, the host and path where the
cluster is not exposed, and `db:5432` — how a sibling reaches it — for a component without a
route. `shelf app status <name>` prints the same three cases.

- The source is the values ConfigMap `<app>-values` that the deploy Kustomization writes, because
  that is the document the chart renders from. It names every component; the Ingresses would have
  named only the routed ones, and `db` and `check` would have been invisible.
- `cluster.AppStates` was left alone. Components are read per app by `cluster.AppComponents` and
  filled in by `ops.App`, not by `ops.Apps`: the dashboard keeps one address per app, because ten
  apps with four components each is a wall, and the components belong on the page of their app.
- Whether a component gets a link is taken from the app's own URL rather than decided a second
  time from the settings. Two places deciding when a name is reachable is how a link to
  `greeter.dev.local` gets offered again.

**A secret added to app.yaml needs shelf (2026-09-20).** A tenant added a component with a
generated secret and pushed. Flux rolled the new artifact out by itself, as it should, and the
pods failed with `couldn't find key db-password in Secret greeter/shelf-secrets`: generating a
secret value is shelf's step, in `ops.AddApp`, and a rollout by Flux never takes it. The
diagnosis now names that case instead of the general "usually a missing secret or config map" —
it says which secret, and that deploying the app again writes it. The gap itself stays: the
registry is the only interface, and shelf will not watch a tag to find out that an app.yaml grew
a secret.

**The login for reading a deploy artifact comes from the cluster (2026-09-20).** Closing the open
item: `deploy.Fetch` fell back to the Docker keychain, so `shelf app add` failed to read a
private artifact that the cluster itself pulls without trouble, and on the mini — where there is
no `~/.docker/config.json` at all — it could never have worked. `ops.AddApp` now asks the cluster
for the login `shelf init cluster` stored there. Precedence: an explicit `Env.Pull` wins, then
the cluster's, then the Docker config. It is only offered to the registry it was stored for, so a
ghcr.io credential is never sent to the dev registry. Whoever may register an app in a cluster can
already read what that cluster pulls, so this hands out nothing new. (Phase 8b: the login is now
that of the app's registry connection, with the same precedence.)

**A warning is not a guard (2026-09-20).** During the Phase 6 acceptance, `just smoke-init` was
run against the dev cluster, which was serving `greeter-dev.tobile.ch` from the Phase 5
exposure. The script passes `--domain dev.local` hard-coded, so the platform moved the app to
`greeter.dev.local` within the minute. Everything kept working except the thing that mattered:
Cloudflare still resolved the old name, the tunnel still carried it, and Traefik answered 404
because no Ingress had that host any more. shelf printed exactly the warning Phase 5 built for
this — and it scrolled past in a wall of output that was read with `tail`.

Two changes, because the warning was right and its position was wrong:

- `shelf init cluster` **refuses** a domain or host suffix that moves apps that already exist,
  unless `--move-hosts` is passed. `--yes` does not cover it: the point is a gesture that cannot
  be made by accident, and the smoke tests all pass `--yes`. The refusal happens before anything
  is applied, names the apps, and only claims stranded records when the old names were public
  ones — a cluster under `dev.local` has none.
- The smoke tests check the cluster's domain before they start (`require_dev_domain` in
  `hack/lib.sh`) and stop with the command that restores it. A test that silently reconfigures
  the machine it is testing on is a trap, and on the mini it would be a bad one.

The general lesson for the admin UI: an operation that changes what every app answers under is
not a warning, it is a question. Phase 11's wizard has to treat it that way too.

### Phase 8b – Connections instead of platform credentials
Registry logins and Cloudflare tokens become connections: defined once, by name, and chosen per
app. Until now every credential was the platform's: one PAT in `shelf-system/registry`, copied
into every app, and one tunnel with one cloudflared, set up by `shelf init expose` with a token
read from the environment on every call. The decisions are in the table "Decided in Phase 8b"
above.

Design:

- **Registry connections in the cluster**: `connection-registry-<name>` in `shelf-system`, with
  `registry-anonymous` beside them for apps without one. `internal/cluster/connections.go` lists,
  reads, saves and deletes them; the username is shown, the token never.
- **Cloudflare connections on the host**: `hostcfg.Connections` keeps one file per connection
  under `~/.shelf/connections/cloudflare/`, with its name inside, so a file renamed by hand is
  refused rather than used for the wrong account.
- **App**: the provider carries the inputs `domain`, `tunnel`, `registry` and `cloudflare`, the
  last two being connection names; `tunnel-<app>` holds the tunnel's `credentials.json` while the
  app is exposed. `cluster.ReadAppConfig` reads it back; `AppState` carries domain, tunnel and
  both connection names.
- **Platform**: the ResourceSet `apps` copies
  `<< if inputs.registry >>connection-registry-<name><< else >>registry-anonymous<< end >>` into
  the app namespace, renders the chart's domain the same way from `inputs.domain` or
  `${SHELF_DOMAIN}`, and, through `resourcesTemplate` guarded by `<<- if inputs.tunnel >>`, runs
  cloudflared in the app namespace. `platform/expose` is gone.
- **`ops`**: `connections.go` lists connections with the apps that use them, saves them (a
  Cloudflare token is verified and its account worked out before it is kept) and removes them,
  refusing what the rules above forbid. `AddOptions.Access` carries a domain and connection names.
  `AddApp` checks that the chosen connections exist before it writes anything, works out the
  exposure before and after (`planExposure`: find, reuse, replace or create the app's tunnel in
  the connection's account), applies the app, and then cleans up (`finish`: withdraw the old
  record when the name or the account changed, publish the new one, delete a tunnel the app no
  longer uses). `SetAccess` is `AddApp` with the artifact the app is registered with. `RemoveApp`
  withdraws the record and deletes the tunnel after the namespace, and with it cloudflared, is gone.
- **Order at Cloudflare.** An old tunnel is deleted only after the cluster reported cloudflared
  gone or moved, and the client first closes the connections Cloudflare keeps open for a while
  after cloudflared exits.
- **Migration** in `shelf init cluster`: `registry-anonymous` is written, the shared login
  becomes the registry connection `ghcr` and every app registered before gets it, providers from
  before get the new inputs, and the shared tunnel is switched off with a warning naming how to
  expose each app again. The old tunnel and its records stay in Cloudflare — shelf never stored
  the token that made them.
- **Admin UI**: the page `/connections` lists both kinds with the apps that use each and holds
  the only token fields; saving and removing run as jobs like every other change, audited without
  values. The add form and the app's "Access" section offer the connections as choices; once one
  is chosen, the add form offers its deploy artifacts, their tags and the zones of its account,
  and the app page the tags of the app's artifact (see "Choices in the forms" above).

Steps:

1. Cluster: connections, objects per app, `ReadAppConfig`, migration, per-app host moves
2. `hostcfg.Connections`, `ops` connections and exposure, `SetAccess`, `Zones`, the Cloudflare
   connection cleanup
3. CLI: `shelf connection`, `--registry`/`--cloudflare`/`--domain`, `shelf app credentials`;
   `shelf init expose` removed
4. Admin UI: `/connections`, the choices on the add form and the app page; golden files,
   including the add form, which had none
5. Platform; smoke scripts: `apps.sh` checks the copy of `registry-anonymous`, `tenant.sh` adds
   through a connection with no Docker config and saves the login again, `expose.sh` becomes
   `just smoke-expose <app> <domain>` through a connection it removes at the end
6. Level 2 in the dev cluster, then level 3 against Cloudflare

**Acceptance:** an app with a registry connection pulls a private artifact and private images
with it, and a new login for the connection keeps it pulling; an app with a Cloudflare connection
answers over HTTPS under its own domain through a tunnel of its own in the connection's account,
with cloudflared in its namespace; moving it to another domain or connection and taking it off
the internet leave no record and no tunnel behind; a connection in use can neither be removed nor
moved to another account; the Cloudflare token is in no Secret, no log line and no page; a
cluster from before this phase is migrated by `init cluster` with its apps still pulling.

Results so far (2026-09-29):

- Level 1 is green (`just test`). The CLI tests cover the connections (define, update, list,
  refuse removal and an account change while in use, refuse a token Cloudflare rejects) and every
  path of the exposure against a fake Cloudflare API that keeps tunnels per account: first
  exposure, reuse on the next deploy, a new domain in the same account (record moves, tunnel
  stays), a connection in another account (new tunnel, old one deleted), `--no-cloudflare`, a
  tunnel without credentials in the cluster (replaced), a machine without the connection (nothing
  touched, leftovers named), `app rm`. The server tests check the connection forms as jobs and
  that no page, refused form or job log carries a token.
- `.example` is reserved (RFC 2606), so `PublicDomain` refuses it, and the first version of the
  tests exposed apps under `shop.example` — which is exactly what shelf must not allow. The tests
  use real TLDs.
- The templates in the ResourceSet are strings to kubeconform, so they were rendered offline with
  `text/template`, the operator's `<< >>` delimiters and `missingkey=error`, with and without a
  registry connection and a tunnel.
- The choices in the forms: `internal/github` is tested against a fake API (paging, a package
  name with a slash, a next page on another host is not followed, errors without the token), the
  Cloudflare zones page through `result_info` and are filtered by account, the three CLI lists run
  against fakes, and the server tests pin the JSON routes (items, the reason as a 502, 401 without
  a session). `form.js` itself has no automated test; it is part of the level-3 check with real
  connections.
- Not run yet: level 2 and 3. The dev cluster still serves `greeter-dev.tobile.ch` through the
  shared tunnel of Phase 5; the migration switches that off, and exposing the app again needs the
  Cloudflare token. That is the maintainer's call, not a side effect of a test.

### Phase 8d – Quick tunnels
An app without a Cloudflare connection used to be reachable from inside the cluster only. It now
can ask for a quick tunnel at a random trycloudflare.com name; it stays private otherwise. The decisions are in
the table "Decided on 2026-09-29 (Phase 8d)" above.

Design:

- **Provider input `quick`** (bool) next to `tunnel`; at most one of them is set.
  `cluster.AppOptions`, `AppConfig` and `AppState` carry it, `AppState.QuickURL` the current name.
- **Platform**: `resourcesTemplate` renders the Secret and the ConfigMap only for a named tunnel,
  and the cloudflared Deployment for either; for a quick tunnel it runs
  `tunnel --no-autoupdate --metrics 0.0.0.0:2000 --url http://traefik… --http-host-header <app host>`.
- **`internal/cluster`**: `waitForTunnel` waits for a cloudflared that runs a quick tunnel
  (`--url` in its arguments, so the one of a named tunnel does not pass), then for the rollout,
  then for a name at `/quicktunnel`, read through the pod proxy from a ready pod that is not being
  deleted. `AppStates` asks each app with a quick tunnel; one that cannot answer has no address.
- **`ops`**: `Access` has `Quick` and `Private` instead of `RemoveCloudflare`. A new app with no
  choice stays private; an existing one keeps what it has. Leaving a Cloudflare connection
  reuses the Phase 8b cleanup (record and tunnel deleted after cloudflared moved). `app()` links
  the quick name, on any domain, and the components' links follow it.
- **Admin UI**: the Cloudflare select starts with "none: inside the cluster only", the default,
  and "none: a quick tunnel …" (`@quick`, which no connection name can be); the pages say that
  the address is temporary, or that the tunnel has none yet.

**Acceptance:** an app added with `--quick` answers at the trycloudflare name shelf printed,
with its own host name in the Host header; `shelf app status` and the UI show the new name after
cloudflared restarts; `--private` stops cloudflared, `--quick` starts it again; an app without
`--quick`, and every app from before, stays private.

Results so far (2026-09-29):

- Level 1 is green (`just test`). New tests: the private default, `--quick` and a quick
  tunnel that stays on the next deploy, the moves from a named tunnel to a quick one and to
  private (record and tunnel deleted), the exclusive flags, the addresses in `app()` and its
  components, `/quicktunnel` parsing, telling the two cloudflared apart, the form choices, and the
  golden pages `dashboard-quick` and `app-quick`.
- The ResourceSet template was rendered offline for a named tunnel, a quick tunnel on the
  cluster's domain and on one of its own, and a private app; every document is valid YAML.
- cloudflared 2026.9.1 was run once in the devcontainer against a dead origin: `/quicktunnel`
  answered with the name before `/ready` turned 200, which is why the wait checks both.
- `just smoke-quick` keeps the cluster's domain and host suffix, since a quick tunnel needs
  neither, and re-installs the platform with them. Not run yet.
- Manual level 2 in the dev cluster (`tobile.ch`, suffix `-dev`): the app `honk`, added from the
  admin UI, first hung at the tunnel because the cluster still ran the old platform (see "Old
  platform, new shelf"). After `platform-push` and `init cluster` with the cluster's settings, the
  provider of `vermiet` got `quick: false` and kept its named tunnel, `honk` got cloudflared, and
  `https://<words>.trycloudflare.com/` answered with the app within seconds.
