# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

A preview runs code an agent wrote. It must not reach other apps or the home LAN, and a preview
should count as green only when the app's own checks pass against it.

## What Changes

- Pod Security labels on app and preview namespaces.
- Default-deny NetworkPolicies per app namespace: traffic only within the namespace and from
  Traefik; egress to the internet allowed, egress to private address ranges outside the cluster
  (the home LAN) denied.
- Smoke tests by convention: an optional test component that runs against the preview and reports
  by exit code; a preview counts as green only when it passes.
- A verification report per branch: preview address, smoke test result, scan result, what changed.

## Capabilities

### New Capabilities

To be decided at `/opsx:propose`; possibly `isolation` and `preview-checks`.

### Modified Capabilities

To be decided at `/opsx:propose`; likely `app-chart`, `app-manifest`, `branch-previews`.

## Impact

`charts/shelf-app`, `platform/apps`, `internal/schema`, `internal/ops`, CLI. Level 2.

## Roadmap text (from the re-cut of 2026-10-27)

**Open:** `restricted` breaks many stock images (e.g. Postgres starting as root); likely
`baseline` enforced plus `restricted` warned, with a per-app opt-in to `restricted`.

**Open:** whether the dev cluster's k3d network policy controller enforces the LAN egress rule the
same way k3s on a host does, i.e. what level 2 can prove.

**Acceptance (level 2):** a preview cannot reach another app's namespace or a LAN address; a
failing smoke test marks the preview red.
