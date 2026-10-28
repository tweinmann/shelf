# Tasks

## 1. Tests in app.yaml

- [x] 1.1 `internal/schema`: `tests` map with `image|build`, `command`, `args`, `env`, `resources`.
  Validation:
  - test names: DNS-1035, at most 40 characters, not a component name;
  - references and secrets as for components;
  - unknown fields refused (`route`, `ports`, `volumes`, `health`, `instances`).

  Verify: table tests for the scenarios of "Tests section"; `just golden` refreshes
  `schema/app.schema.json`.
- [x] 1.2 `shelf render`: pin test images, take `--image <test>=<reference>` for built tests, and
  write `tests` into the normalized app.yaml. Verify: render tests for an image test and a built
  test, plus the `--image` errors for an unknown or doubled name.
- [x] 1.3 `shelf build-plan` lists built tests after the components. Verify: a build-plan test with
  `web` and `e2e`.
- [x] 1.4 `examples/tenant/app.yaml` gets a curl test against `/healthz`. Verify: `just test`
  validates the example. Document `tests` in the README's app.yaml reference.

## 2. Chart

- [x] 2.1 `charts/shelf-app`: one hook Pod `<app>-test-<test>` per test:
  - annotations `helm.sh/hook: test` and `before-hook-creation`;
  - `restartPolicy: Never`, labels with `shelf.dev/test`;
  - env, references and secrets as for a component;
  - resources and the pull secret.

  Verify: golden files for a test with an image and with a secret; `helm lint` and kubeconform in
  `just test`.

## 3. Platform

- [x] 3.1 `platform/apps` and `platform/previews`:
  - namespace labels: `enforce: baseline`, `warn` and `audit: restricted`, `*-version: latest`;
  - the NetworkPolicy `shelf-isolation` of design decision 3, in every app and preview namespace;
  - in `apps` only, one `ipBlock` per entry of the input `lan`.

  Verify: `platform_test.go` renders an app without and with `lan` and a preview of an app with
  `lan`, and checks the policy and the labels.
- [x] 3.2 HelmRelease `test.enable: true`, with `ignoreFailures: false` for apps and `true` for
  previews. Verify: rendered in `platform_test.go`.

## 4. Cluster state

- [x] 4.1 `internal/cluster`:
  - the provider input `lan` in `AppOptions` and `AppConfig`;
  - `NodePodCIDRs` reads `spec.podCIDRs` of the nodes.

  Verify: unit tests against the fake client.
- [x] 4.2 The stage "the tests" after the pods:
  - read from the HelmRelease history's `testHooks` and the condition `TestSuccess`, for the
    revision that runs;
  - the last 20 log lines of a failed test pod;
  - test pods (`shelf.dev/test`) left out of the pods stage.

  Verify: state tests for none, running, passed, failed on an app (rolled back) and failed on a
  preview; the diagnosis golden files gain the sixth stage.
- [x] 4.3 Isolation state:
  - the Pod Security level from the namespace labels;
  - `restricted` violations through a server-side dry-run patch with a `WarningHandler`;
  - the LAN exceptions.

  Also a Pod Security hint for a `FailedCreate` refused by `baseline`. Verify: unit tests for
  parsing the warnings and for the hint.
- [x] 4.4 `cluster.verification` counts test images under "the scan covers every image". Verify: a
  verification test where the scan lacks the test's digest.

## 5. Operations

- [x] 5.1 `ops`: the LAN option of add and credentials:
  - kept when not given, replaced by the given list, cleared by deny;
  - checked as a private CIDR that does not overlap a node's pod range.

  Verify: `internal/cli` tests for the scenarios of "LAN exceptions are the operator's decision".
- [x] 5.2 `Verdict`: Ready only when the awaited revision's tests passed; Failed at once for a
  failed test. Verify: wait tests for both, and for an app without tests.
- [x] 5.3 `ops.Report`: status parts plus, for a preview, the comparison with the app by component
  digest and by findings. Verify: unit tests for an app and for a preview with one changed
  component.

## 6. CLI

- [x] 6.1 `--allow-lan <cidr>` (repeatable) and `--deny-lan` on `app add` and `app credentials`.
  Verify: harness tests for keep, replace, clear and each refusal.
- [x] 6.2 `shelf app status`: the tests and isolation lines, six stages. JSON: `tests` and
  `isolation` in `AppStatus`. Verify: refreshed text and JSON golden files, plus one for a failed
  test.
- [x] 6.3 `shelf app report <app> [--preview <branch>] [--json]`: Markdown and `Report`. Verify:
  golden files for an app, a preview and JSON; the README CLI table.

## 7. Admin UI

- [x] 7.1 App page: the tests stage in `How it gets here` and an isolation card. Access form: a LAN
  field that calls the same `ops` function, with the form shown again on an invalid range.
  Verify: page golden files, and a server test for a valid and an invalid range.

## 8. Dev environment and docs

- [x] 8.1 `hack/smoke/hardening.sh` as in design decision 10, plus a `just smoke-hardening`
  recipe, an entry in `select-smokes.sh` and in `ci.yml`. Verify: it passes on a fresh dev cluster
  (level 2).
- [x] 8.2 Docs:
  - CLAUDE.md: guardrail "Apps are isolated" enforced by the policy, LAN exceptions as an operator
    decision, conventions (`lan` input, `shelf-isolation`, `shelf.dev/test`), dev table row;
  - `openspec/config.yaml` context;
  - README: isolation, tests, report;
  - `examples/tenant`: `CLAUDE.md` and the skill name the tests stage and `shelf app report`.

  Verify: `just test`.

## 9. Acceptance

- [x] 9.1 Level 2: run every smoke test against a fresh dev cluster:
  - all of `select-smokes.sh` in full;
  - `smoke-chart` and `smoke-agent` check that probes still pass under the policy;
  - `smoke-quick` checks that the quick tunnel's name is still read through the pod proxy.

  Record the turnaround with and without a test in design.md under "Results".
- [x] 9.2 Level 3 with `tweinmann/shelf-hello`:
  - add a curl test and push to main: Ready;
  - a branch whose test fails: a red preview that keeps running, then `shelf app report` for it;
  - merge a failing test: `gretter` keeps the previous version.

  Record the times in design.md.

## Workflow follow-up

- Archive with `/opsx:archive phase-12-hardening`; update CLAUDE.md Status, the README Status row
  12 and the context.
