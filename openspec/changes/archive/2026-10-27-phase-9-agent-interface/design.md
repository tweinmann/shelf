# Design

## Context

See proposal.md for the motivation. The facts the approach rests on:

- `ops.App`, `cluster.AppState`, `cluster.Diagnosis` and `cluster.Stage` already carry `json`
  tags, because the admin UI's `/api/` serves them. That API is explicitly unstable ("`/api/`
  serves the UI and may change", context), so it is not the contract for agents.
- Flux copies the deploy artifact's OCI annotations into `status.artifact.metadata` of the
  OCIRepository. On the dev cluster on 2026-10-27, the app `greeter` shows
  `org.opencontainers.image.revision: main@sha1:42a964eb…` and
  `org.opencontainers.image.source: https://github.com/tweinmann/shelf-hello`. The reusable
  workflow sets them with `flux push artifact --revision "$GITHUB_REF_NAME@sha1:$GITHUB_SHA"`.
  So the cluster knows the commit without any forge API.
- `shelf app add` avoids the stale-status race by asking each controller to reconcile and
  waiting for the request to be handled (`rollOut`, `reconcileAndWait`). `--wait` is read-only
  (decision 4), so it needs a different way to tell "Ready for the new artifact" from "still Ready
  from the old one".
- The HelmRelease waits for its resources (Helm's wait is on by default and the platform does
  not turn it off), so a Ready release means the Deployments and StatefulSets have rolled out.
  `shelf-values` carries `reconcile.fluxcd.io/watch: Enabled`, so helm-controller reacts to a new
  ConfigMap at once.
- The deploy artifact is polled every minute (`interval: 1m` in the ResourceSet).
- Traefik is the ClusterIP Service `traefik/traefik`, port 80 → entry point `web`. cloudflared
  of every tunnel talks to it at `traefik.traefik.svc.cluster.local:80` and rewrites the Host for
  quick tunnels (`--http-host-header`).
- `cli.Execute` maps every error to exit status 1; `ErrReported` suppresses the second print.

## Goals / Non-Goals

**Goals:**
- A JSON contract an agent can rely on across releases, tested by golden files.
- A wait that gives a true answer for one specific commit, without writing to the cluster.
- Browser access to any app on the developer's machine, with routing identical to a tunnel.

**Non-Goals:**
- JSON for the mutating commands (`add`, `credentials`, `redeploy`, `rm`) or a JSON event stream
  of `progress.Event`s. Possible later as a new kind.
- A stable HTTP API. `/api/` stays the UI's own.
- Waiting for branch artifacts. Only `main` is pushed until Phase 10, so `--commit` of a branch
  commit times out. Phase 10 adds `--preview <branch>`.
- `shelf app open` for previews (Phase 10), and any access beyond the loopback interface.
- A Claude Code hook in the tenant template.

## Decisions

### 1. The JSON documents are own types in `internal/cli`, versioned as `shelf.dev/v1alpha1`

Every `--json` output is one document type with `apiVersion` and `kind`
(`AppList`, `AppStatus`, `WaitResult`, `TagList`, `ConnectionList`, `Version`, `Error`), built
from the `ops` values by explicit mapping functions in `internal/cli/json.go`. Each kind has a
golden file in `internal/cli/testdata/json/`, so a rename in `ops` or `cluster` that would change
the contract fails a test instead of reaching an agent. Field names are lowerCamelCase, times are
RFC 3339 in UTC, empty values are omitted (`omitempty`/`omitzero`), and no field is ever `null`.
An `AppStatus` nests its diagnosis as `stages`, the five stage objects in order.

The API group is the same as app.yaml's (`shelf.dev/v1alpha1`) on purpose: it is the convention
of the project (CLAUDE.md, Conventions), and the `kind` keeps the two apart.

*Alternatives:* marshal `ops.App` and `cluster.Diagnosis` directly (cheapest, but every internal
rename becomes a breaking change for agents, and the UI's needs leak into the contract; rejected
by the maintainer). A separate package such as `internal/agentapi` (more ceremony for six types
that only the CLI produces; can be split out if the server ever serves them). An unversioned
format (no way to make an incompatible change later).

### 2. Errors are documents too

With `--json`, `cli.Execute` writes an `Error` document to stdout and the plain message to
stderr. An agent then reads one place, and a human watching the terminal still sees the error.
The exit status stays 1. Cobra's own usage errors (unknown flag) happen before the command runs
and stay plain text, which is acceptable: they are a bug in the caller, not a state of the
cluster.

### 3. The commit comes from the artifact's revision annotation

`cluster.AppState` gains `Commit`, parsed from
`status.artifact.metadata["org.opencontainers.image.revision"]` of the OCIRepository `deploy`. The
value has the form `<ref>@sha1:<sha>`, and a bare 40-digit SHA is accepted too. Anything else
leaves `Commit` empty. The text output of `shelf app status <app>` gains a `commit` line, and the
JSON documents carry `commit`. This reads the cluster only, so the registry stays the only
interface between build and platform.

*Alternatives:* read the commit from the image label `org.opencontainers.image.revision` (needs a
registry call per image, and a component with `image:` has no label of ours). Ask the GitHub API
(against the guardrail).

### 4. `--wait` is a read-only poll with a "caught up" check

`ops.Wait(ctx, app, commit, timeout)` polls every 2 seconds (the existing `pollInterval`) with a
new `cluster.AppRollout` read: the diagnosis plus what the stages have caught up with. In order:

1. **Commit.** With `--commit`, the deploy artifact's `Commit` must have the given prefix. Until
   it has, the verdict-to-be is Working at "the deploy artifact" ("waiting for commit 42a964e;
   running 9956993"). Only a Failed deploy artifact stage ends the wait early here (e.g. the
   registry refuses the login, so the new artifact can never arrive). Failed later stages belong
   to the previous artifact and are ignored.
2. **App values.** The Kustomization's `status.lastAppliedRevision` must equal the OCIRepository's
   `status.artifact.revision`, as `rollOut` already checks with `appliedRevision`. A Failed
   values stage counts only when `status.lastAttemptedRevision` is that revision too; otherwise
   it is the previous artifact's failure.
3. **Release.** shelf computes the digest of the values the release has to run: the ConfigMap
   `shelf-values` (key `app.yaml`) with the HelmRelease's inline `spec.values` merged on top,
   encoded as YAML with `sigs.k8s.io/yaml` and hashed with SHA-256. That is helm-controller's
   `configDigest`, reproduced byte for byte against the dev cluster (see "Spike" below), and a
   unit test pins it to a digest helm-controller wrote. Then:
   - The release *is about the current values* when `status.history[0].configDigest` equals that
     digest and `Reconciling` is not True. Only then do its Ready or Failed count.
   - An attempt *in flight is for the current values* when `status.lastAttemptedConfigDigest`
     equals it. Only then may a Failed pod stage end the wait early, because the release waits for
     its pods and would otherwise report the failure only after Helm's 5-minute timeout.
   - Anything else is Working ("waiting for the release to pick up the new values"). This covers
     an upgrade of the previous values that is still running, the rollback after it, and the
     retry.
4. **Diagnosis.** After that, the five stages decide: all Ready means `Ready`, and a Failed stage
   means `Failed` with that stage.

**Spike (2026-10-27, dev cluster).** A broken image was pushed, and the fix was pushed while
Helm was still waiting for the broken upgrade. What the HelmRelease reported:

| Time | HelmRelease |
|---|---|
| 21:13:15 | upgrade starts: `Reconciling`/`Ready=Unknown` "Running 'upgrade'", `lastAttemptedConfigDigest` = digest of the broken values, `history` unchanged |
| 21:18:07 | the fix's values are applied by kustomize-controller |
| 21:18:16 | broken upgrade times out: `Ready=False UpgradeFailed`, history v2 `failed`, `lastDeployed` 21:13:16 (the attempt's start) |
| 21:18:18 | remediation rollback: history v3 `deployed`, `lastDeployed` 21:18:18 (after the fix's values), digest of the old values; `Reconciling` True |
| 21:18:35 | upgrade to the fix: `Ready=True UpgradeSucceeded`; no new revision, because v3 already has those values |

The first version of this decision compared timestamps: the release's latest history entry or
Ready transition against the values' last change. `smoke-agent` showed it failing. The broken
upgrade's `UpgradeFailed` at 21:18:16 is newer than the fix's values, so the fix got the
verdict Failed. Comparing only the start of the latest attempt does not help either: the
rollback's entry is newer than the fix's values and carries the old ones. Only the digest says
which values a release holds.

Without `--commit`, steps 2 to 4 still apply, so a wait that starts in the middle of a rollout
does not report the old release as Ready.

The verdict (`WaitResult`) holds `outcome`, `app`, `commit`, `revision`, and for anything but
Ready the `stage` with `name`, `phase`, `reason`, `message` and `hint`. The text form is three
lines at most (outcome, stage and reason, hint). The default timeout is 10 minutes, which covers
a CI build of a few minutes plus the one-minute poll.

The exit statuses are 0, 1 and 2. They travel as a new `cli.ExitError{Code}` that
`cli.Execute` maps, after printing the verdict, without a second error line.

*Alternatives:* ask Flux to reconcile like `rollOut` does (faster by up to a minute, but status
would become a write that has to be audit-logged; rejected by the maintainer). Compare timestamps
of the values and the release (the first version of this decision; wrong after a failed upgrade,
see the spike). Watch instead of poll (more code for a loop that runs once per agent push).

### 5. `shelf app open`: client-go port-forward to a Traefik pod, plus a local reverse proxy

`internal/cluster` gets `ForwardTraefik(ctx, cfg)`. It resolves the Service `traefik/traefik`,
picks a ready pod from its EndpointSlices, maps the service port 80 to the pod's `web` port, and
opens a client-go `tools/portforward` session on `127.0.0.1:0`. It returns that local address
and a channel that closes when the session ends. This talks to the API server only: no kubectl,
and nothing about HTTP.

`internal/ops` builds the proxy: an `httputil.ReverseProxy` to the forwarded address whose
`Rewrite` sets `Host` to `AppHost(app, suffix, domain)`, the app's own host and never the quick
tunnel's random name. It also sets `X-Forwarded-Host` and `X-Forwarded-Proto: http` to the local
address. A `Location` header pointing at the app's own host is rewritten to the local address,
so redirects stay in the browser's reach. Upgrades (WebSocket) pass through, because
`ReverseProxy` supports them. When the session ends (Traefik pod restarted), the next request
opens a new one. Requests in flight fail with 502.

`internal/cli` binds `127.0.0.1:<port>` itself, with no flag for the address (spec "Local access
stays on the loopback interface"), prints the URL and serves until the context ends.
`--browser` calls `Options.OpenBrowser`. That seam defaults to `open` on macOS and `xdg-open` on
Linux, and tests replace it.

*Alternatives:* the API server's service proxy (`/api/v1/namespaces/traefik/services/…/proxy/`),
which `quickURL` uses for one GET. It rewrites paths and links, mangles redirects and does not
carry WebSockets well, so routing would no longer be identical to a tunnel. Forwarding straight
to the app's pod bypasses Traefik, `stripPrefix` and the multi-component routing. A raw TCP
forward without a proxy cannot set the Host header, so Traefik would not route.

### 6. Tenant template: a skill and a `CLAUDE.md` section, no hook

`examples/tenant/.claude/skills/shelf/SKILL.md` describes the loop:
1. Commit and push.
2. Run `shelf app status <app> --wait --commit "$(git rev-parse HEAD)" --json`.
3. On exit status 1, read `stage`, `reason` and `hint`, fix, and push again. On exit status 2,
   check CI first.

It also says what an agent cannot do: exposure is the operator's decision, and `shelf app open`
is for the person at the machine. The `CLAUDE.md` section names the app (`<app>`, filled in by
the operator) and points at the skill. A PostToolUse hook was rejected: a wait of several minutes
would hit the hook timeout and block the agent's turn.

### 7. Level-2 acceptance in a new `just smoke-agent`

The new smoke test does the following:
- It pushes `examples/hello` to the dev registry with `--revision main@sha1:<random>` (as
  `smoke-apps` already does).
- It adds the app and checks `status --json` with `jq`.
- It starts `--wait --commit <new>` before pushing a second artifact, and expects exit status 0
  only after the new commit's release is Ready.
- It pushes an artifact whose image cannot be pulled, and expects exit status 1 with the stage
  "the pods".
- It checks `--timeout 5s --commit <unknown>`, which must give exit status 2.
- It runs `shelf app open` against a fixture app with a `/api` route with `stripPrefix`
  (`traefik/whoami` echoes the path it got), and checks both routes and the Host Traefik saw
  with `curl`.
- It removes its app.

It runs in CI with the other level-2 smoke tests.

### 8. CI runs level 2 when a pull request is ready, chosen by paths, and in full on main

Added after the first PR run, when `smoke-agent` made each pull-request run about 12 minutes
(33 runner minutes), on every push. Measured on the PR runs of 2026-10-19 and 2026-10-27: test
2.7 min, smoke-secrets 2, smoke-chart 4.5, smoke-init 4.7, smoke-apps 6.8, smoke-agent about
12, of which about 5 are Helm's upgrade timeout in its last step.

- **When.** Level 2 runs when a pull request is opened or reopened as ready, or leaves draft;
  on every push and when the label is added while it carries the label `level-2` (also as a
  draft); and in full with the label `level-2-full`. Other pushes run level 1 only. A new push
  cancels the run it replaces, while a label unrelated to CI cancels nothing.
- **What.** `.github/scripts/select-smokes.sh` maps changed paths to smoke tests. Planning,
  docs, examples, the admin UI and the Cloudflare and GitHub clients need none. A path it does
  not list needs all of them, so a forgotten entry costs time, never coverage.
- **How much.** Pull requests run `smoke-agent` with `SMOKE_FULL=0`, which skips the fixed
  commit after the broken one (Helm's 5-minute timeout). `smoke-agent` asks Flux to fetch each
  new artifact at once; `--wait` stays read-only, and what is checked is how it follows a
  rollout, not Flux's interval. `smoke-apps` keeps its one step that waits for the poll on
  purpose.
- **main.** Every merge runs everything with `SMOKE_FULL=1`, so a failure belongs to one commit.
  A failure opens an issue labelled `ci-main-red`, or comments on the open one, because the
  e-mail GitHub sends goes to one person and is easy to miss (the maintainer chose the issue).
  `release.yml` publishes only a commit whose CI run on main succeeded.
- **Cache.** `just test` keeps Go's build and module cache in `.cache/go` through
  `actions/cache`. `gofmt` therefore checks the tracked files only.

Expected: about 2 minutes for a push to a pull request, about 4–5 when it is ready (smoke-apps
and smoke-agent decide), about 7 in full on main.

*Alternatives:* a nightly full run instead of one per merge (it was rejected: the result belongs
to no commit, GitHub mails only the last editor of the schedule, and it disables schedules in
public repositories after 60 days without activity). A cached devcontainer image in GHCR (about
a minute per job, but it publishes a new package; deferred to the maintainer). A shorter Helm
timeout or Flux interval for every app (product changes, not CI ones). One job for all smoke
tests (fewer runner minutes, but no shorter wait, and the tests would share a cluster).

## Risks / Trade-offs

- **The values digest reproduces helm-controller's.** If a Flux release changes how
  helm-controller merges or hashes values, no release ever matches, and every wait ends TimedOut
  at "the app" instead of giving a wrong verdict. → shelf pins Flux through its platform release;
  the unit test holds a digest helm-controller wrote, and `smoke-agent` fails on the first wait if
  the digests disagree. Both run before a Flux update ships.
- **A fix after a broken push waits for Helm.** helm-controller finishes (times out) the broken
  upgrade and its rollback before it starts the next one, which can take Helm's 5-minute timeout.
  → The verdict says "waiting for the release to pick up the new values" meanwhile, and the
  tenant skill tells the agent to expect it; `--timeout` defaults to 10 minutes.
- **A Failed pod stage can be transient** (an image pull that would succeed on a retry). → Report
  it as the verdict anyway. Kubernetes' own back-off makes "retry later" the agent's call, and
  the hint says what failed.
- **The commit annotation is a convention of `flux push artifact`.** An artifact pushed some
  other way has none, and `--commit` then times out. → The verdict says "the artifact names no
  commit" instead of only "timed out".
- **The port-forward needs `pods/portforward` in `traefik`.** The cluster-admin kubeconfig shelf
  uses has it. A narrower kubeconfig gets the API server's 403, which the command passes on.
- **Absolute URLs an app builds from the Host header point at `<app>.shelf.internal`.** Quick
  tunnels have the same issue. → Rewrite `Location` headers (decision 5). Links inside page
  bodies stay the app's responsibility, as they do behind any proxy.
- **A regression in the fix-after-failure path shows on main, not in the pull request.** → The
  `ci-main-red` issue and the release gate; `level-2-full` runs it on a pull request on demand.
- **The path map can miss a dependency.** → Unlisted paths run everything, and main runs
  everything after each merge.
- **The JSON contract can't be made stable after the fact.** → Golden files per kind from the
  first release, and the rule in the spec "Stable fields within a version".

## Migration Plan

No migration. The new fields and flags are additive. The text output of `shelf app status <app>`
gains a `commit` line, and its golden files are rewritten with `just golden`. Rollback is the
previous release. The platform artifact and the chart do not change.
