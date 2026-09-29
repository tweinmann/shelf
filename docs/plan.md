# shelf – Mini App Platform

## Context

A Mac mini (Apple Silicon) is to run as a personal app platform, published as a GitHub product
named **shelf**. Users build their apps outside the platform (GitHub Actions) and deliver images
plus a deploy artifact to a registry. The platform watches the artifact and deploys
automatically. Apps are reachable through Cloudflare Tunnel at `<app>.<domain>`.
Learning project for platform engineering, not for production use.

**Target product, revised on 2026-09-20 (after Phase 5).** Until here shelf was a command-line
tool that the maintainer drives from a devcontainer: this document described the operator as
someone with a terminal, SSH and kubectl, and listed an overview page as out of scope. That
changes. shelf becomes an **appliance**: it installs on the Mac mini with one command and a
browser wizard, and it serves an **admin UI** in which apps are registered, changed and removed
by someone who neither sees nor needs to understand Kubernetes. Resource usage, logs and similar
follow later. The substrate stays deliberately reachable with kubectl for anyone who wants it,
and the CLI remains the expert interface — every action in the UI has a CLI equivalent, and both
call the same function. What the user still has to bring from outside does not change: a GitHub
account with a classic PAT, a Cloudflare domain with an API token, and per app a repository with
`app.yaml` plus the unchanged workflow. The registry stays the only interface between build and
platform. Everything from Phase 6 on follows from this; the decisions are in the table
"Decided on 2026-09-20" below.

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
| Ingress | **One Ingress per routed component**, all on `<app>.<domain>`; Traefik attaches middlewares per Ingress, so `stripPrefix` stays per route. The Ingress is Traefik's routing table, so it is needed despite the tunnel. (It was external-dns's source too, until Phase 5 dropped external-dns.) |
| Platform values | **`platform` block** next to the app values: `domain`, `ingressClassName` (default `traefik`), `imagePullSecret`. The ResourceSet sets it inline in the HelmRelease (Phase 4); later additions such as the host suffix go here too. |

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
| GHCR credential | **One classic PAT for the platform.** `shelf init cluster` stores it in `shelf-system`; the ResourceSet copies it into every app namespace (`copyFrom`) for images and the deploy artifact. *Revised in Phase 8b: registry connections, chosen per app.* |
| shelf in tenant CI | **Built from source** (`go install …@<ref>`) in the reusable workflow; release binaries come later. |
| Webhook receiver | **Moved to Phase 5**, where the tunnel makes it reachable and testable. Phase 4 relies on `OCIRepository` polling every minute. |

Decided in Phase 4b (2026-09-17), after the Phase 4 acceptance:

| Question | Decision |
|---|---|
| What a tenant repository must know | **As little as possible: the `app.yaml` format and one boilerplate workflow.** The image names of components built in the repository are not part of that knowledge any more. |
| Building own images | **`build: ./web` instead of `image:`.** `shelf build-plan` lists what has to be built, the workflow builds and pushes it as `ghcr.io/<owner>/<repository>-<component>`, and `shelf render --image <component>=<ref>` pins the digest. The naming convention lives in the workflow, not in shelf and not in `app.yaml`. |
| shelf in the tenant CI | **Stays, but as a release binary.** `release.yml` publishes binaries, the chart and the platform artifact per tag; the workflow downloads the pinned binary in seconds instead of building it. Rendering in the cluster was considered and rejected: it would move a tested renderer into Helm templates, lose the checks in the pull request, and rebuild digest pinning in shell, without reducing what the tenant repository knows. |
| Workflow reference | **A moving major tag** (`@v0`, later `@v1`), maintained by the release, so tenants do not track versions. |

Decided in Phase 5 (2026-09-18):

| Question | Decision |
|---|---|
| Host names | **`<app><host-suffix>.<domain>`**, with the suffix set per cluster (`-dev` in development, empty on the mini). Cloudflare's free Universal SSL covers `domain.tld` and `*.domain.tld`, but not `*.dev.domain.tld`, so a second level would need a paid certificate. The suffix keeps every host one level deep and lets both clusters share one zone. |
| DNS records | **shelf writes them itself** through the Cloudflare API: `shelf app add` publishes `<app><suffix>.<domain>` as a proxied CNAME to the tunnel, `shelf app rm` removes it, and `shelf init expose` publishes the apps that already run. external-dns was installed first and then dropped: a host name belongs to exactly one app, and apps only come and go through shelf, so the generic watcher solved a problem shelf does not have — at the price of a token with DNS rights living in the cluster. Now that token stays on the operator's machine. The cost is drift when an app is removed past shelf; `shelf init expose` reconciles again. *Phase 8b keeps the rule: the token is a Cloudflare connection in `~/.shelf/connections/cloudflare/`, chosen per app; the next `shelf app add` of an app reconciles its record.* |
| Tunnel | **Locally managed, created through the API** by `shelf init expose`, named `shelf<host-suffix>`. shelf generates the tunnel secret, keeps the credentials in the cluster, and never stores them elsewhere. A tunnel that exists without credentials in the cluster is replaced after asking, because Cloudflare hands out the secret only once. *Revised in Phase 8b: one tunnel per app, `shelf<host-suffix>-<app>`, in the account of the app's Cloudflare connection; `shelf init expose` is gone.* |
| Exposure is optional | The platform carries cloudflared in a **ResourceSet that stays empty** until `shelf init expose` creates its input provider, so a cluster runs unexposed until it is exposed. *Revised in Phase 8b: exposure is per app; the ResourceSet `apps` runs cloudflared in the namespace of an app that has a tunnel.* |
| Webhook receiver | **Dropped, not moved again.** Polling reaches the app in about 80 s end to end; a receiver would need a public endpoint with a token and two secrets in every tenant repository, which is the knowledge Phase 4b removed. |
| Test DNS isolation | **One zone for both clusters**, separated by the host suffix (`greeter-dev.<domain>` next to `greeter.<domain>`). The open question was whether a shared zone is safe; with external-dns it would have needed `--txt-owner-id` and `--domain-filter` per cluster, because each instance deletes records it considers orphaned. shelf only ever touches the record of the app it is working on, so a shared zone needs nothing else — and one zone keeps the free certificate, which covers `*.<domain>` but not a second level. |

Decided on 2026-09-20, when the target product changed (see "Context"):

| Question | Decision |
|---|---|
| Where the admin UI runs | **A host service on the mini**, not a workload in the cluster: the same `shelf` binary in server mode, started by launchd as the logged-in user. It uses the kubeconfig, the secret backup and the tokens that live there anyway, it can start and stop Colima, and it still shows something when the VM or the cluster is broken. No container image, no new RBAC, no DNS-capable token in the cluster — the Phase 5 decision stays true word for word. A cluster component was considered and rejected: it would need the first container image built from this repository, a ServiceAccount with far-reaching rights, and it disappears exactly when it is needed most. |
| Access | **LAN with a login**: `http://shelf.local:<port>` from the home network, the password set during setup. No admin endpoint on the internet. |
| Installation | **One command, then a browser wizard.** `install.sh` downloads the release binary, verifies the checksum and registers the service; domain, tokens and cluster are done in the browser. No Apple Developer account, no signing, no notarization, no `.pkg` — a binary fetched with `curl` carries no quarantine attribute, so Gatekeeper does not block it. That makes the one-liner the supported path, and a tarball downloaded in a browser needs `xattr -d com.apple.quarantine`. |
| What "edit" means | The UI changes the **artifact and the tag** (which is also how a rollback works), triggers a redeploy, shows and rotates secrets, and removes apps. The `app.yaml` stays in the tenant repository, where it is versioned and where the CI builds from it; the UI only displays it and links the commit that is running. |
| Apps without CI | **Not offered.** An app still comes from a deploy artifact built by the tenant's CI. Letting the UI invent an app.yaml and push an artifact itself would make shelf write to the registry and would give an app two sources of truth. |
| Frontend | **`html/template` with `go:embed`**, plus about a hundred lines of plain JavaScript for the job log. Rendered pages are byte-comparable with the golden-file helper this repository already uses, the devcontainer needs no Node toolchain, `release.yml` needs no new step, and everything still ships as one binary — as the embedded Flux Operator manifest already does. The cost is honest: forms and full page loads instead of a single-page app. |
| Dependencies | **None added.** `net/http` (method patterns in `ServeMux`), `html/template`, `crypto/rand.Text`, `crypto/pbkdf2`, `crypto/subtle` and `http.CrossOriginProtection` cover the server; the module is on Go 1.27.1. `k8s.io/client-go` is already a dependency, so pod logs cost nothing either. Same habit as `internal/cloudflare`, which is an API client without an SDK. |
| Password | **PBKDF2-SHA256**, 600 000 iterations, 16-byte salt, stored as `pbkdf2-sha256$600000$<salt>$<hash>` and compared with `subtle.ConstantTimeCompare`. argon2id is the better primitive but costs `golang.org/x/crypto`; the algorithm tag in the hash keeps that upgrade open. |
| Sessions | **Server-side ids**, persisted in `admin.yaml` so that a restart or a self-update does not log everyone out. Cookie `HttpOnly`, `SameSite=Lax`, 30 days sliding. Revocation and "log out everywhere" stay trivial. |
| TLS on the LAN | **None.** A self-signed certificate teaches the user to click through browser warnings, and ACME needs a public name, which contradicts "no admin endpoint on the internet". The documented alternative is `listen: loopback` plus `ssh -L`. The risk is written down rather than hidden: the admin password crosses the home network in the clear. |
| Long operations | **One job at a time, globally.** A second mutating request gets 409 with a link to the running job. Two `init cluster` runs would fight over the same objects. Progress reaches the browser as Server-Sent Events, and a job survives a page reload because its events are kept in memory. |
| `shelf.local` | Comes from `scutil --set LocalHostName shelf` alone. The installer **asks** instead of renaming someone's Mac silently, and otherwise prints the URL with the current name plus the IP address. Bonjour advertising (`_http._tcp`) creates no name and is skipped. |

What this changes about decisions taken earlier:

- **The secret backup now lives on the same machine as the cluster.** It was insurance against
  losing the cluster while sitting on a different machine. It still covers the common case (the
  Colima profile is recreated, the cluster is rebuilt), but no longer disk failure or theft, and
  it now sits next to the tokens. It stays, and gets `shelf secrets export`, a download button in
  the UI and a line on the wizard's last screen saying to copy `~/.shelf` somewhere else.
  `SHELF_HOME` already allows moving the whole tree to an encrypted volume.
- **The Cloudflare and GHCR tokens now live on the mini**, as 0600 files under a 0700 directory.
  (Phase 8b: the Cloudflare tokens are connections in `~/.shelf/connections/cloudflare/`; the GHCR
  logins are connections too, and live in the cluster, where the pulls need them.)
  The Phase 5 decision — no token with DNS rights inside the cluster — holds unchanged: the token
  is a file on the host, not a Kubernetes Secret, and no shipped workload can reach the API server
  or the host filesystem. What changes is the separation. The tokens used to sit on the MacBook
  while the apps that answer requests from the internet ran on another machine; now the mini does
  both. That does not make the mini open: cloudflared dials out, no port is opened towards the
  internet, and the only port shelf listens on is the admin UI on the LAN. But whoever escapes a
  tenant pod now has two boundaries left (the container and the Lima VM) instead of a different
  machine. Plainly: anyone with a shell as that user owns the cluster, the DNS zone and the
  registry token.
- **The guardrail "the platform never talks to Git or forge APIs" is about the cluster.** The host
  service may call the GitHub API for convenience — checking for a newer release — as long as no
  part of the deploy path depends on it.
- **The kubectl escape hatch is documented, not hidden.** An "Advanced" page shows the kubeconfig
  path and offers a download behind a re-entered password, and the README explains what shelf owns
  in the cluster: platform objects belong to Flux and are reverted when edited by hand, the input
  providers belong to shelf, everything else belongs to the user.

Decided in Phase 8b (2026-09-28, revised 2026-09-29), when credentials moved from the platform to
connections the apps choose:

| Question | Decision |
|---|---|
| Scope | **No platform-wide credentials.** The shared PAT, `shelf init expose` and the shared tunnel are gone. Registry logins and Cloudflare tokens are **connections**: defined once, by name, and chosen per app. An app without a registry connection reads its registry anonymously (public packages, the dev registry); an app without a Cloudflare connection runs inside the cluster only. The first cut (2026-09-28) gave every app its own token directly; the maintainer asked the next day for connections, so that a token is typed once, a new one reaches every app that uses it, and the UI offers a choice instead of password fields. |
| Isolation | **Apps share only what the user assigns to several of them: a connection.** Namespace, secret values, tunnel and cloudflared stay every app's own. The shared login and tunnel before this phase were shared by the platform, without anyone choosing it — one tenant's token pulled every tenant's packages. |
| Registry connection | **In the cluster, `shelf-system/connection-registry-<name>`** (dockerconfigjson, label `shelf.dev/connection=registry`). The ResourceSet copies it into the namespace of every app that chooses it, and an app without one gets `shelf-system/registry-anonymous`, which holds no login, so the copy always has a source. The cluster pulls with it anyway, and a new login reaches every app at once through the watch label. shelf reads the artifact with the same login, so the machine running shelf needs no Docker config. |
| Cloudflare connection | **Token and account, in `~/.shelf/connections/cloudflare/<name>.yaml`** (0600 in a 0700 directory, through `internal/hostcfg`), never in the cluster. The Phase 5 rule holds: nothing in the cluster needs the token, and a pod that escapes into the VM does not reach it. The price is that only the machine holding the file can expose, move or withdraw apps through it — in practice the mini. Without the file nothing fails silently: shelf leaves record and tunnel as they are and says so, and `shelf connection add cloudflare <name>` defines it again. A Secret in `shelf-system` was considered and rejected: anyone who can read that namespace would own the zones of all apps. |
| An app in a Cloudflare account | **A tunnel of its own, and a domain chosen per app.** A proxied CNAME cannot point at a tunnel in another account, so a tunnel belongs to the account of the app's connection (`shelf<host-suffix>-<app>`), and the app's domain has to be a zone of that account. One tunnel per connection was considered and rejected: the apps would share their way to the internet, and cloudflared would have to live outside their namespaces. The admin UI offers the zones the connections can see; the host is `<app><host-suffix>.<domain>`. |
| Rules for a connection | Defining one under an existing name gives it a new token. A Cloudflare connection **cannot move to another account** while apps use it, because their tunnels live in the old one. A connection **cannot be removed** while apps use it; the refusal names them. |
| The cluster's domain | **Stays, as the default.** `init cluster --domain` is where an app without a domain of its own answers; the host suffix stays per cluster, so the dev cluster never takes the mini's names. `--move-hosts` now only counts the apps a change actually moves: a new suffix moves all of them, a new domain those on the cluster's. |
| How it is given | **`shelf connection add registry|cloudflare <name>`** reads `GHCR_USERNAME`/`GHCR_TOKEN` or `CF_API_TOKEN`/`CF_ACCOUNT_ID`; `shelf connection list` and `rm` go with it. `shelf app add` and `shelf app credentials` take `--registry <name>`, `--cloudflare <name>` and `--domain`, and `credentials` also `--no-registry` and `--no-cloudflare`; they read no tokens at all. In the admin UI connections are defined **only on the page `/connections`**, the one page with token fields; the add form and the app page offer them as a choice. A token is never rendered, not even back into a form that was refused. |
| Phase | **A phase of its own, 8b**, before the Mac mini. Phase 8 is accepted as it was. |
| Choices in the forms (2026-09-29) | **The forms offer what a connection can reach, as real choices.** The add form lists the deploy artifacts of the registry connection's user and their tags (newest first), and the zones in the account of the Cloudflare connection; the app page lists the tags of the app's own artifact, so going back is a choice. The packages and tags come from the **GitHub API** (`/user/packages`, the versions of a package) with the connection's token — a classic PAT with `read:packages` is enough. That is the host service talking to a forge for convenience, which the guardrail allows: the form still sends a plain `oci://…:tag`, parsed and checked as before, and when a list cannot be read, or there is no connection, the text field is shown as it always was. Only the user's own packages are listed; the images of the components (`<name>/<component>`) and signature tags (`sha256-…`) are left out. The domain is a select with the cluster's domain, the zones and "other…", which keeps a subdomain of a zone or a LAN-only domain possible. A small `form.js` follows a change of connection through three JSON routes under `/api/connections/`; without JavaScript the page is the one it was. The same lists are `shelf connection packages <registry>`, `shelf connection zones <cloudflare>` and `shelf app tags <app>`. Packages of an organisation are not listed yet. |

Tried and reverted on 2026-09-29: **app.yaml without a name** (the short-lived Phase 8c). shelf
named an app only at `shelf app add`, so one artifact could run as several apps, and the reusable
workflow named the packages after the repository (input `package`). It was released as v0.3.0 and
reverted the same day at the maintainer's request: `name` is required in app.yaml again, it names
the packages (`ghcr.io/<owner>/<app>`, `…/<app>/<component>`), and `shelf app add` refuses an
artifact for another app, as before. What stayed is a fix to `release.yml` that the release
exposed: the release became "latest" at once, the major tag moved a minute later, and a tenant
build in between ran the old workflow with the new binary. The release is now created with
`--latest=false` and marked latest right after the major tag moves.

Decided on 2026-09-29, after the revert, when the maintainer asked to decouple app.yaml's name
from the app's name in shelf:

| Question | Decision |
|---|---|
| What `name` in app.yaml means | **The package name, and nothing else.** It stays required and keeps its rules (DNS label, at most 40 characters); the workflow publishes `ghcr.io/<owner>/<name>` and `…/<name>/<component>` as before, so a tenant repository changes nothing. It no longer has to avoid platform namespaces: a package may be called `traefik`. |
| The app's name | **Chosen at `shelf app add <app>`**, checked by `ops.CheckAppName`, which now also refuses the platform's namespaces (`default`, `flux-system`, `traefik`, `cloudflared`, `kube-*`, `shelf-*`). `shelf app add` no longer refuses an artifact whose package has another name, so one artifact can run as several apps — the goal of the reverted Phase 8c, without taking the name out of app.yaml. |
| The ConfigMap in the artifact | **`shelf-values` in every app**, without labels; the deploy Kustomization labels it with the app. The HelmRelease reads it through `valuesFrom` and passes `name: << inputs.name >>` as `values`, which win over the package name in the resolved app.yaml. |
| Artifacts from before | **Refused with a hint**: an artifact whose ConfigMap is still `<name>-values` was rendered by a shelf older than v0.5.0 and must be pushed again. No transition code; a running app stays on its last revision until its tenant pushes. |
| `shelf build-plan` | **Prints `{package, builds}`** instead of `{app, builds}`, and the workflow reads `.package`. The fix to `release.yml` switches workflow and binary together. |
| Phase | No phase of its own: a change to Phase 8b's conventions, released as v0.5.0. |

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
3. **DNS for a tunnel**: a record that points at `<tunnel-uuid>.cfargotunnel.com` only works
   when it is proxied; the name does not resolve on its own, and the certificate comes from
   Cloudflare. shelf therefore writes proxied CNAMEs (originally this was external-dns with the
   annotations `external-dns.kubernetes.io/target` and `.../cloudflare-proxied`; see the Phase 5
   decisions for why it was dropped).
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

## Architecture

### Runtime (Mac mini, Phase 9 onward)
Colima profile `shelf`:
`--vm-type vz --vz-rosetta --runtime containerd --kubernetes
 --kubernetes-disable traefik --kubernetes-disable servicelb`.
Storage: local-path on the VM disk.
In development, k3d plays the same role — see the development environment above.

> **local-path does not enforce capacity.** The `size` field in the schema is documentation,
> not a limit — a pod can fill the VM disk. This must be stated in the docs.

### Components on the mini (Phase 7 onward)
Everything shelf itself runs on the host is one binary in two modes. The CLI is one invocation;
`shelf serve` is the long-running one, started by launchd.

| Concern | Building block |
|---|---|
| Admin UI and its API | `shelf serve`, an HTTP server with embedded templates, bound to the LAN |
| Autostart | LaunchAgent `dev.shelf.agent`, `RunAtLoad` + `KeepAlive` |
| VM | Colima, started and stopped by the service |
| Host setup | `shelf init host` (brew, `pmset`, Colima profile, host name) |

The service runs **as the logged-in user, not as root**: Colima is per-user (`~/.colima`,
`~/.lima`), and so are the kubeconfig, the secret backups and the tokens. A root daemon running
`colima start` would silently create a second VM under root's home. No secret ever goes into the
plist, which is world-readable.

`shelf serve` at startup loads its configuration, serves nothing but the claim page while the
instance is unclaimed, and binds its listener. Every page then reads the cluster when it is
asked for, under a timeout of a few seconds, so a cluster that does not answer becomes a page
that says so instead of a request that hangs — which is what keeps the UI useful when the VM is
down. (Phase 7 built this with a timeout rather than the background poller this paragraph
described first: the poller would be a second source of truth, and its staleness would have to
be shown on every page.) With autostart enabled the service also brings Colima up at login,
which is what makes the platform survive a power cut.

State on disk, all under `~/.shelf` (0700), each file 0600:

```
bin/shelf                  the real binary; /usr/local/bin/shelf is a symlink to it
config.yaml                domain, host suffix, listen address, Colima profile, autostart
admin.yaml                 password hash, sessions, and the setup token until the claim
apps/<app>/secrets.yaml    the secret backup, unchanged since Phase 4
connections/cloudflare/    one file per Cloudflare connection: token and account (Phase 8b)
audit.log                  one line per mutating action
```

Plain files rather than the macOS keychain: keychain ACLs are bound to a code-signing identity,
so an unsigned binary replaced on every self-update would re-prompt — a dialog nobody is sitting
in front of on a headless machine. `SHELF_HOME` moves the whole tree, for anyone who wants it on
an encrypted volume.

### Cluster components
| Concern | Building block |
|---|---|
| GitOps | Flux, installed via Flux Operator (`FluxInstance`) |
| App rendering | Helm chart `shelf-app`, rendered by helm-controller |
| App registration | Flux Operator `ResourceSet` + `ResourceSetInputProvider` |
| Ingress | Traefik (Helm, ClusterIP — no LoadBalancer) |
| Exposure | `cloudflared` in-cluster, one catch-all rule pointing at Traefik |
| DNS | one proxied CNAME per app, written by the CLI through the Cloudflare API |

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
              ResourceSet ──────────────► HelmRelease           ConfigMap shelf-values
                   (values: name = the app)    │  valuesFrom ◄──────────┘
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
2. An OCI artifact `ghcr.io/<owner>/<package>`; images built in the same repository are pushed as
   `ghcr.io/<owner>/<package>/<component>`. The package is `name` in app.yaml; it names the
   artifact, not an app, which is named at `shelf app add` — one artifact can become several apps
   - Content: the ConfigMap `shelf-values` (no namespace, no labels) holding the resolved
     `app.yaml` under the key `app.yaml`
     - images pinned by digest
     - `${<component>.host}` / `${<component>.port}` / `${<component>.ports.<name>}` substituted
     - `${secrets.*}` left unresolved (resolved by the chart)
   - Tags: moving `main`, immutable `sha-<shortsha>`
   - Annotations: `org.opencontainers.image.{source,revision,created}`, custom ones under
     `dev.shelf.*`
3. A call to the Flux webhook receiver (from Phase 4; before that, `OCIRepository` polling every
   1m is sufficient)

Unknown `apiVersion` → the artifact is rejected, and so is an artifact whose ConfigMap is not
`shelf-values` (rendered before v0.5.0).

## Schema `shelf.dev/v1alpha1`

```yaml
apiVersion: shelf.dev/v1alpha1
name: shop                  # the package name, not the app's name in shelf

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
- `image` — a ready-made image; or `build: ./web`, a directory of the tenant repository that the
  CI builds. Exactly one of the two. `shelf build-plan` lists the build directories, and
  `shelf render --image <component>=<reference>` takes the pushed image back. The image name of
  a built component is the workflow's convention (`ghcr.io/<owner>/<repository>-<component>`),
  so it never appears in `app.yaml`
- `command`, `args`, `env` — as in Kubernetes; `env` is a map, and scalar values
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

Name rules: package and app names are DNS-1123 labels, component names DNS-1035 labels, all at
most 40 characters so suffixes (tunnel names, StatefulSet pod names, revision hashes) still fit
into 63. The app name, given at `shelf app add` and checked by `ops.CheckAppName`, must not be a
platform namespace (`default`, `flux-system`, `traefik`, `cloudflared`, `kube-*`, `shelf-*`).
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
- `shelf init host` / `shelf init`
- `shelf app add <name> <oci://…:tag> [--insecure-registry] [--domain …] [--registry <conn>]
  [--cloudflare <conn>]` / `shelf app rm <name>`, both with `--context`, `--kubeconfig`,
  `--timeout`; `rm` asks unless `--yes`
- `shelf app credentials <name> [--domain …] [--registry <conn>] [--cloudflare <conn>]
  [--no-registry] [--no-cloudflare]` — change an app's domain and connections, nothing else (Phase 8b)
- `shelf connection add registry|cloudflare <name>` / `list` / `rm registry|cloudflare <name>` —
  the connections apps choose; tokens from the environment only (Phase 8b)
- `shelf app status <name>` — the diagnosis for one app: the first stage that is not healthy,
  with its message and a hint in plain language
- `shelf doctor` — preflight plus runtime (VM, tunnel, Flux status), reporting per layer. It is
  the same diagnosis engine the dashboard renders, only as text
- `shelf destroy` — remove profile, tunnel and DNS records. Stays **CLI-only**, including
  `--purge`: a "delete everything" button behind one LAN password is a bad trade, and the
  "back to vanilla" test on M1/M2 hardware needs it reliable, not convenient
- `shelf serve` — the admin UI and its HTTP API (Phase 7 onward)
- `shelf service install|uninstall|status` — the launchd agent (Phase 10)
- `shelf secrets export <name>` — the secret backup as a file, for keeping a copy off the machine

The rule for everything above: **every action the UI offers exists as a command, and both call
the same function in `internal/ops`.** The UI is a second face on one operations layer, never a
second implementation.

## Repository layout

```
shelf/
  CLAUDE.md                   working agreements for Claude (see below)
  .devcontainer/
    devcontainer.json         Docker-in-Docker, volumes, remoteEnv
    Dockerfile                Go base + pinned tool binaries (single source of versions)
    ssh_config                template for mini access (Phase 9)
  hack/
    lib.sh                    shared helpers, require_devcontainer guard
    cluster-up.sh             create/start dev cluster and registry, write kubeconfig
    cluster-down.sh           delete dev cluster and registry
    platform-push.sh          push platform/ to the dev registry
    chart-push.sh             push the chart to the dev registry
    nuke.sh                   host-side cleanup by exact name (POSIX sh)
    smoke/                    smoke tests (Phase 0, chart in Phase 2, init in Phase 3, apps and
                              tenant in Phase 4)
  install.sh                  one-command install on the mini (POSIX sh, Phase 10)
  cmd/shelf/                  CLI entry point
  internal/
    cli/                      cobra commands: flags, prompts, text output
    ops/                      the operations both the CLI and the server call (Phase 6)
    progress/                 typed progress events, rendered as text or streamed to a browser
    server/                   shelf serve: routes, auth, sessions, jobs (Phase 7)
      ui/                     html/template files and static assets, embedded
    hostcfg/                  ~/.shelf: config, admin file, Cloudflare connections
    schema/                   app.yaml types, parser, ${…} syntax, JSON Schema generation
    validate/                 validation rules
    render/                   app.yaml → resolved app.yaml → ConfigMap manifest; registry lookup
    chart/                    helm template golden-file tests for charts/shelf-app
    secrets/                  secret value generation, merge, host backup
    deploy/                   reading a deploy artifact from the registry
    testutil/                 golden-file helper
    preflight/                host checks
    host/                     brew, pmset, colima, launchctl — behind a command-runner interface
    cloudflare/               tunnel and DNS API
    cluster/                  shelf init cluster: embedded Flux Operator manifest, FluxInstance,
                              server-side apply and readiness waits
  charts/shelf-app/           the generic app chart
  platform/                   Flux-managed platform manifests (→ OCI artifact)
    traefik/                  Phase 3
    apps/                     the ResourceSet for apps (Phase 4)
  .github/workflows/
    ci.yml                    level-1 checks and level-2 smoke tests in the devcontainer image
    build.yml                 reusable tenant workflow (build-plan, render, push artifact)
    release.yml               CLI binaries, chart and platform artifact per tag, major tag
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
Phase 1, `just push-mini` in Phase 9).

## Phases

Stop after each phase, show the result, wait for approval.
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

### Phase 9 – Mac mini: host, Colima, doctor, destroy
The old Phase 6, without the installer. `shelf init host`: preflight (Apple Silicon, RAM, disk,
macOS version, Rosetta, Homebrew, tool versions, existing profile, energy settings), tool
installation, disable sleep, host name, Colima profile. Plus `shelf doctor` on top of
`ops.Diagnose`, `shelf destroy [--purge]`, the `shelf init` wrapper, `just push-mini`, and the SSH
setup above.
Since the devcontainer is Linux, `internal/host` cannot run there for real: it is tested with a
fake command runner (expected `brew`/`pmset`/`colima` calls and their order) and executed for
real only on the mini.

**Acceptance:** one command on the mini brings Colima and the platform up; `shelf doctor` reports
every layer and names the first broken one; `shelf destroy --purge` followed by `shelf init`
returns to a working platform; kubectl from the devcontainer through the SSH forward reaches the
mini; the experiment on starting Colima from a LaunchAgent without a GUI login has been run and
its result is recorded here.

### Phase 10 – Service and installer
`shelf service install|uninstall|status` for the LaunchAgent, and `install.sh`: download the
release binary, verify the checksum, install it to `~/.shelf/bin` with the symlink in
`/usr/local/bin`, offer the host name, set `pmset`, allow the binary through the application
firewall, register the service, print the URL and the setup token. Autostart after a power cut
falls out of the same chain.

**Acceptance:** the one-liner on an untouched user account ends with a URL that opens the claim
page from another device on the LAN; pulling the power and restoring it brings shelf and the
cluster back with no keyboard attached; `shelf service uninstall` leaves no loaded agent and no
plist behind; the `xattr` workaround for a tarball downloaded in a browser is documented.

### Phase 11 – The wizard
Claim → host preflight → Colima → the cluster's domain → cluster as one job → the connections (a
registry login and a Cloudflare token, each checked when it is entered) → the first app, choosing
them and its domain (the zone picked from the Cloudflare API rather than typed), with the two
files the tenant repository needs offered for copying, `<owner>` already filled in.

**Acceptance:** someone who has never seen Kubernetes gets from the one-liner to a reachable
`https://<app>.<domain>` without opening a terminal, given only a registry PAT, a Cloudflare token
and a tenant repository; re-running the wizard on a configured host changes nothing and shows the
current state; a wrong token is rejected on the step that collects it, naming the permission that
is missing.

### Phase 12 – Operations: logs, usage, self-update
Live pod logs including the previous container of a crash-looping pod, instantaneous CPU and
memory per pod, a warning when the VM disk fills up (local-path enforces no quota, so one app can
fill it), "update shelf" as one job that moves the binary, the platform tag and the chart version
together, and the Advanced page with the kubeconfig.

**Acceptance:** a crash-looping app shows its previous container's log in the UI; the dashboard
warns above 85 % VM disk usage; an update from one release to the next runs from the browser and
the three versions shown afterwards agree; the kubeconfig download is refused without a re-entered
password; every UI action has a documented CLI equivalent.

### Phase 13 – Reference apps
`examples/hello` and `examples/tenant`, then the maintainer's own apps as the first real
tenants.

## Open items

Due in Phase 9:
- **Mini hardware** (chip, RAM, macOS version) determines Colima sizing defaults and whether the
  "vanilla Mac" first run can be tested in a `tart` VM. Apple's Virtualization framework only
  supports nested virtualization from M3 and macOS 15; on M1/M2 the substitute is a tested
  `shelf destroy --purge` ("back to vanilla" instead of "start from vanilla"). Until the hardware
  is known, the wizard proposes half the CPUs, half the RAM and 60 GB of disk, all editable.
- **Does Colima's `vz` VM start from a LaunchAgent without a GUI login?** The highest-impact
  unknown of the new plan, because it decides whether automatic login is required: an agent in
  `~/Library/LaunchAgents` loads when a user session is created, and a headless mini has nobody to
  log in. Plan: ship automatic login as the documented path, run the experiment with
  `launchctl bootstrap user/<uid>` from a daemon in Phase 9, and drop automatic login only if it
  demonstrably works without it. Automatic login in turn requires FileVault to be off, so the disk
  holding the tokens and the secret backups is unencrypted — accepted for a home appliance whose
  threat model is the LAN, but it belongs in the documentation, not in a footnote.
- **The macOS application firewall** shows a GUI dialog when an unsigned binary opens a listening
  socket — and nobody is sitting in front of a headless mini. `install.sh` adds the binary with
  `socketfilterfw --add` while it still has sudo, and `shelf doctor` checks the state. Easy to
  miss, unpleasant to debug remotely.

Later:
- **Packages of an organisation** in the choices of the forms: `/user/packages` lists the
  user's own only. An organisation's would need `/orgs/<org>/packages` and a way to know which
  organisations to ask (`read:org`, or the owner typed once).
- **Short 504 during a rollout** (seen once in Phase 4, not reproduced in Phase 5): Traefik
  answered 504 for a moment while a single-instance app was replaced. Worth a look under real
  traffic on the mini.
- MongoDB with auth + replica set needs a keyfile → the schema would need secrets as file mounts
- Build via `path`, CronJobs, init/migration jobs, `${app.url}`, secret rotation

## Explicitly not in the MVP

Per-branch environments, scale-to-zero, supply-chain security (signatures, scans, policies),
own registry, backups, seed data, devcontainer integration *for tenant apps* (the devcontainer
for shelf itself is part of Phase 0), catalog of managed services.

Added on 2026-09-20 with the admin UI: more than one user or any notion of roles, admin access
from the internet, TLS on the LAN, a stable public HTTP API (the JSON under `/api/` serves the
UI and may change freely), code signing, notarization and `.pkg` installers, metrics history and
graphs, log retention and search, editing `app.yaml` in the UI, and apps without a tenant
repository.

Removed from this list on the same day: an **overview page** — that is the product now — and
**autostart after power loss**, which is a goal of Phase 10. Monitoring stays out except for the
instantaneous numbers in Phase 12: Prometheus on a 16 GB mini would be a second platform.
