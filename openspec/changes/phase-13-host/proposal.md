# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

Everything so far runs in the devcontainer against k3d. shelf needs a real host: a Linux machine
with k3s, or a Mac that runs the same Linux in a Colima VM, prepared, diagnosable and resettable
without a developer at the keyboard.

## What Changes

- `shelf init host`, split into "provide the environment" (macOS: preflight, tools, energy
  settings, host name, Colima VM; Linux: preflight and k3s directly) and "install shelf's
  components", which is the same on both.
- `shelf doctor` on top of the five-stage diagnosis, and `shelf destroy [--purge]`.
- The `shelf init` wrapper, `just push-mini` and the SSH access from the devcontainer.

## Capabilities

### New Capabilities

- `host-setup` (planned): preflight and preparation of the host, macOS and Linux.
- `diagnostics` (planned, or a change to `app-delivery`): `shelf doctor`.
- `teardown` (planned): `shelf destroy`.

### Modified Capabilities

To be decided at `/opsx:propose`.

## Impact

`internal/host` (tested through the fake command runner only), CLI, justfile, `.devcontainer/ssh_config`. Level 3: needs the mini and a Linux host.

## Re-cut of 2026-10-27

Formerly Phase 9 (Mac mini). The core thesis (Phases 9 to 12) now comes first, and the Mac
lock-in is dropped: shelf runs on any Linux host with k3s, and on macOS the installer creates the
Colima VM and then runs the same Linux bootstrap. Tier 1: macOS (Apple Silicon, Colima) and
Ubuntu/Debian (arm64, amd64). The guardrail "no Colima, macOS or distribution assumptions outside
`internal/host`" covers both.

**Open:** what the Linux preflight checks (k3s already installed or not, cgroup setup, distribution
versions), and whether k3s is installed by its upstream script or by shelf.

## Roadmap text (from the plan before OpenSpec)

The old Phase 6, without the installer. `shelf init host`: preflight (Apple Silicon, RAM, disk,
macOS version, Rosetta, Homebrew, tool versions, existing profile, energy settings), tool
installation, disable sleep, host name, Colima profile. Plus `shelf doctor` on top of
`ops.Diagnose`, `shelf destroy [--purge]`, the `shelf init` wrapper, `just push-mini`, and the SSH
setup above.
Since the devcontainer is Linux, `internal/host` cannot run there for real: it is tested with a
fake command runner (expected `brew`/`pmset`/`colima` calls and their order) and executed for
real only on the mini.

**Acceptance:** one command on the mini brings Colima and the platform up; `shelf doctor` reports
every layer and names the first broken one; `shelf destroy --purge` followed by `shelf init`
returns to a working platform; kubectl from the devcontainer through the SSH forward reaches the
mini; the experiment on starting Colima from a LaunchAgent without a GUI login has been run and
its result is recorded here.

### Mac mini access for development

This is how shelf is *developed and tested* against the mini, not how the mini is operated: since
2026-09-20 the mini is run through its admin UI (see "Components on the mini"). The SSH forward,
the separate kubeconfig and `just push-mini` stay as level-3 test tooling for the maintainer.

Almost nothing runs over SSH — instead the API server is brought into the devcontainer:

```
# .devcontainer/ssh_config (template, HostName adjustable locally)
Host mini
  HostName mini.local
  ControlMaster auto
  ControlPath ~/.ssh/cm-%r@%h:%p
  ControlPersist 10m

ssh -N -f -L 6444:127.0.0.1:6443 mini
```

Colima binds the k3s API to `127.0.0.1:6443` on the mini. The forward runs in the devcontainer;
the k3s server certificate has `127.0.0.1` as an IP SAN and the port is irrelevant for
verification, so TLS works without `insecure-skip-tls-verify`. The mini's kubeconfig is a
separate file `~/.kube/shelf-mini.yaml` in the container; switching happens only explicitly via
`KUBECONFIG`, never via a context switch in a shared file. `kubectl`, `helm`, `flux`, `stern`
then run from the devcontainer against the mini. Only `shelf init host` and `colima` remain for
SSH.

VS Code forwards the Mac's SSH agent into the devcontainer automatically, so keys never leave the
Mac. The Mac's `~/.ssh/config` is **not** mounted — hence the template in the repo.

The binary is copied rather than syncing source. **Caution:** the devcontainer is
`linux/arm64`, the mini is `darwin/arm64`. A plain `go build` produces a Linux binary that fails
on the mini with "exec format error". `just push-mini` therefore builds explicitly with
`GOOS=darwin GOARCH=arm64` and copies via `scp`.

### Open items due in this phase

- **Mini hardware** (chip, RAM, macOS version) determines Colima sizing defaults and whether the
  "vanilla Mac" first run can be tested in a `tart` VM. Apple's Virtualization framework only
  supports nested virtualization from M3 and macOS 15; on M1/M2 the substitute is a tested
  `shelf destroy --purge` ("back to vanilla" instead of "start from vanilla"). Until the hardware
  is known, the wizard proposes half the CPUs, half the RAM and 60 GB of disk, all editable.
- **Does Colima's `vz` VM start from a LaunchAgent without a GUI login?** The highest-impact
  unknown of the new plan, because it decides whether automatic login is required: an agent in
  `~/Library/LaunchAgents` loads when a user session is created, and a headless mini has nobody to
  log in. Plan: ship automatic login as the documented path, run the experiment with
  `launchctl bootstrap user/<uid>` from a daemon in this phase, and drop automatic login only if it
  demonstrably works without it. Automatic login in turn requires FileVault to be off, so the disk
  holding the tokens and the secret backups is unencrypted — accepted for a home appliance whose
  threat model is the LAN, but it belongs in the documentation, not in a footnote.
- **The macOS application firewall** shows a GUI dialog when an unsigned binary opens a listening
  socket — and nobody is sitting in front of a headless mini. `install.sh` adds the binary with
  `socketfilterfw --add` while it still has sudo, and `shelf doctor` checks the state. Easy to
  miss, unpleasant to debug remotely.
