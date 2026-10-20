# Tasks

## 1. Phase 8b at level 2 and 3

- [ ] 1.1 Install the dev cluster with a host suffix: `just init-cluster --yes --host-suffix -dev`.
  Verify with `kubectl -n flux-system get cm shelf-config -o jsonpath='{.data.SHELF_HOST_SUFFIX}'`,
  which must print `-dev`.
- [ ] 1.2 Check whether the shared tunnel of Phase 5 and the record `greeter-dev.tobile.ch` still
  exist in Cloudflare (the dev cluster was reset on 2026-10-20), and if they do, have the
  maintainer delete them by exact name. Verify that neither is listed in the Cloudflare dashboard.
- [ ] 1.3 Private registry: add an app from a private artifact with `--registry ghcr`, then give
  the connection a new login with `shelf connection add registry ghcr`. Verify that the app is
  Ready before and after, and that `just smoke-registry <private image>` passes.
- [ ] 1.4 The maintainer runs `just smoke-expose <app> tobile.ch`. Verify that it passes: HTTPS
  under its own domain, then the move to another domain and off the internet with no record or
  tunnel left behind.
- [ ] 1.5 Refusals and token hygiene: removing a connection in use and moving it to another
  account are refused; `grep` over `kubectl get secrets -A -o yaml`, the job logs and the
  rendered pages finds no Cloudflare token. Verify with the command outputs in this task's
  notes.
- [ ] 1.6 `form.js` against the real connections: the package, tag and zone selects on the new-app
  page fill from GitHub and Cloudflare, and the free-text fallback works when a lookup fails.
  Verify by hand in `just serve`, with screenshots or notes here.

## 2. Phase 8d at level 2

- [ ] 2.1 The maintainer runs `just smoke-quick`. Verify that it passes: the trycloudflare name
  answers with the app's own Host, a new name appears after a cloudflared restart, `--private` and
  `--quick` switch it, and `rm` cleans up.

## 3. Close

- [ ] 3.1 Record the results in CLAUDE.md's status (8b and 8d complete) and in the README Status
  table. Verify with `git diff`.
- [ ] 3.2 Tag and publish release v0.5.0. Verify that the release workflow is green and the
  release lists the binaries, the chart and the platform artifact.
