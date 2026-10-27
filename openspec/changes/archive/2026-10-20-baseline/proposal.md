# Proposal

## Why

shelf was planned and built in Phases 0 to 8d before it adopted OpenSpec on 2026-10-20. All of
that lived in one document, `docs/plan.md`. This archived change is where that document went
when OpenSpec became the only source: nothing here is current, and nothing here is edited
again. What shelf does today is in `openspec/specs/`; what it is about to do is in
`openspec/changes/`.

## What Changes

- `design.md`: the Decisions and Validated assumptions of Phases 0 to 8d, and the reference
  sections of the plan (architecture, rendering pipeline, handover contract, schema, CLI,
  repository layout) as they stood on 2026-10-20, all verbatim.
- `phases.md`: the development and test environment, and the goals, acceptance criteria and
  results of Phases 0 to 8d, verbatim.
- The 7 baseline specs in `openspec/specs/` were written from this text and the code; where they
  disagree, the specs and the code are right.

## Capabilities

### New Capabilities

None. The baseline specs were added directly, on 2026-10-20.

### Modified Capabilities

None.

## Impact

None; a record only. Stale statements are kept as they were, because they record what was
decided at the time.

## Context

A Mac mini (Apple Silicon) is to run as a personal app platform, published as a GitHub product
named **shelf**. Users build their apps outside the platform (GitHub Actions) and deliver images
plus a deploy artifact to a registry. The platform watches the artifact and deploys
automatically. Apps are reachable through Cloudflare Tunnel at `<app>.<domain>`.
Learning project for platform engineering, not for production use.

**Target product, revised on 2026-09-20 (after Phase 5).** Until here shelf was a command-line
tool that the maintainer drives from a devcontainer: this document described the operator as
someone with a terminal, SSH and kubectl, and listed an overview page as out of scope. That
changes. shelf becomes an **appliance**: it installs on the Mac mini with one command and a
browser wizard, and it serves an **admin UI** in which apps are registered, changed and removed
by someone who neither sees nor needs to understand Kubernetes. Resource usage, logs and similar
follow later. The substrate stays deliberately reachable with kubectl for anyone who wants it,
and the CLI remains the expert interface — every action in the UI has a CLI equivalent, and both
call the same function. What the user still has to bring from outside does not change: a GitHub
account with a classic PAT, a Cloudflare domain with an API token, and per app a repository with
`app.yaml` plus the unchanged workflow. The registry stays the only interface between build and
platform. Everything from Phase 6 on follows from this; the decisions are in the table
"Decided on 2026-09-20" below.

The repository directory is empty — greenfield start.

This plan is the validated version of an earlier draft. The technically risky assumptions were
checked against sources (see "Validated assumptions"), open decisions were settled with the
maintainer, and several gaps were closed (GitOps drift during app registration, missing
external-dns target annotation, non-working reference example).

**Ordering:** installation on the Mac mini moves to the end. Everything is developed and tested
first inside a devcontainer on the MacBook against a local k3d cluster; the mini joins only as
the target machine once the platform works.

**Language:** everything in the repository is English — code, comments, docs, commit messages,
CLI output. Conversation with the maintainer may be German or English. This plan is written in
English because it is committed to the repo as `docs/plan.md`.
