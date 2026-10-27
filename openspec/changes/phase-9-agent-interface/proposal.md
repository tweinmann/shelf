# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

shelf's users run several coding agents in parallel. An agent that pushes a commit has to learn
whether it arrived and what is broken without parsing text written for people, and a person has
to open a private app in a browser, which today needs a public address.

## What Changes

- `--json` with stable fields for `shelf app status` (all apps and one app, including the five
  stages and the hints) and the other agent-facing commands.
- `shelf app status <app> --wait`: wait for the rollout of a given revision and return a condensed
  result (stage, reason, hint), suitable for a Claude Code hook after `git push`.
- `shelf app open <app>`: a local port forwarded through the API server to Traefik, with the Host
  header set to the app's internal host, so routes and `stripPrefix` behave as through a tunnel.
  CLI only, not the admin UI.
- The tenant template gains a Claude Code skill or `CLAUDE.md` snippet that explains the loop:
  push, wait, read the status, fix.

## Capabilities

### New Capabilities

- `agent-interface` (planned), or changes to `app-delivery` and `app-exposure`: to be decided at
  `/opsx:propose`.

### Modified Capabilities

To be decided at `/opsx:propose`; likely `app-delivery` (status, `--json`, `--wait`) and
`app-exposure` (`shelf app open`).

## Impact

`internal/cli`, `internal/ops`, `internal/cluster` (port-forward through the API server),
`examples/tenant`. Level 1 for the JSON output, level 2 for `--wait` and `open`.

## Roadmap text (from the re-cut of 2026-10-27)

First phase of the core thesis: an agent interface before previews, so every later phase can be
accepted by an agent in a tenant repository.

`shelf app open` uses the same trick as quick tunnels' `--http-host-header`: the forward ends at
Traefik, not at a pod, so the request takes the path a tunnel's would. It also makes private apps
usable from a browser, which they are not today. It is the way previews are reached (Phase 10);
LAN access with certificates stays rejected (router DNS rebind protection, Colima VM networking,
DNS-01 needing a Cloudflare token in the cluster, renewal, the LAN not being a trust boundary, no
access from outside).

The guardrail "agent-facing commands offer `--json` with stable fields" was adopted with the
re-cut; this phase defines which commands are agent-facing and what "stable" means (field names,
additive changes only, a version field or not).

**Acceptance:** an agent in a tenant repository can push, wait and read a machine-readable verdict
without parsing text output; `shelf app open` serves a private app with multiple routes,
`stripPrefix` included, in a browser on the developer's machine.
