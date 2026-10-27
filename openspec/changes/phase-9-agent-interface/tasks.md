# Tasks

## 1. Commit of the running artifact

- [x] 1.1 Add `Commit` to `cluster.AppState`, parsed from `status.artifact.metadata["org.opencontainers.image.revision"]` of the OCIRepository `deploy` (`<ref>@sha1:<sha>` or a bare 40-digit SHA, otherwise empty); verify with table-driven cases in `internal/cluster` (workflow form, bare SHA, `local`, missing metadata)
- [x] 1.2 Print a `commit` line in `shelf app status <app>` (left out when empty) and update the README CLI reference; verify with `just golden` and a CLI test for both cases (spec app-delivery "Commit of the running artifact", "Artifact without a commit")

## 2. JSON documents

- [x] 2.1 Add `--json` plumbing to `internal/cli`: document envelope (`apiVersion`, `kind`), stdout-only output, `Error` document from `cli.Execute` with the message also on stderr; verify with a CLI test that an unknown app yields the exact `Error` JSON and exit status 1
- [x] 2.2 Add the document types and mapping functions for `AppList` and `AppStatus` in `internal/cli/json.go`, with `shelf app status [app] --json`; verify with golden files in `internal/cli/testdata/json/` for an empty list, two apps, a healthy app and a failing app, and a test that no field is `null`
- [x] 2.3 Add `TagList` (`shelf app tags --json`, `current: true` on the running tag), `ConnectionList` (`shelf connection list --json`, no token, account or "not on this machine") and `Version` (`shelf version --json`); verify with golden files, using only obviously fake account IDs
- [x] 2.4 Check that `shelf app secrets --json` fails as an unknown flag; verify with a CLI test
- [x] 2.5 Document `--json`, the kinds and the stability rule in the README CLI reference; verify that the documented examples match the golden files

## 3. Waiting for a verdict

- [x] 3.1 Add `cluster.AppRollout`: the diagnosis plus the deploy artifact's revision and commit, the Kustomization's `lastAppliedRevision` and `lastAttemptedRevision`, the digest of the current values (ConfigMap `shelf-values` merged with the HelmRelease's inline values, as helm-controller computes `configDigest`), the HelmRelease's `history[0].configDigest`, `lastAttemptedConfigDigest` and whether it is `Reconciling`; add it to the `ops.Cluster` interface and the fake; verify with tests against unstructured fixtures and a digest test against the values and `configDigest` helm-controller wrote on the dev cluster (`testdata/configdigest`)
- [x] 3.2 Implement `ops.Wait(ctx, app, commit, timeout)` with the caught-up rules of design decision 4 (commit prefix, applied revision, Failed values only for an attempted revision, release digest equal and not reconciling, pod look-ahead only for an attempt of the current values, then the five stages; only a Failed deploy-artifact stage ends a commit wait early); verify with table-driven tests for: already Ready, rollout in progress, previous values still released, previous upgrade still running or rolling back, old artifact Failed while waiting for a new commit, registry refusal, image pull failure, timeout, artifact without commit
- [x] 3.3 Add `--wait`, `--commit` (at least 7 hex digits, validated before polling) and `--timeout` (default 10m) to `shelf app status <app>`, the `WaitResult` document and its three-line text form, and `cli.ExitError` mapped by `cli.Execute` to 0/1/2 without a second error line; verify with CLI tests for each outcome and exit status, and golden files for `WaitResult`
- [x] 3.4 Verify that the wait never writes: a fake cluster that fails every write method is used in the 3.2 tests and stays untouched (spec "Waiting is read-only")
- [x] 3.5 Document `--wait`, `--commit` and the exit statuses in the README CLI reference

## 4. `shelf app open`

- [x] 4.1 Add `cluster.ForwardTraefik`: resolve Service `traefik/traefik`, pick a ready pod from its EndpointSlices, map port 80 to the pod's `web` port, open a client-go port-forward on `127.0.0.1:0`, return the address and a done channel; verify the pod and port selection with unit tests (no ready pod, named and numeric target ports)
- [x] 4.2 Add the proxy in `internal/ops`: `Host` set to `AppHost` (also for quick-tunnel apps), `X-Forwarded-Host`/`-Proto`, `Location` rewrite for the app's own host, reconnect after the session ends, 502 while it is down; verify with `httptest` against a fake forward (Host seen, redirect rewritten, WebSocket upgrade passes, reconnect after close)
- [x] 4.3 Add `shelf app open <app> [--port] [--browser]`: app existence checked before listening, bind `127.0.0.1` only, clear error for a port in use, `Options.OpenBrowser` seam (`open` on macOS, `xdg-open` on Linux); verify with CLI tests (unknown app, port in use, browser called only with `--browser`, listener address is loopback)
- [x] 4.4 Document `shelf app open` in the README (CLI reference and "Deploying an app": how to reach a private app), and note in `--help` that it is not an exposure

## 5. Tenant template

- [x] 5.1 Add `examples/tenant/.claude/skills/shelf/SKILL.md` (push, `--wait --commit "$(git rev-parse HEAD)" --json`, the three exit statuses, read stage/reason/hint, no exposure, `shelf app open` is for the person) and a `CLAUDE.md` section with the `<app>` placeholder; verify that `examples/tenant` contains no `.claude/settings.json` hook and that the commands in the skill match the CLI's `--help`
- [x] 5.2 Mention the agent loop in `examples/tenant/README.md` and in the README section "Deploying an app"

## 6. Level-2 acceptance

- [x] 6.1 Add `hack/smoke/agent.sh` and `just smoke-agent` as in design decision 7 (status JSON, wait for a new commit with exit 0 only after it rolled out, failing image with exit 1, unknown commit with exit 2, `open` against a whoami fixture with `/api` + `stripPrefix`, cleanup); verify that `just smoke-agent` passes on a fresh `just cluster-reset`
- [x] 6.2 Add `smoke-agent` to the level-2 matrix in `.github/workflows/ci.yml` and to the CLAUDE.md dev-environment table; verify that the CI job runs it on a pull request
- [x] 6.3 Run `just test` and `openspec validate --all --strict`; both pass

## 7. Faster CI

- [x] 7.1 Add `.github/scripts/select-smokes.sh` (changed paths to smoke tests, unlisted paths to all of them); verify on the paths of both commits of this branch (the roadmap commit selects none)
- [x] 7.2 Rework `.github/workflows/ci.yml` as in design decision 8 (plan job, label and draft rules, concurrency, `SMOKE_FULL`, Go cache, `ci-main-red` issue on main); verify the plan logic by running its script for every event kind
- [x] 7.3 Add `SMOKE_FULL` and the Flux nudge to `hack/smoke/agent.sh`, and limit `gofmt` in `just test` to tracked files; verify with `just test` and `just smoke-agent` with `SMOKE_FULL=0` and `1`
- [x] 7.4 Gate `release.yml` on a successful CI run on main; verify the query against the GitHub API for a green commit and an unknown one
- [ ] 7.5 Update the spec `dev-environment` (delta), CLAUDE.md and the context; verify `openspec validate --all --strict` and that a pull request run shows the selected matrix in its summary

## Workflow follow-up

- Acceptance by the maintainer: an agent in a copy of `examples/tenant` pushes, waits and reads the verdict without parsing text; `shelf app open` serves a private app in the Mac's browser through the devcontainer's forwarded port.
- `/opsx:archive` merges the deltas into `openspec/specs/`; update CLAUDE.md Status, README Status and the context; stop for approval before Phase 10.
