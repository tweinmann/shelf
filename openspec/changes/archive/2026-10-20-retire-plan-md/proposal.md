# Proposal

## Why

`docs/plan.md` (about 1,850 lines) predates OpenSpec. Since the baseline specs exist, half of it
repeats them, some of it in a form that is no longer true (the spec writers found a stale image
name scheme, `{app, builds}`, a webhook call that is never made, and `init cluster --domain`).
Two sources of truth for one behaviour drift apart. shelf should be described by OpenSpec alone:
specs for what it does, changes for what it is about to do, and the archive for how it got
here.

## What Changes

- New capability `dev-environment`: the devcontainer, the dev cluster, the test levels and the
  **footprint on the host Docker daemon** become requirements. The footprint inventory is a
  safety rule, which CLAUDE.md, `hack/nuke.sh` and `devcontainer.json` refer to.
- The history of Phases 0–8d moves, unchanged, into one archived change,
  `openspec/changes/archive/2026-10-20-baseline/`. Its `design.md` holds every decision and
  validated assumption, and its `phases.md` holds the goals, acceptance criteria and results of
  each phase.
- The roadmap becomes draft changes, each holding only a proposal and marked
  `skip_specs: true` until `/opsx:propose` writes its specs: `phase-9-mac-mini`,
  `phase-10-service-installer`, `phase-11-wizard`, `phase-12-operations` and
  `phase-13-reference-apps`. The open acceptance runs of 8b and 8d become
  `phase-8-acceptance`, which is verification only. The README Status table keeps one line per
  phase.
- Context, architecture and the limits of the MVP go, condensed, into the `context` of
  `openspec/config.yaml`, which every proposal reads.
- `docs/plan.md` is deleted. Every reference to it points to its new home instead.

## Capabilities

### New Capabilities

- `dev-environment`: how shelf is developed and checked: the pinned devcontainer with its own
  Docker daemon, the guard in `hack/`, the k3d dev cluster with its registry, the three test
  levels and CI, and the exact objects shelf leaves on the host Docker daemon and removes again.

### Modified Capabilities

None.

## Impact

- Deleted: `docs/plan.md`.
- New: `openspec/specs/dev-environment/spec.md` (on archive),
  `openspec/changes/archive/2026-10-20-baseline/`, and 6 draft changes under
  `openspec/changes/`.
- Edited: `openspec/config.yaml`, CLAUDE.md, README.md, and comments in `hack/nuke.sh`,
  `.devcontainer/devcontainer.json` and `hack/smoke/secret-expansion.yaml`.
- No code, chart, platform or behaviour changes. `just test` validates the drafts too, so a draft
  without its `skip_specs` marker would fail level 1.
