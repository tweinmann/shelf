# Design

## Context

See proposal.md for the motivation. The facts the approach rests on:

- The platform turns each `ResourceSetInputProvider` labelled `shelf.dev/app` in `shelf-system`
  into an app (ResourceSet `apps`, `platform/apps/resourceset.yaml`). Each app's provider is of
  type `Static` and carries the inputs `shelf app add` wrote.
- The embedded Flux Operator v0.60.0 offers the provider type `OCIArtifactTag`, with `includeTag`
  and `excludeTag` filters, a `limit`, `insecure`, a `secretRef` holding an image pull secret, and
  `defaultValues` that fill in inputs the provider does not produce.
- **Spike, 2026-10-28, dev cluster.** Three tags (`main`, `branch-feature-login`,
  `branch-fix-typo`) were pushed to `shelf-registry:5000/spike/previews`, and an
  `OCIArtifactTag` provider with `includeTag: ^branch-` and `defaultValues` `{app, url}` was
  created next to them.
  - It exported one input set per branch tag, `{id, tag, digest}` plus the defaults, and left
    out `main`. The `id` is a number derived from the tag.
  - A ResourceSet template rendered `<< inputs.app >>--<< trimPrefix "branch-" inputs.tag >>`
    as `hello--feature-login`.
  - Deleting a tag failed: the dev registry answers `DELETE` with 405, because `registry:2`
    ships with deletes disabled (`/etc/docker/registry/config.yml` has no `storage.delete`).
- GHCR deletes a tag through the GitHub REST API ("delete package version"), not through the
  OCI `DELETE`. That call happens in the tenant's CI, not in the cluster.
- The chart reads every secret from the Secret `shelf-secrets`, which the platform copies from
  `shelf-system/app-<app>`, where `shelf app add` stores what it generated (`rand.Text()`, 26
  URL-safe characters). Nobody runs `shelf app add` when a branch appears.
- `kubectl top` works on the dev cluster (k3s ships metrics-server), so RAM per preview can be
  measured at level 2.

## Goals / Non-Goals

**Goals:**
- A branch tag in the registry becomes a running, isolated preview within about two minutes,
  and a deleted tag removes it, with the platform reading only the registry.
- Previews behave like apps to every read: diagnosis, `--wait --commit`, `open`, JSON.
- Opting an app in is one flag for the operator. Opting out, or removing the app, leaves nothing
  behind.

**Non-Goals:**
- Exposing a preview in any way (Cloudflare Access is a later phase).
- NetworkPolicies and Pod Security (Phase 12). Quota and LimitRange are in this phase because
  without them one preview can starve the app.
- Previews in the admin UI. The dashboard keeps listing apps; it lists previews in a later phase.
- Previews of pull requests from forks. Forks never push.
- Copying data from the app into a preview (seed data stays in "Later").
- Deleting the per-branch images. Only the deploy artifact's tag is deleted; images accumulate
  as they do for `main` today.

## Decisions

### 1. One `OCIArtifactTag` provider per app, read by a second ResourceSet

```
 shelf app add hello oci://ghcr.io/acme/hello:main --registry ghcr --previews=3
   |
   +--> shelf-system/hello            Static, label shelf.dev/app       --> ResourceSet apps
   +--> shelf-system/hello--previews  OCIArtifactTag, label shelf.dev/previews=hello
          url oci://ghcr.io/acme/hello, includeTag ^branch-, limit 3,
          secretRef connection-registry-ghcr, reconcileEvery 30s
          defaultValues: app, url, insecure, registry
            |  one input set per tag: id, tag, digest + defaults
            v
        ResourceSet previews (platform/previews/resourceset.yaml)
            per preview: Namespace, Secret shelf-registry (copy), ResourceQuota, LimitRange,
            ServiceAccount/Role/RoleBinding shelf-deploy, OCIRepository deploy (tag),
            Kustomization deploy, OCIRepository shelf-app, HelmRelease <app>--<slug>
```

- **Naming and label.** The provider is named `<app>--previews`, so it cannot collide with
  another app's provider, since `--` is reserved in app names. It carries
  `shelf.dev/previews=<app>` and not `shelf.dev/app`, so the ResourceSet `apps` does not pick
  it up.
- **Interval.** The provider runs every 30 seconds (`fluxcd.controlplane.io/reconcileEvery: 30s`),
  as does a preview's deploy artifact (decision 9).
- **Deploy chain.** Each preview's chain is the app's, with the branch tag as the OCIRepository's
  `ref.tag`. The HelmRelease is named after the preview namespace, as an app's is after its own,
  so the diagnosis of decision 6 needs no special case.

*Alternatives:*
- **Flux Operator `GitHubBranch`/`GitHubPullRequest` providers.** The cluster would call the
  GitHub API, against the guardrail.
- **One `Static` provider per preview, written by the host service.** A deploy path would then
  depend on the host service running.
- **Previews as extra inputs of the app's own provider.** A `Static` provider exports one input
  set.
- **One shared provider for all apps.** One provider has one URL.

### 2. Previews are switched on per app, by the operator

- **Flags.** `--previews[=N]` (1–10, default 3) on `shelf app add` and `shelf app credentials`
  creates or updates `<app>--previews` with `filter.limit: N`. `--no-previews` deletes it, and
  the ResourceSet `previews` then deletes every preview of the app.
- **Where the state lives.** There is no new input on the app's provider: whether an app has
  previews is whether that provider exists, and N is its limit.
- **Registry and access.** A later `shelf app credentials` that changes the app's registry
  connection or `--insecure-registry` rewrites the preview provider as well, so that it reads
  with the same login.
- **Same path as every operation.** The flags go through `internal/ops`, as every CLI action
  does. The admin UI does not offer them in this phase, which the guardrail allows (UI ⊆ CLI).

*Alternative:* previews for every app. A tenant's branch pushes would then use the operator's
RAM without the operator's say, the same reason exposure is the operator's decision.

### 3. The slug is computed once, in Go, and travels in the tag

- **Function.** `schema.BranchSlug(branch)` lower-cases the branch name, turns every run of
  other characters into `-` and trims the ends. A result longer than 20 characters becomes its
  first 13 characters, `-`, and 6 hex digits of the SHA-256 of the branch name.
- **Who calls it.** `shelf build-plan --branch <name>` prints the tag `branch-<slug>` for
  `build.yml`; `--preview <branch>` in the CLI calls the same function. A slug passes through
  it unchanged, so `--preview feature-login` works as well as `--preview feature/login`.
- **Length.** 40 characters of app, `--` and 20 of slug make 62, inside the 63 of a namespace
  and of a DNS label in a host name.
- **The cluster does no slugging.** It only strips the prefix:
  `<< inputs.app >>--<< trimPrefix "branch-" inputs.tag >>`, shown to work in the spike.

*Alternatives:*
- **The provider's numeric `id` (`hello-p1366820814`).** Always short and unique, but nobody
  can read it; rejected by the maintainer.
- **Slugging in the ResourceSet template.** It would need truncation and hashing inside the
  template, a second implementation that can drift from the CLI's.

### 4. The chart generates a preview's secrets with `lookup`

- **What the chart renders.** With `platform.preview: true`, set only by the ResourceSet
  `previews`, `shelf-app` renders `shelf-secrets` itself: for each secret app.yaml declares with
  `generate: true`, the value `lookup` finds in the existing Secret, otherwise `randAlphaNum 26`.
- **Where it ends up.** helm-controller runs `lookup` against the cluster, so upgrades keep the
  values. The values end up in the Secret and in Helm's release storage in the preview
  namespace. Both are in the cluster and go away with the namespace; neither reaches Git, a log,
  a golden file or the host.
- **Tests.** The chart tests replace the generated values with a fixed placeholder before
  comparing with the golden files.
- **Apps are unchanged.** Without `platform.preview` the chart does what it does today.

*Alternatives:*
- **A Job in each preview namespace that creates the Secret with kubectl.** An image, RBAC and
  code of our own for something Helm does.
- **Copying the app's secrets.** It would share a database password between branches and the
  app, against "a preview's own state".
- **The host service generating them.** A deploy path would depend on it.

### 5. Limits: quota, LimitRange, at most N previews

- **Quota.** Every preview namespace gets a ResourceQuota (`requests.cpu: 1`,
  `requests.memory: 2Gi`, `requests.storage: 5Gi`).
- **LimitRange.** A quota on requests makes Kubernetes refuse pods without requests, so the
  LimitRange gives containers a default request (50m CPU, 64Mi memory) and a default memory
  limit of 512Mi.
- **The limit of N.** It is the provider's `filter.limit`. The provider takes tags in reverse
  alphabetical order, not by recency.
- **Reporting.** shelf cannot know about the branches beyond the limit without reading the
  registry, so `shelf app status` reports "limit reached" whenever the number of previews
  equals N.

### 6. Reading previews: the same diagnosis, keyed by namespace

- **Listing.** `cluster.AppStates` stays the list of apps (providers labelled `shelf.dev/app`).
  A new `cluster.Previews(app)` lists the namespaces labelled `shelf.dev/app=<app>` and
  `shelf.dev/preview`.
- **Diagnosis and rollout.** They run on a namespace instead of an app name. The objects of a
  preview have the same names in their namespace as an app's, so `diagnose`, `AppRollout` and
  `Verdict` from Phase 9 apply unchanged.
- **CLI.** `--preview <branch>` on `status`, `status --wait` and `open` maps the branch to its
  namespace through `BranchSlug`.
- **Waiting for a preview that doesn't exist yet.** It is Working ("waiting for the preview of
  branch feature/login"), because CI and the one-minute poll come first.

### 7. Publishing and deleting branch artifacts in `build.yml`

- **Triggers.** Tenants change their `deploy.yml` to
  `on: {push: {branches: ['**']}, pull_request: {}, delete: {}}`. A tenant that keeps
  `branches: [main]` keeps working and gets no previews.
- **Push to the default branch.** Unchanged.
- **Push to another branch.** Images and the deploy artifact as `sha-<short>` and
  `branch-<slug>`, from `shelf build-plan --branch "$GITHUB_REF_NAME"`. The job summary shows
  `shelf app status <app> --preview <branch>` and `shelf app open <app> --preview <branch>`.
- **`delete` of a branch.** The workflow computes the tag the same way, finds the package
  version of `ghcr.io/<owner>/<package>` that carries it (`/users/` or `/orgs/` packages API,
  depending on the owner), and deletes that version with the workflow's `GITHUB_TOKEN`
  (`packages: write`).
- **Pull requests.** They still push nothing, from forks or not.

*Alternative:* leaving branch artifacts and expiring previews by age in the cluster. That needs
state the provider does not have, and a deleted branch would keep running.

### 8. Dev registry with deletes, and `just smoke-previews`

- **Registry deletes.** `hack/cluster-up.sh` enables `storage.delete` in the registry
  container's config and restarts it, idempotently, so that level 2 deletes tags as GHCR does.
- **What `smoke-previews` does:**
  1. Adds `examples/hello` (web plus Postgres with a volume) with `--previews=2`.
  2. Pushes `branch-a` and `branch-b`, waits for both previews with `--wait --commit`, and
     checks two namespaces, two Postgres volumes and three distinct passwords. The values are
     compared by hash, never printed.
  3. Checks that `open --preview` reaches a preview with its own Host, and that no preview has
     cloudflared. The app stays private: only `smoke-quick` may open a quick tunnel (safety
     rules), and that the template never renders cloudflared is pinned by
     `TestPreviewsResourceSet`.
  4. Pushes `branch-c` and expects the limit to hold, with `previewLimitReached` set.
  5. Deletes `branch-a` and expects its namespace and volume to be gone.
  6. Runs `--no-previews` and expects all previews gone, the app untouched.
  7. Measures RAM per preview with `kubectl top` and records it in the results of this change.
  8. Removes the app.
- **CI.** It joins the full level-2 set and `select-smokes.sh`. `platform/previews/` and
  `charts/**` select it.

### 9. Turnaround: the agent should hardly notice the platform

**Measured in Phase 9.** A push to `main` of `tweinmann/shelf-hello` was Ready on `greeter` in
127 s. That covers:
- the CI build, two images, estimated at 60–80 s;
- up to 60 s for Flux's poll;
- 10–20 s of rollout.

A first push of a new branch adds the provider's discovery and a first install, together an
estimated 2–3.5 minutes. The maintainer asked to take the low-hanging fruit and to slow the
agent's flow as little as possible.

- **Build cache.** `build.yml` builds with
  `--cache-from type=registry,ref=<image>:buildcache` and, in a publishing run,
  `--cache-to type=registry,ref=<image>:buildcache,mode=max`.
  - The cache lives in the component's image package, not in the deploy artifact. The preview
    provider (`^branch-`) and `shelf app tags` never see it.
  - It needs no extra action, because the registry login is already there.
  - Pull requests only read it.
- **Previews poll every 30 seconds.** The preview provider (`reconcileEvery: 30s`) and a
  preview's deploy OCIRepository (`interval: 30s`). Apps stay at 1 minute: the extra registry
  calls are limited to the branches agents are working on.
- **The agent does not idle.** The skill makes the wait a background job: push, start
  `--wait --commit` in the background, go on, read the verdict before reporting the work done.
  With the GitHub CLI it first watches the push's CI run (`gh run watch --exit-status`), so a
  broken build is known in seconds rather than at the wait's timeout. That reads the forge from
  the agent's machine, never from the platform.
- **Measured, not assumed.** `smoke-previews` prints the seconds from push to Ready for a first
  push and for a later commit. Results go into this design. The level-3 acceptance with
  `shelf-hello` adds the CI time.

*Alternatives:*
- **A Flux Receiver webhook from CI.** It removes the poll altogether, but the cluster would
  have to be reachable from the internet, which only the operator decides for an app.
- **`--reconcile` on the wait.** It was rejected in Phase 9: status would write.
- **30-second polls for apps too.** It doubles every app's registry calls for a gain only
  previews need.
- **The GitHub Actions cache (`type=gha`).** `docker buildx build` in a `run` step does not see
  the runtime token without an extra action.

## Risks / Trade-offs

- **Two branches with the same slug share a tag**, for example `feature/login` and
  `feature-login`. The later push wins and both run as one preview. → Documented in the skill
  and the README; it only happens without truncation, where the names are visibly alike.
- **The limit picks tags alphabetically, not by recency, and can evict.** A new branch that
  sorts after the others pushes an existing preview out, and that preview's namespace and data
  go with it, until a tag before it is deleted. → The status says when the limit is reached; the
  operator raises N or deletes branches. A limit by recency would need a provider that sorts by
  push time, which `OCIArtifactTag` does not offer.
- **Deleting through GHCR needs the token to administer the package.** Packages created by the
  repository's own workflow normally allow it. → Checked at level 3 with `tweinmann/shelf-hello`;
  until then a deleted branch's tag may stay, and its preview with it.
- **A preview counts against the host's RAM even when idle.** → At most N per app, a quota, and
  the RAM per preview measured in `smoke-previews`. Scale-to-zero stays out of scope.
- **Generated preview secrets live in Helm's release storage.** → In the preview namespace only,
  deleted with it, and never in a file of the repository. Accepted with the decision.
- **`requests.storage` counts requested sizes; local-path does not enforce them.** → As for apps
  today, the README already says volume sizes are not enforced.
- **Previews read the registry every 30 seconds**: the provider per app and the deploy artifact
  per preview. → Authenticated with the app's login, at most N + 1 readers per app. GHCR's limits
  for authenticated reads are far above that.
- **A stale or broken build cache.** → `buildx` treats the cache as a hint; a missing or
  incompatible cache only costs the time it would have saved.

## Migration Plan

- **Platform and chart.** The ResourceSet `previews` and the chart's preview mode ship with the
  next platform and chart versions; `shelf init cluster` installs them. Apps registered before
  are unchanged: they have no preview provider.
- **Tenants.** Previews need the new `deploy.yml` trigger and an operator's `--previews`.
  `examples/tenant` and the README show the new trigger; `tweinmann/shelf-hello` is updated in
  the acceptance.
- **Rollback.** `--no-previews` on every app, then the previous platform release.

## Open Questions

- ~~Whether GHCR lets the workflow's `GITHUB_TOKEN` delete a package version of a user-owned
  package without a separate PAT.~~ It does: answered at level 3 on 2026-10-28 (see Results).

## Results

`just smoke-previews` on the dev cluster (k3d in the devcontainer, 2026-10-28), times measured
from the moment the tag is in the registry, so without the CI build:

| Measurement | Value |
|---|---|
| First push of a branch to a Ready preview (provider, namespace, quota, chain, release) | 23 s |
| Later commit on that branch to Ready | 17 s |
| Branch tag deleted to preview namespace gone | 26 s |
| RAM per preview of `examples/hello` (`kubectl top`) | 30 Mi, same as the app |

Both previews kept their own password and volume across a new commit, the limit of two held
with three branch tags, and `--no-previews` removed the previews and kept the app. The push to
Ready of a real tenant, CI build included, is measured in the level-3 acceptance.

Level 3 with the private tenant `tweinmann/shelf-hello` and release v0.7.0 on 2026-10-28, app
`gretter` (four components, two databases) with `--previews`:

| Measurement | Value |
|---|---|
| CI run of the branch push (build, render, push), first build with v0.7.0, cache empty | 1 min 33 s |
| Deploy artifact `branch-try-preview` pushed by CI to the preview Ready | 11 s (10:38:14 to 10:38:25) |
| Push of a new branch to a Ready preview, in total | about 1 min 45 s |
| Branch deleted on GitHub to the preview namespace gone | under 1 min; the tag left GHCR, then 20 s to the namespace gone |

The `delete-branch` job deleted the tag with the workflow's `GITHUB_TOKEN`; no separate token
is needed.
