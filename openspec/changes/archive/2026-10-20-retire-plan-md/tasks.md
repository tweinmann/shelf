# Tasks

## 1. Baseline archive

- [x] 1.1 Create `openspec/changes/baseline` with `openspec new change baseline`. Add
  `skip_specs: true` to its `.openspec.yaml`, and write a `proposal.md` saying what the baseline
  is (shelf as of Phase 8d, history before OpenSpec, specs in `openspec/specs/`). Verify with
  `openspec validate baseline --strict`.
- [x] 1.2 Move the "Decisions" and "Validated assumptions" sections of `docs/plan.md` verbatim
  into `baseline/design.md`, under a short intro. Move "Development and test environment" and
  "Phases" from Phase 0 up to and including 8d into `baseline/phases.md`. Verify with a script
  that every line of the moved sections appears byte for byte in the target files.
- [x] 1.3 Add a `tasks.md` with no tracked tasks (a note that the work happened before OpenSpec).
  Then `mv` the change to `openspec/changes/archive/2026-10-20-baseline`. Verify that
  `openspec list` no longer shows it.

## 2. Roadmap as draft changes

- [x] 2.1 Create `phase-9-mac-mini`, `phase-10-service-installer`, `phase-11-wizard`,
  `phase-12-operations` and `phase-13-reference-apps` with `openspec new change`, each with
  `skip_specs: true`. Each `proposal.md` holds Why, What Changes, the planned new capabilities
  and Impact, plus the phase text from plan.md (goal and acceptance) unchanged under
  "## Roadmap text (from docs/plan.md)". `phase-9-mac-mini` also gets "Mac mini access for
  development" and the "Due in Phase 9" open items. Verify with
  `openspec validate --all --strict`.
- [x] 2.2 Create `phase-8-acceptance` (`skip_specs: true`) with a proposal and a `tasks.md` of
  the open runs from CLAUDE.md's 8b/8d status lines: `just smoke-expose` against `tobile.ch`,
  migrating `greeter-dev` off the shared tunnel, `just smoke-quick`, `form.js` against real
  connections, and release v0.5.0. Verify that `openspec list` shows its task count.

## 3. Context and references

- [x] 3.1 Rewrite the `context` in `openspec/config.yaml`:
  - product and architecture summary
  - the `init` layers
  - not in the MVP, and later items
  - the rule that a draft's `skip_specs` goes when its specs are written
  - CLAUDE.md as the home of guardrails, safety rules and conventions, with no mention of
    plan.md

  Verify that `openspec context --json` parses it and that `wc -c` stays under 51,200 bytes.
- [x] 3.2 Update CLAUDE.md:
  - the intro pointer
  - the "Results in docs/plan.md" status lines, which now point to the baseline archive
  - the working agreements (phases are the changes in `openspec/changes/`; decisions in
    `design.md`; a convention change also changes CLAUDE.md or the context; drafts lose
    `skip_specs` when proposed; "Keep this file and docs/plan.md current" drops plan.md)
  - the footprint safety rule, which now points to the `dev-environment` spec

  Verify with `git grep -n plan.md CLAUDE.md`, which must print nothing.
- [x] 3.3 Update the remaining references:
  - README.md: How it works, Status, Footprint link, layout, Further reading
  - `hack/nuke.sh:49`
  - `.devcontainer/devcontainer.json:28`
  - `hack/smoke/secret-expansion.yaml:1`

  Verify that `git grep -n 'plan\.md' -- ':!openspec/changes/archive'` prints nothing.
- [x] 3.4 Delete `docs/plan.md`, and `docs/` if it is empty.

## 4. Verification

- [x] 4.1 For every `## ` and `### ` heading of `git show HEAD:docs/plan.md`, a script reports
  where it went: the baseline archive, a draft, the context, or the "dropped" list in design.md.
  No heading may be unaccounted for.
- [x] 4.2 `just test` is green, and `openspec list` shows `phase-8-acceptance` and `phase-9-…`
  through `phase-13-…`, and nothing else.

## Workflow follow-up

- `/opsx:archive retire-plan-md` after approval. It creates
  `openspec/specs/dev-environment/spec.md`.
- Commit together with the pending README update and the archive of `require-platform-for-apps`.
