# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

Parallel coding agents work on several branches of the same app at once. Each branch needs its
own running, isolated copy of the app's complete stack to be tested against, on the user's own
hardware, without a shared database and without becoming reachable from the internet.

## What Changes

- `build.yml` pushes images and a deploy artifact also for non-default branches, tagged e.g.
  `branch-<slug>` (today it builds there but pushes nothing), and deletes the tag when the branch
  is deleted. Pull requests from forks never push.
- In the cluster, previews are discovered from the deploy artifact's tags.
- Each preview: its own namespace, its own secrets (generated, never copied from the app), its own
  volumes, a resource quota, a limit of concurrent previews per app; deleted with its tag.
- Previews are never exposed through a tunnel in this phase; they are reached with
  `shelf app open <app> --preview <branch>`.

## Capabilities

### New Capabilities

- `branch-previews` (planned).

### Modified Capabilities

To be decided at `/opsx:propose`; likely `app-delivery`, `app-chart` and the reusable workflow.

## Impact

`.github/workflows/build.yml`, `platform/apps`, `internal/cluster`, `internal/ops`, CLI. Level 2.

## Roadmap text (from the re-cut of 2026-10-27)

A preview gets the app's complete stack, including its own database instance, in its own
namespace. The platform knows components, not databases; opinionated providers (e.g.
`resources: db: {type: postgres}`) may come later as an optional layer.

**To verify at propose:** the Flux Operator's input provider for OCI artifact tags (the embedded
operator is v0.60.0), its tag filtering and polling interval. Fallback: the host service maintains
one provider per preview, as long as no deploy path depends on a forge API.

**Open:** naming of preview namespaces and hosts within the 40-character limit and without
colliding with other app names (e.g. a reserved separator, or a label-based namespace name).

Rejected alternatives, with their reasons:

- Flux Operator `GitHubPullRequest` / `GitHubBranch` input providers: the cluster would talk to
  the GitHub API, against the guardrail that the registry is the only interface between build and
  platform. The workflow pushes an artifact per branch instead.
- A shared Postgres instance for previews: cheaper, but opinionated, and against "everything is a
  component" and independent environments.
- A vcluster per preview: heavier than namespaces; only worth it for apps that bring CRDs or
  cluster-scoped objects. Namespaces with quotas (and the NetworkPolicies of Phase 12) are the
  default.
- A local build path into the cluster for fast iteration: the devcontainer is the inner loop;
  previews verify the built artefact.

**Acceptance (level 2):** two branches run side by side, isolated, each with its own database;
deleting a branch removes its preview; RAM per preview is measured and recorded. `examples/hello`
and `examples/tenant` serve as the reference apps (formerly Phase 13).
