# Proposal

## Why

A preview runs code an agent wrote, and since Phase 11 shelf knows that code was built by its
workflow, but not that it behaves. Today a preview can reach every other app in the cluster, the
home LAN and the cloud metadata address, and it counts as green as soon as its pods are ready.
Before agents push branches unattended, a preview must be fenced in, and "green" must mean that
the app's own checks pass against it.

## What Changes

- **Pod Security.** App and preview namespaces enforce the Pod Security level `baseline` and warn
  and audit `restricted`. Pods that would violate `restricted` are listed in `shelf app status`,
  without blocking them, so stock images that start as root (Postgres) keep working. The chart
  gives every pod the two settings no image depends on (seccomp `RuntimeDefault`, no privilege
  escalation), so that list shows what the images decide.
- **Network isolation.** Every app and preview namespace gets a default-deny NetworkPolicy:
  - ingress only from its own namespace and from Traefik;
  - egress to its own namespace, to the cluster DNS and to the internet;
  - egress to other namespaces, the Kubernetes API, private address ranges (the LAN) and
    link-local addresses (cloud metadata) denied.
- **LAN exceptions, operator only.** `shelf app add|credentials --allow-lan <cidr>` (repeatable,
  `--deny-lan` clears it) lets one app reach named private addresses, for example a NAS. Like
  exposure, it is never part of `app.yaml`, and previews never get it.
- **Tests in app.yaml.** A new optional section `tests:` declares test containers (`image` or
  `build`, `command`, `args`, `env` with references such as `${web.host}`). The chart renders them
  as Helm test hooks, and Flux runs them after every install and upgrade. A failing test:
  - on an app, fails the upgrade, so the previous version keeps running;
  - on a preview, keeps the failing version running, so it can be looked at;
  - in both cases marks the app red: a new sixth stage "the tests", and `--wait` ends `Failed`.
- **Built tests.** `shelf build-plan`, `shelf render --image` and the reusable workflow build,
  sign and scan test images like component images.
- **Report.** A new command `shelf app report <app> [--preview <branch>]` prints a verification
  report as Markdown, or `--json` (kind `Report`):
  - the commit, the address and the verdict;
  - the test results;
  - signature and scan;
  - for a preview, what differs from the app: components with another digest, and the change in
    CRITICAL and HIGH findings.

  Everything comes from the cluster and the registry, never from Git.
- **Admin UI.** The app page shows the tests, the isolation and its LAN exceptions; the connection
  form edits the exceptions through the same function as the CLI.
- **`just smoke-hardening`** joins level 2.

## Capabilities

### New Capabilities

- `isolation`: Pod Security levels, the NetworkPolicy of app and preview namespaces, LAN
  exceptions as an operator decision, and how isolation is reported.
- `app-tests`: running an app's tests after each rollout, how a failing test affects an app and a
  preview, and how test results are reported.

### Modified Capabilities

- `app-manifest`: the `tests` section, test names, `--image` and `shelf build-plan` for built tests.
- `app-chart`: test hooks rendered from `tests`.
- `app-delivery`: the diagnosis gets the stage "the tests".
- `agent-interface`: `AppStatus` carries tests and isolation; the new `Report` document.
- `branch-previews`: a preview's contents include the isolation, without LAN exceptions.
- `admin-ui`: the app page shows tests and isolation, and edits LAN exceptions.
- `dev-environment`: `smoke-hardening` in level 2.

## Impact

- **Chart and platform:**
  - `charts/shelf-app`: test hook pods;
  - `platform/apps` and `platform/previews`: namespace labels, NetworkPolicy, HelmRelease `test`,
    provider input `lan`.
- **Code:**
  - `internal/schema`: `tests`;
  - `internal/render`, `internal/cli` build-plan;
  - `internal/cluster`: the tests stage, isolation state, restricted findings, LAN input;
  - `internal/ops`: report, LAN option;
  - `internal/cli`: `--allow-lan`, `report`, JSON;
  - `internal/server`: app page, connection form.
- **Workflow:** `build.yml` needs no new step, since it builds what `shelf build-plan` lists and
  scans every digest in the values.
- **Existing apps:** keep running. An app that reaches another namespace or the LAN loses that
  path at the platform upgrade, until the operator adds an exception. A pod that violates
  `baseline` is refused at its next start. Both are named in the upgrade notes.
- **Levels:** level 2 (k3d enforces NetworkPolicies, checked in a spike); level 3 with
  `shelf-hello` and a test.
