# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

Running apps needs more than deploying them: logs, resource usage, a warning before the disk is full, and updating shelf itself.

## What Changes

- Live pod logs including the previous container, CPU and memory per pod, a VM disk warning.
- "Update shelf" as one job for binary, platform tag and chart version.
- The Advanced page with the kubeconfig.

## Capabilities

### New Capabilities

- `app-logs`, `resource-usage`, `self-update` (planned), and changes to `admin-ui`.

### Modified Capabilities

To be decided at `/opsx:propose`.

## Impact

`internal/server`, `internal/cluster`, `internal/ops`, release artifacts. Level 2, and level 3 for the update.

## Roadmap text (from the plan before OpenSpec)

Live pod logs including the previous container of a crash-looping pod, instantaneous CPU and
memory per pod, a warning when the VM disk fills up (local-path enforces no quota, so one app can
fill it), "update shelf" as one job that moves the binary, the platform tag and the chart version
together, and the Advanced page with the kubeconfig.

**Acceptance:** a crash-looping app shows its previous container's log in the UI; the dashboard
warns above 85 % VM disk usage; an update from one release to the next runs from the browser and
the three versions shown afterwards agree; the kubeconfig download is refused without a re-entered
password; every UI action has a documented CLI equivalent.
