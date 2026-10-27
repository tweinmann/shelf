# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

A preview may run anything a branch built. Before an app goes beyond a preview, shelf has to know
that the running digest is the one CI built from a given commit, signed by the expected identity
and scanned.

## What Changes

- `build.yml`: multi-arch build option (arm64 default for shelf hosts, amd64 for cloud targets),
  image scan (e.g. Trivy), signature and build provenance for images and the deploy artifact
  (cosign keyless / GitHub artifact attestations), the scan result attached as an attestation.
- Cluster: Flux verifies the deploy artifact's signature (OCIRepository `verify`); an admission
  policy accepts only signed image digests from the expected identity.
- `shelf app status` shows the chain: commit → digest → signature → scan → deployed.
- Exposure gate: `--cloudflare` and `--quick` refuse an app whose running digest is not verified.

## Capabilities

### New Capabilities

- `verification` (planned).

### Modified Capabilities

To be decided at `/opsx:propose`; likely `app-delivery`, `app-exposure`, `cluster-init` and the
reusable workflow.

## Impact

`.github/workflows/build.yml`, `platform/`, `internal/cluster`, `internal/ops`, CLI and admin UI.
Level 2.

## Roadmap text (from the re-cut of 2026-10-27)

Three separate states per app and preview: **build** (built, scanned, signed), **deployment**
(which digest runs where), **exposure** (cluster only, port-forward, tunnel). Only a verified
artefact goes beyond a preview.

**Open:** Kyverno vs. Sigstore policy-controller for the admission policy; the RAM footprint on a
small host matters.

This change adds the guardrail "only a verified digest can be exposed beyond a preview" to
CLAUDE.md. It was deliberately not adopted with the re-cut, because it describes behaviour that
does not exist before this phase. shelf is not marketed as more secure than alternatives before
this chain exists.

**Acceptance (level 2):** an unsigned or unscanned artefact does not reach the cluster; exposing
an unverified app is refused; the chain is queryable per running version.
