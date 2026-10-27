# Tasks

## 1. Phase 8b at level 2 and 3

- [x] 1.1 Install the dev cluster with a host suffix: `just init-cluster --yes --host-suffix -dev`.
  Verify with `kubectl -n flux-system get cm shelf-config -o jsonpath='{.data.SHELF_HOST_SUFFIX}'`,
  which must print `-dev`.
- [x] 1.2 Check whether the shared tunnel of Phase 5 and the record `greeter-dev.<domain>` still
  exist in Cloudflare (the dev cluster was reset on 2026-10-20), and if they do, have the
  maintainer delete them by exact name. Verify that neither is listed in the Cloudflare dashboard.
- [x] 1.3 Private registry: add an app from a private artifact with `--registry ghcr`, then give
  the connection a new login with `shelf connection add registry ghcr`. Verify that the app is
  Ready before and after, and that `just smoke-registry <private image>` passes.

  Notes (2026-10-27): `acc-private` from `oci://ghcr.io/tweinmann/greeter:main` with `--registry
  ghcr` was Ready. A first `connection add` with the old token left the Secret unchanged
  (server-side apply, same content); with a fresh PAT, `connection-registry-ghcr` and the
  `shelf-registry` copies in `acc-private` and `greeter` changed within seconds, at the same time
  (`copyFrom`, no explicit reconcile needed). `shelf app redeploy acc-private` fetched the artifact
  with the new login; `acc-private` and `greeter` Ready afterwards; `just smoke-registry
  ghcr.io/tweinmann/greeter/web:sha-42a964e` run by the maintainer with the new token.
- [x] 1.4 The maintainer runs `just smoke-expose <app> <domain>`. Verify that it passes: HTTPS
  under its own domain, then the move to another domain and off the internet with no record or
  tunnel left behind.

  Notes (2026-10-27): `CF_ACCOUNT_ID=<account id> just smoke-expose
  acc-private <domain>` passed ("PASS: own tunnel in the connection's account, record published
  and corrected, HTTPS as acc-private-dev.<domain>, and nothing left behind when taken off").
  The token sees several accounts, so the script needs `CF_ACCOUNT_ID`. HTTPS answered after
  121 s; until then the Cloudflare edge returned a 500 page of the zone's old host while Traefik
  inside the cluster already answered 200. Not checkable at level 3, accepted by the maintainer:
  the script does not move the app to another domain, and the connection's account has a single zone
  (`<domain>`); the move is covered at level 1 only (`TestAppCredentialsMovesTheApp`, "another
  domain in the same account", "a connection in another account"). Taking an app off the internet
  without leaving anything behind was seen twice for real: here and with `greeter` in 1.1.
- [x] 1.5 Refusals and token hygiene: removing a connection in use and moving it to another
  account are refused; `grep` over `kubectl get secrets -A -o yaml`, the job logs and the
  rendered pages finds no Cloudflare token. Verify with the command outputs in this task's
  notes.

  Notes (2026-10-27). The maintainer checked the page sources of `/connections` and of an app in
  `just serve` and ran jobs from the UI (add, change of connections): no token on a page;
  `~/.shelf/audit.log` still 0 hits afterwards. Moving a used connection to another
  account is not checkable at level 3 (no token for a second account), accepted by the
  maintainer; covered at level 1 by `internal/cli/access_test.go` ("moving a used connection to
  another account").
  ```
  $ shelf connection rm registry ghcr
  error: the registry connection ghcr is used by acc-private, greeter; give them another one first
  $ shelf connection rm cloudflare <connection>
  error: the cloudflare connection <connection> is used by greeter; give it another one first
  ```
  `grep -cF "$token"` (token read from `~/.shelf/connections/cloudflare/<connection>.yaml`, never
  printed): `kubectl get secrets -A -o yaml` 0, its base64 form 0; ConfigMaps, providers and
  ResourceSets 0; `~/.shelf/audit.log` 0; the logs of all 18 pods 0.
- [x] 1.6 `form.js` against the real connections: the package, tag and zone selects on the new-app
  page fill from GitHub and Cloudflare, and the free-text fallback works when a lookup fails.
  Verify by hand in `just serve`, with screenshots or notes here.

  Notes (2026-10-27): checked by the maintainer in `just serve` with the connections `ghcr` and
  `<connection>`: package, tag and zone selects fill; "other package…"/"other tag…" show the text
  field; with the browser offline, changing the connection shows the text field with "the list
  could not be read". OK.

## 2. Phase 8d at level 2

- [x] 2.1 The maintainer runs `just smoke-quick`. Verify that it passes: the trycloudflare name
  answers with the app's own Host, a new name appears after a cloudflared restart, `--private` and
  `--quick` switch it, and `rm` cleans up.

  Notes (2026-10-27): the first run failed in the script, not in shelf: `shelf app status` shows
  the trycloudflare address twice (the `address` line and the routed component `web`), and the
  restart step took both matches, so `curl` got a URL with a newline in it. Fixed with `head -1`
  in `hack/smoke/quick.sh`. The second run passed: "PASS: quick tunnel on request, address
  reported, Host rewritten to hello-quick.shelf.internal, new address after a restart, --private
  and --quick, app rm". The address answered after 0 s; after `--quick` the tunnel was up in 10 s.

## 3. Close

- [x] 3.1 Record the results in CLAUDE.md's status (8b and 8d complete) and in the README Status
  table. Verify with `git diff`.
- [ ] 3.2 Merge into `main`, then tag and publish release v0.6.0 there. Verify that the release
  workflow is green, the release lists the binaries, and GHCR has the chart
  `shelf/charts/shelf-app:0.6.0` and the platform artifact `shelf/platform:v0.6.0`.

  Notes (2026-10-27): the task first named v0.5.0, but v0.5.0 had been published on 2026-09-29
  (at `0d7fe37`, before the form choices and 8d); a published tag is never moved, so the
  maintainer chose v0.6.0 on `main` after the merge.
