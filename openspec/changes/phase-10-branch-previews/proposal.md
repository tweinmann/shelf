# Proposal

## Why

Parallel coding agents work on several branches of the same app at once. Each branch needs its
own running, isolated copy of the app's complete stack, its own database included, to be tested
against on the user's own hardware. Today a branch is built in CI and then thrown away: nothing
of it runs anywhere. Phase 9 gave agents a way to wait for a commit and read a verdict; this
phase gives them something per branch to wait for.

## What Changes

- **Previews are an operator decision per app.** `shelf app add` and `shelf app credentials`
  take `--previews[=N]` (at most N previews, default 3) and `--no-previews`; off by default.
  A tenant repository cannot give itself previews.
- **The reusable workflow publishes branches.** `build.yml` also pushes the images and the
  deploy artifact for a push to a non-default branch, tagged `branch-<slug>`, and deletes that
  tag when the branch is deleted. Pull requests, from forks or not, still push nothing. The
  slug comes from `shelf build-plan --branch <name>`, the one implementation the CLI shares.
  Tenants change their `deploy.yml` trigger to all branches plus `delete`.
- **The cluster discovers previews from the registry.** An app with previews gets a second
  provider, `<app>-previews`, of type `OCIArtifactTag` on the app's deploy artifact, filtered to
  `branch-*`. A new ResourceSet `previews` in the platform artifact turns each tag into a
  preview: namespace `<app>--<slug>`, host `<app>--<slug>.shelf.internal`, the app's registry
  login, a ResourceQuota and LimitRange, and the same deploy chain as an app. A deleted tag
  removes its preview with all its volumes. No forge API is involved.
- **A preview has its own state.** The chart generates a preview's secrets itself (`lookup`,
  otherwise random), never copied from the app; volumes and database are the preview's own.
- **Previews are never exposed.** No tunnel, no quick tunnel, no DNS record; they are reached
  with `shelf app open <app> --preview <branch>`.
- **The agent interface covers previews.** `shelf app status <app>` lists the app's previews;
  `--preview <branch>` selects one for status, `--wait --commit` and `open`, with the same JSON
  documents. `shelf app rm` removes an app's previews with it.
- **Short turnaround for agents.** `build.yml` caches image layers in the registry
  (`:buildcache` per component), previews poll every 30 seconds instead of 60, and the tenant
  skill tells an agent to wait in the background while it keeps working, and to check the CI run
  of its push first, so that a broken build shows within seconds instead of at the timeout.
  `smoke-previews` measures the time from push to Ready.
- **App names may not contain `--`**, which separates an app from its previews.
- **Dev registry allows deletes**, so that level 2 can delete a branch tag.
- `just smoke-previews`: two branches side by side, each with its own database; deleting a
  branch removes its preview; RAM per preview measured.

## Capabilities

### New Capabilities

- `branch-previews`: what a preview is, how it is discovered, named, isolated, limited, reached
  and removed.

### Modified Capabilities

- `app-delivery`: app naming reserves `--`; the build plan names a branch's tag; the reusable
  workflow publishes and deletes branch artifacts; status lists previews; removal removes them.
- `app-chart`: generated secrets in preview mode.
- `app-exposure`: local access to a preview.
- `agent-interface`: `--preview` on status, wait and the JSON documents; the tenant skill waits
  in the background and checks CI first.
- `cluster-init`: the platform artifact also holds the ResourceSet `previews`.
- `dev-environment`: the dev registry allows deletes; `smoke-previews` in the full level-2 set.

## Impact

- `platform/previews` (new ResourceSet), `charts/shelf-app` (preview mode, generated secrets),
  `.github/workflows/build.yml` (branches, delete), `.github/scripts/select-smokes.sh`.
- `internal/cluster` (preview provider, preview discovery and diagnosis), `internal/ops`
  (previews on add/credentials/rm, status, wait, open), `internal/cli` (`--previews`,
  `--preview`, `build-plan --branch`), a slug function shared by CLI and workflow.
- `examples/tenant` and the README (new `deploy.yml` trigger), `hack/cluster-up.sh` (registry
  deletes), `hack/smoke/previews.sh`.
- Platform and chart versions change; existing apps are unaffected until previews are switched
  on for them. No admin UI change in this phase.
- Level 1 for slug, chart and verdicts; level 2 for discovery, isolation and removal
  (`just smoke-previews`); level 3 for branch deletion through GHCR with `tweinmann/shelf-hello`.
