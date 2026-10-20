# Proposal

> Verification only: no behaviour changes, so this change keeps `skip_specs: true` for good.

## Why

Phases 8b (connections) and 8d (quick tunnels) are implemented and green at level 1, but their
acceptance has not been run against a real cluster and real Cloudflare. Until it has, the specs
`connections` and `app-exposure` describe behaviour that has only met fakes.

## What Changes

- Run the level 2 and 3 checks of 8b and 8d and record the results in `tasks.md`.
- Fix what they find in a change of its own, which then also corrects the affected specs.
- Publish release v0.5.0 once both are accepted.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. A defect found here becomes its own change.

## Impact

The dev cluster and the Cloudflare account `tobile` (records and tunnels named `shelf-dev-<app>`
only). The maintainer runs every step that needs the Cloudflare token.

## Acceptance (from the plan before OpenSpec)

**8b:** an app with a registry connection pulls a private artifact and private images with it,
and a new login for the connection keeps it pulling; an app with a Cloudflare connection answers
over HTTPS under its own domain through a tunnel of its own in the connection's account, with
cloudflared in its namespace; moving it to another domain or connection and taking it off the
internet leave no record and no tunnel behind; a connection in use can neither be removed nor
moved to another account; the Cloudflare token is in no Secret, no log line and no page; a cluster
from before this phase is migrated by `init cluster` with its apps still pulling.

**8d:** an app added with `--quick` answers at the trycloudflare name shelf printed, with its own
host name in the Host header; `shelf app status` and the UI show the new name after cloudflared
restarts; `--private` stops cloudflared, `--quick` starts it again; an app without `--quick`, and
every app from before, stays private.
