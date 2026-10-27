# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

Someone who has never seen Kubernetes should get from the one-liner to a reachable app without opening a terminal.

## What Changes

- A browser wizard: claim, host preflight, Colima, cluster, connections, the first app.
- Note: the roadmap text still names "the cluster's domain" as a step. Since Phase 8d a cluster has no domain; a domain belongs to an app. The proposal has to drop that step.

## Capabilities

### New Capabilities

- `setup-wizard` (planned), and changes to `admin-ui`.

### Modified Capabilities

To be decided at `/opsx:propose`.

## Impact

`internal/server`, `internal/ops`. Level 2 for the UI, level 3 for the host steps.

## Roadmap text (from the plan before OpenSpec)

Claim → host preflight → Colima → the cluster's domain → cluster as one job → the connections (a
registry login and a Cloudflare token, each checked when it is entered) → the first app, choosing
them and its domain (the zone picked from the Cloudflare API rather than typed), with the two
files the tenant repository needs offered for copying, `<owner>` already filled in.

**Acceptance:** someone who has never seen Kubernetes gets from the one-liner to a reachable
`https://<app>.<domain>` without opening a terminal, given only a registry PAT, a Cloudflare token
and a tenant repository; re-running the wizard on a configured host changes nothing and shows the
current state; a wrong token is rejected on the step that collects it, naming the permission that
is missing.
