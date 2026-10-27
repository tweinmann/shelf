# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

shelf is pre-production: it is where an app is built and verified. Production is an independent
cluster that depends only on GHCR and its own fleet repository, so that it keeps running when
shelf is down. The verified state has to get there as standard artefacts, without depending on
shelf.

## What Changes

- `shelf target add`: register a target cluster and run its preflight.
- `shelf eject plan <app> --target <t>`: render with the target's profile, show the differences,
  block unverified or local artefacts.
- `shelf eject apply`: open a pull request in the target's fleet repository with plain
  Flux/Helm/policy objects and the same verified digest. Later versions arrive as pull requests
  that bump the digest (promotion), or the repository continues on its own (eject).

## Capabilities

### New Capabilities

- `targets`, `eject` (planned).

### Modified Capabilities

To be decided at `/opsx:propose`.

## Impact

CLI, `internal/ops`, a new package for target profiles and fleet repositories. Level 3: needs a
real target cluster.

## Roadmap text (from the re-cut of 2026-10-27)

First reference target: the maintainer's Hetzner k3s/Flux setup.

Needed from the start, and to be respected by earlier phases: target profiles (the shelf host is
just the first target), a portable secrets format (e.g. SOPS per target, or External Secrets),
per-target component overrides (replace a component with env pointing at an external service),
artefacts always in GHCR. The guardrail "the runtime output depends on no shelf-specific CRD" was
adopted with the re-cut for this phase.

**Open:** opening a pull request is a forge API call from the host. The guardrail allows the host
service to call the GitHub API as long as no deploy path depends on it; whether a path into
production counts as a deploy path has to be decided here.

shelf cannot test high availability, provision cloud infrastructure or migrate data automatically;
it can guide a data migration and a DNS cut-over.

Later, building on the target preflight: `shelf init --existing-cluster`, which installs only
shelf's components into an existing k3s/Kubernetes cluster.

**Acceptance (level 3):** a verified app from the shelf host runs on the Hetzner target from a
merged pull request with the same digest, and keeps running with shelf switched off; the
maintainer's own apps are the first real tenants (formerly Phase 13).
