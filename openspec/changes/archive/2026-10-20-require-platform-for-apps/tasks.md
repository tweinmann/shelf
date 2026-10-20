# Tasks

## 1. Platform check in internal/cluster

- [x] 1.1 Replace `checkQuickTunnels` in `internal/cluster/tunnel.go` with an exported
  `CheckPlatform(ctx, cfg, quick bool) error`. Its decision goes into a pure function over the
  fetched ResourceSet (nil means missing). That function returns "this cluster has no shelf
  platform; install it with `shelf init cluster`" when the ResourceSet is missing. With `quick`
  set and no `quick` input, it returns "the platform in this cluster does not run quick tunnels
  yet; install the platform of this shelf with `shelf init cluster` first, or add the app without
  --quick". Verify with a table-driven test in `internal/cluster/tunnel_test.go` covering missing,
  old with quick, old without quick, and current. `TestPlatformRunsQuickTunnels` still passes.
- [x] 1.2 Remove the quick check from `cluster.AddApp` in `internal/cluster/apps.go`. Verify that
  `go vet ./internal/cluster` reports no unused symbol and that `go test ./internal/cluster` passes.

## 2. Call it first in internal/ops

- [x] 2.1 Add `CheckPlatform(ctx, cfg, quick bool) error` to the `Cluster` interface in
  `internal/ops/ops.go`, implemented by `liveCluster` through `cluster.CheckPlatform`. Add it to
  the CLI fake in `internal/cli/harness_test.go` with a switch for "no platform" and one for "old
  platform". Verify that `go build ./...` and `go vet ./...` pass.
- [x] 2.2 In `ops.AddApp`, call `CheckPlatform` as soon as the quick flag the app will end up with
  is known, from `opts.Access` and the current config, so a re-add of a quick
  app is checked too. Verify with CLI tests in `internal/cli`:
  - `app add` against "no platform" fails with the new message, and the fake records no applied
    object, no Cloudflare call and no backup file.
  - `app add --quick` against "old platform" fails with the corrected hint.
  - `app credentials greeter --quick` against "old platform" leaves the tunnel and the record
    untouched.
- [x] 2.3 In `ops.Redeploy`, call `CheckPlatform(…, false)` before `Cluster.Redeploy`. Verify with
  a CLI test that `app redeploy` against "no platform" fails at once with the new message.
- [x] 2.4 Check that no golden file changed: run `go test ./internal/server ./internal/cli` and
  `git status internal/*/testdata`. A successful add prints exactly what it printed before.

## 3. Dev bootstrap

- [x] 3.1 In `justfile`, make `init-cluster` depend on `platform-push` and `chart-push`, and
  update its comment. Update the `just init-cluster` row in CLAUDE.md. Verify with
  `just --dry-run init-cluster` that both pushes come before `shelf init cluster`.

## 4. Verification

- [x] 4.1 Run `just test`; it must be green, including `openspec validate --all --strict`.
- [x] 4.2 Level 2, by hand in the dev cluster:
  - Run `just cluster-reset`, then `go run ./cmd/shelf app add hello-platform
    oci://shelf-registry:5000/shelf/hello:dev --insecure-registry` with no Cloudflare
    connection. The artifact need not exist, because the check comes first. It fails within
    seconds with "no shelf platform", and `kubectl get ns shelf-system` reports it missing or
    `kubectl -n shelf-system get resourcesetinputprovider` shows nothing.
  - Then run `just init-cluster --yes`. It succeeds without a prior manual push.
  - Then `just smoke-apps` passes.

## Workflow follow-up

- `/opsx:archive require-platform-for-apps` after the maintainer approves. It merges the new
  requirement into `openspec/specs/app-delivery/spec.md` and the corrected one into
  `openspec/specs/app-exposure/spec.md`.
- Add a one-line Status note to CLAUDE.md when archiving.
