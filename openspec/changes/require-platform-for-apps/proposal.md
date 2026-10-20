# Proposal

## Why

On 2026-10-20, `shelf app add greeter` against a freshly reset dev cluster hung at "waiting for the
deploy artifact" until its timeout. The dev registry was empty, so `shelf init cluster` had no
platform to install, and without the platform's ResourceSet `apps` nothing ever creates the app.
Before the wait started, the add had already replaced the app's Cloudflare tunnel and written its
secrets and provider. An app operation must not write anything or touch Cloudflare in a cluster
that cannot deploy apps. And the dev recipe that installs the platform should not depend on
remembering to push it first.

## What Changes

- `shelf app add`, `shelf app credentials` and `shelf app redeploy` (and the matching admin UI
  jobs) check that the platform is installed before anything else: no secret, provider, tunnel or
  DNS record is written. Without the ResourceSet `apps` they fail at once and say to run
  `shelf init cluster`.
- The existing check for quick tunnels becomes part of the same check, and its hint is corrected:
  it said to add the app "with --private", a flag `shelf app add` does not have. It now says
  "without --quick".
- `just init-cluster` pushes the working copy's platform and chart to the dev registry before it
  runs `shelf init cluster`, so a reset cluster never gets an empty platform. Its host suffix
  stays the caller's choice.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `app-delivery`: a new requirement that app operations refuse a cluster without the platform,
  before writing anything.
- `app-exposure`: "Quick tunnel requires a current platform" runs before anything is written,
  including Cloudflare, and its hint names `--quick` instead of `--private`.

## Impact

- `internal/cluster`: a check of the ResourceSet `apps` that is exported for `internal/ops`; it
  takes over `checkQuickTunnels`.
- `internal/ops`: the `Cluster` interface gains the check. `AddApp`, which `SetAccess` goes
  through, and `Redeploy` call it first. The CLI test fake in `internal/cli/harness_test.go` gets
  the method.
- `justfile`: `init-cluster` depends on `platform-push` and `chart-push`. Same text in CLAUDE.md.
- No change to the platform, the chart, the stored objects, or the output of a successful add.
