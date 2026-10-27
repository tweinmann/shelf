# Design

Moved verbatim from `docs/plan.md` on 2026-10-20, when the plan was retired in favour of OpenSpec
(change `retire-plan-md`). Nothing in the moved text was changed.
Decisions made since live in the `design.md` of the change that made them.

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
| Scope | **No platform-wide credentials.** The shared PAT, `shelf init expose` and the shared tunnel are gone. Registry logins and Cloudflare tokens are **connections**: defined once, by name, and chosen per app. An app without a registry connection reads its registry anonymously (public packages, the dev registry); an app without a Cloudflare connection runs inside the cluster only (since Phase 8d: or behind a quick tunnel, if it asks for one). The first cut (2026-09-28) gave every app its own token directly; the maintainer asked the next day for connections, so that a token is typed once, a new one reaches every app that uses it, and the UI offers a choice instead of password fields. |
| Isolation | **Apps share only what the user assigns to several of them: a connection.** Namespace, secret values, tunnel and cloudflared stay every app's own. The shared login and tunnel before this phase were shared by the platform, without anyone choosing it — one tenant's token pulled every tenant's packages. |
| Registry connection | **In the cluster, `shelf-system/connection-registry-<name>`** (dockerconfigjson, label `shelf.dev/connection=registry`). The ResourceSet copies it into the namespace of every app that chooses it, and an app without one gets `shelf-system/registry-anonymous`, which holds no login, so the copy always has a source. The cluster pulls with it anyway, and a new login reaches every app at once through the watch label. shelf reads the artifact with the same login, so the machine running shelf needs no Docker config. |
| Cloudflare connection | **Token and account, in `~/.shelf/connections/cloudflare/<name>.yaml`** (0600 in a 0700 directory, through `internal/hostcfg`), never in the cluster. The Phase 5 rule holds: nothing in the cluster needs the token, and a pod that escapes into the VM does not reach it. The price is that only the machine holding the file can expose, move or withdraw apps through it — in practice the mini. Without the file nothing fails silently: shelf leaves record and tunnel as they are and says so, and `shelf connection add cloudflare <name>` defines it again. A Secret in `shelf-system` was considered and rejected: anyone who can read that namespace would own the zones of all apps. |
| An app in a Cloudflare account | **A tunnel of its own, and a domain chosen per app.** A proxied CNAME cannot point at a tunnel in another account, so a tunnel belongs to the account of the app's connection (`shelf<host-suffix>-<app>`), and the app's domain has to be a zone of that account. One tunnel per connection was considered and rejected: the apps would share their way to the internet, and cloudflared would have to live outside their namespaces. The admin UI offers the zones the connections can see; the host is `<app><host-suffix>.<domain>`. |
| Rules for a connection | Defining one under an existing name gives it a new token. A Cloudflare connection **cannot move to another account** while apps use it, because their tunnels live in the old one. A connection **cannot be removed** while apps use it; the refusal names them. |
| The cluster's domain | **Stays, as the default.** `init cluster --domain` is where an app without a domain of its own answers; the host suffix stays per cluster, so the dev cluster never takes the mini's names. `--move-hosts` now only counts the apps a change actually moves: a new suffix moves all of them, a new domain those on the cluster's. *Revised in Phase 8d: a cluster has no domain.* |
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

Decided on 2026-09-29 (Phase 8d), when the maintainer asked how apps could be reached without a
Cloudflare account of their own:

| Question | Decision |
|---|---|
| LAN access | **Not built.** Considered: the host service proxies port 80 on the LAN to a NodePort of Traefik, and answers mDNS for `<app>.shelf.local`. It would have needed a second port on the LAN, an mDNS responder, a second host name per Ingress, and it cannot be tested beyond the devcontainer, because Docker Desktop does not pass multicast to the real network. Rejected in favour of keeping tunnels the only way in. |
| An app without a Cloudflare connection | **Stays inside the cluster by default, and can ask for a quick tunnel** (`--quick`, UI: "none: a quick tunnel …"): cloudflared without an account asks trycloudflare.com for a random `https://<words>.trycloudflare.com` name. No token, no zone, no DNS record, no port on the mini, and it works on a reserved domain such as `dev.local`. The first cut made the quick tunnel the default and `--private` the choice; the maintainer asked the same day for private as the default, so that nothing reaches the internet without being asked for. |
| Its limits | Written into the help texts and the UI rather than hidden: the name is random and **changes whenever cloudflared restarts**; the app is public to anyone who has the name; Cloudflare offers quick tunnels for trying things out, with no uptime guarantee, a limit of concurrent requests and no server-sent events. A stable name needs a domain and a Cloudflare connection, as before. |
| Routing | cloudflared rewrites the Host header to the app's own `<app><host-suffix>.<domain>` (`--http-host-header`), so Traefik routes as it does for a named tunnel and the chart stays unchanged. The app sees its own host name, not the trycloudflare one. |
| Where the name comes from | cloudflared reports it on its metrics port at `/quicktunnel` (`{"hostname": …}`, checked against `cloudflare/cloudflared:2026.9.1`). shelf reads it through the API server's pod proxy, so `internal/cluster` still talks to the API server only; nothing stores the name, every status reads it anew. `shelf app add` prints it as the detail of the wait for the tunnel. |
| Apps registered before | **Stay inside the cluster.** `init cluster` gives their providers `quick: false`, and the ResourceSet reads the input with `index inputs "quick"`, which is empty for a provider without it, where `inputs.quick` would fail the whole ResourceSet under `missingkey=error` — in the dev cluster Flux can pick up a pushed platform before `init cluster` migrates. An upgrade never puts an app on the internet. |
| Old platform, new shelf | **Refused before anything is written.** A shelf that adds a quick tunnel to a cluster whose ResourceSet does not know the input would wait for a cloudflared that never comes; `cluster.AddApp` checks the ResourceSet first and says to run `shelf init cluster` (or to add the app without `--quick`). Found in the first manual run: the job hung at "waiting for the tunnel" until its timeout. |
| Flags | `shelf app add` takes `--quick`; `shelf app credentials` takes `--quick` and `--private`, and `--no-cloudflare` means `--private`, as before. A Cloudflare connection, a quick tunnel and private exclude each other. |
| Smoke tests | Only `just smoke-quick` opens a public tunnel; the others add apps without `--quick`. |
| No cluster domain | **A cluster has no domain; a domain belongs to an app.** `init cluster --domain` is gone, and with it `SHELF_DOMAIN`. The maintainer asked for it once apps could be private or behind a quick tunnel: neither needs a domain, and the dev cluster answered every such app under `tobile.ch` without anyone choosing it. An app exposed through a Cloudflare connection **needs** a domain of its own, a zone of that account; shelf refuses it without one before anything is written. |
| An app without a domain | **Answers at `<app>.shelf.internal`**, inside the cluster only. Traefik still needs a host name to route by, and the quick tunnel sends it as the Host header. `.internal` is reserved for private use (ICANN, 2024), so `PublicDomain` already refuses it and no page links it. It carries no host suffix, which only separates public names of two clusters. |
| The host suffix | **Stays per cluster.** `--move-hosts` now only concerns a new suffix, which renames the apps with a domain; internal names never move. |
| Clusters from before | `init cluster` reads the old `SHELF_DOMAIN` once. If it is a public domain, every app without a domain of its own gets it as its own (reported per app), so no app loses its name and no DNS record is stranded. A reserved one such as `dev.local` is not kept; those apps become `<app>.shelf.internal`. The ConfigMap key disappears with the next apply. |
| Phase | **8d**, before the Mac mini. (8c is the name the reverted change of the same day had.) |

Decided on 2026-10-20, after Phase 8d, when the maintainer asked to retrofit
[OpenSpec](https://github.com/Fission-AI/OpenSpec):

| Question | Decision |
|---|---|
| What OpenSpec is for | **The behaviour of shelf as specs, and every phase as a change.** `openspec/specs/<capability>/spec.md` holds requirements and scenarios for what shelf does today; `openspec/changes/<name>/` holds proposal, design, delta specs and tasks of the work in progress, and moves to `openspec/changes/archive/` when the phase ends. |
| Baseline | **Seven specs, written from this plan and the code** before the first change: `app-manifest`, `app-chart`, `cluster-init`, `app-delivery`, `connections`, `app-exposure`, `admin-ui`. Where the plan and the code disagreed, the code won. Leftovers of 8b and 8d (level 2 and 3 runs, `smoke-quick`) stay in this plan and were not turned into a change. |
| This plan | **Keeps context, decisions, architecture and the roadmap.** From Phase 9 on, a phase section is the roadmap entry plus a link to its change; goals, tasks and phase-local decisions live in the change. Cross-cutting decisions still go into this section. |
| Installation | **Node and `@fission-ai/openspec` pinned in `.devcontainer/Dockerfile`**, like every other tool, with `OPENSPEC_TELEMETRY=0` and `DO_NOT_TRACK=1`. No new Docker object on the host, so the footprint inventory and `hack/nuke.sh` are unchanged. |
| Checks | `just test` runs `openspec validate --all --strict`, so CI checks the specs at level 1. |
| Claude Code | `openspec init --tools claude` writes the `/opsx:*` commands and skills into `.claude/`; they are committed and regenerated with `openspec update` after a version bump, never edited by hand. Core profile only. |

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
| Exposure | `cloudflared` in the app namespace, one catch-all rule pointing at Traefik: a named tunnel with a Cloudflare connection, otherwise a quick tunnel with the Host header rewritten |
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
  openspec/
    config.yaml               project context for proposals
    specs/                    behaviour of today's shelf, one directory per capability
    changes/                  one change per phase; archive/ holds the finished ones
  .claude/                    OpenSpec's /opsx commands and skills (generated)
  justfile
```

## `CLAUDE.md`

[`CLAUDE.md`](../CLAUDE.md) holds the working agreements for Claude: phase-by-phase workflow,
language rule, guardrails, the command table, safety rules and naming conventions. It is
deliberately short and points here for design detail. It also carries a short status section,
because a new Claude session inside the devcontainer has no memory of earlier conversations.
It is updated at the end of every phase; the command table grows with it (`just test` in
Phase 1, `just push-mini` in Phase 9). Since 2026-10-20 it also says how a phase runs as an
OpenSpec change.

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
