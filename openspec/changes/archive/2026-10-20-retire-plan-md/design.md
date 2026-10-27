# Design

## Context

See proposal.md, "Why". OpenSpec has a home for current behaviour (`specs/`), for planned work
(`changes/`) and for finished work (`changes/archive/`), plus one free-text field that every
proposal reads (`context` in `openspec/config.yaml`, at most 50 KB). It has no file for history,
a roadmap or a project overview. Checked in a scratch copy with OpenSpec 1.14.1: a change that
holds only a proposal fails `openspec validate --strict` with "Change must have at least one
delta", and passes once its `.openspec.yaml` says `skip_specs: true`. `validate --all` does not
look into the archive.

## Goals / Non-Goals

**Goals:**
- Every section of plan.md has exactly one new home, or is dropped because a spec or the README
  already covers it.
- Nothing is rewritten on the way: history and decisions move verbatim, so this change cannot
  introduce new errors into them.

**Non-Goals:**
- Correcting the stale parts of the old text. They stay as they were in the archive, which
  records what was decided then; the specs say what is true now.
- Writing the specs of Phases 9–13. That is `/opsx:propose`, phase by phase.

## Decisions

- **Where each section goes:**

  | plan.md section | New home |
  |---|---|
  | Context, Architecture, Design consequence (`init` layers), Explicitly not in the MVP, "Later" open items | `openspec/config.yaml` `context`, condensed. All of them are also kept verbatim in the baseline archive |
  | Context | `archive/2026-10-20-baseline/proposal.md`, verbatim |
  | Decisions, Validated assumptions, and the reference sections (Architecture, Rendering pipeline, Handover contract, Schema, App registration, CLI, Repository layout, `CLAUDE.md`) | `archive/2026-10-20-baseline/design.md`, verbatim, as a snapshot of 2026-10-20 |
  | Development and test environment (levels, DinD, dev cluster, footprint), Phases 0–8d | `archive/2026-10-20-baseline/phases.md`, verbatim; the rules of the environment also become the spec `dev-environment` |
  | Mac mini access, "Due in Phase 9" open items | draft `phase-9-mac-mini` (and verbatim in the baseline) |
  | Phases 9–13 | drafts `phase-9-…` to `phase-13-…` |
  | 8b/8d leftovers | draft `phase-8-acceptance` |

- **One baseline archive instead of one per phase.** Phases 0–8d were never OpenSpec changes,
  and splitting them would invent proposals and task lists after the fact. A single archived
  change, `2026-10-20-baseline`, with `skip_specs: true` (its specs are the 7 baseline specs that
  already exist), keeps the history in one greppable place.
  *Alternative:* git history only. Rejected by the maintainer: decisions keep their reasons in
  the repository.
- **Drafts carry `skip_specs: true` until they are proposed.** This is the only way a
  proposal-only change passes `just test`. `/opsx:propose` for a phase removes the marker when it
  writes the delta specs, and `openspec/config.yaml` says so, so a proposal cannot forget it.
  `phase-8-acceptance` keeps the marker for good, because verification changes no behaviour.
- **Decisions from now on** go into the `design.md` of the change that makes them, and reach the
  archive with it. A decision that changes a convention or guardrail also changes CLAUDE.md or
  the `context` in the same change; that replaces the "Decisions" section of plan.md.
- **The footprint inventory becomes a requirement**, with the table in a scenario. That keeps it
  under `openspec validate`, and turns the rule "new host objects go into the inventory,
  `hack/nuke.sh` and `devcontainer.json` together" into part of the spec.

## Risks / Trade-offs

- [Draft markers forgotten: a phase is implemented while its change still says `skip_specs`,
  and no spec records the new behaviour] → The `context` tells `/opsx:propose` to remove the
  marker, and the CLAUDE.md working agreement repeats it. Archiving a change with the marker and
  no deltas is visible in review.
- [The `context` grows into a second plan.md] → It holds only what every proposal needs, at
  roughly 150 lines; details belong in specs or changes.
- [Links to `docs/plan.md#…` anchors in old commits and the archived `require-platform-for-apps`
  break] → Old commits keep their own plan.md. The archive is never edited.
