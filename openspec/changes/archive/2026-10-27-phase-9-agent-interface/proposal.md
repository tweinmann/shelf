# Proposal

## Why

shelf's users run several coding agents in parallel, and each agent has to learn whether its
push arrived and what is broken. Today it has to parse text written for people, and it cannot
tell its new commit from the old one that was already running. The person behind the agents
cannot open a private app in a browser without giving it a public address. Both block the core
thesis: branch previews (Phase 10) are only useful if an agent can wait for one and read its
verdict, and if a person can look at it without exposing it.

## What Changes

- `--json` on the read-only agent-facing commands: `shelf app status` (all apps and one app),
  `shelf app tags`, `shelf connection list` and `shelf version`. The output is a versioned
  document with its own types (`apiVersion: shelf.dev/v1alpha1`, a `kind`), changed only
  additively within a version. `shelf app secrets` gets no `--json`, so secret values don't end up
  in an agent's transcript, and the mutating commands keep their text output.
- `shelf app status <app> --wait [--commit <sha>] [--timeout]`: waits until the app's chain
  is Ready or Failed. With `--commit`, it first waits until the deploy artifact carries that git
  commit. It prints a condensed verdict (outcome, stage, reason, hint) and exits 0 (Ready), 1
  (Failed) or 2 (timed out). It stays read-only: it never asks Flux to reconcile.
- `shelf app status` shows the git commit the running deploy artifact was built from. shelf reads
  it from the artifact's revision annotation, which the OCIRepository reports, so no forge API is
  involved.
- `shelf app open <app> [--port] [--browser]`: a local listener on `127.0.0.1` only that forwards
  through the API server to Traefik and sets the Host header to the app's own host, so routes and
  `stripPrefix` behave as they do through a tunnel. It runs until it is stopped. CLI only.
- `examples/tenant` gains a Claude Code skill (`.claude/skills/shelf/SKILL.md`) and a `CLAUDE.md`
  section that explain the loop: push, wait for the commit, read the verdict, fix. There is no
  hook.

- CI runs level 2 when a pull request is ready rather than on every push, only the smoke tests its
  paths need, and everything in full after each merge to main, which opens an issue when it fails;
  a release needs a green main.

## Capabilities

### New Capabilities

- `agent-interface`: the machine-readable contract for agents: the versioned JSON documents and
  which commands offer them, the wait verdict with its exit codes, and the agent instructions in
  the tenant template.

### Modified Capabilities

- `app-delivery`: the app status command also shows the commit the running artifact was built
  from.
- `dev-environment`: when CI runs which level-2 checks, the issue for a red main, the release
  gate.
- `app-exposure`: a new way to reach an app: a local port-forward to Traefik with the app's Host,
  for private and exposed apps alike, on the loopback interface only.

## Impact

- `internal/cluster`: reads the commit from the OCIRepository status, and opens a port-forward
  connection to a Traefik pod through the API server (client-go `tools/portforward`, no kubectl).
- `internal/ops`: the wait loop, and the Host-rewriting proxy for `open`.
- `internal/cli`: the JSON document types, `--json`, `--wait`, `--commit` and the `open` command.
  Golden files for every JSON document.
- `examples/tenant`: skill and `CLAUDE.md`. README CLI reference.
- No new direct Go dependency. client-go's port-forward brings three indirect modules of its own
  graph (`gorilla/websocket`, `moby/spdystream`, `k8s.io/streaming`). No change to the platform
  artifact, the chart or `build.yml`.
- Level 1 for the JSON documents and the verdict logic (fake cluster). Level 2 for `--wait`
  against a real rollout and for `open`, in a new `just smoke-agent`.
