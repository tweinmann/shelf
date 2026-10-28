# Tasks

## 1. Names

- [x] 1.1 Reserve `--` in `ops.CheckAppName` with the message "`--` is reserved for previews"; verify with the table test in `internal/ops` and a CLI test for `shelf app add hello--copy` (spec app-delivery "Double hyphen")
- [x] 1.2 Add `schema.BranchSlug(branch)` and `schema.BranchTag(branch)` as in design decision 3; verify with table tests: `feature/Login`, runs of punctuation, leading and trailing separators, a slug passed in again, two long names with the same 20-character prefix, every result at most 20 characters and a DNS label
- [x] 1.3 Add `--branch` to `shelf build-plan`, printing `tag`; verify with a CLI test and the README CLI reference

## 2. Chart preview mode

- [x] 2.1 Render `shelf-secrets` in `charts/shelf-app` when `platform.preview` is set, one key per `generate: true` secret, from `lookup` or `randAlphaNum 26`; verify that `helm lint` passes and that the chart renders no `shelf-secrets` without `platform.preview` (existing golden files unchanged)
- [x] 2.2 Add a preview-mode case to the chart tests in `internal/chart`, replacing generated values with a placeholder before comparing; verify with `just golden` and that no generated value appears in `internal/chart/testdata`

## 3. Platform

- [x] 3.1 Add `platform/previews/resourceset.yaml` (and the kustomization entry) as in design decision 1, with the deploy OCIRepository at `interval: 30s`: Namespace with labels, `shelf-registry` copy, ResourceQuota, LimitRange, `shelf-deploy` ServiceAccount/Role/RoleBinding, deploy OCIRepository at `<< inputs.tag >>`, Kustomization, chart OCIRepository, HelmRelease `<app>--<slug>` with `platform.preview: true` and no cloudflared; verify with kubeconform in `just test` and the platform golden/manifest tests in `internal/cluster`
- [x] 3.2 Make `shelf init cluster` wait for the ResourceSet `previews` as for `apps`; verify with the cluster tests and `just smoke-init` (spec cluster-init "Platform applied")

## 4. Cluster and operations

- [x] 4.1 Add the preview provider to `internal/cluster`: create or update `<app>--previews` (type, url, filter, limit, insecure, secretRef, defaultValues, `reconcileEvery: 30s`, label `shelf.dev/previews`), and delete it; verify with server-side-apply fixture tests that the provider matches spec "Preview provider" and is not listed by `AppStates`
- [x] 4.2 Add `cluster.Previews(app)` (namespaces labelled `shelf.dev/app` and `shelf.dev/preview`, with phase, tag and commit) and generalize `AppDiagnosis`, `AppComponents` and `AppRollout` to a namespace; verify with tests on unstructured fixtures for an app with two previews
- [x] 4.3 Wire `--previews N` / `--no-previews` through `ops.AddApp` and `ops.Credentials`, rewrite the preview provider when the registry connection or `--insecure-registry` changes, and delete it in `ops.RemoveApp`, which also waits for every preview namespace; verify with CLI tests against the fake cluster (on, off, limit out of range, registry change, removal)

## 5. CLI and agent interface

- [x] 5.1 Show previews in `shelf app status <app>` (branch tag, phase, commit, limit reached) and add `--preview <branch>` for one preview; verify with CLI tests (spec app-delivery "App with previews", "One preview")
- [x] 5.2 Add `previews`, `previewLimit` and `previewLimitReached` to `AppStatus`, and a preview's own `AppStatus`; verify with new golden files in `internal/cli/testdata/json` and that apps without previews keep their existing golden files
- [x] 5.3 Add `--preview` to `--wait`, with a missing preview counting as Working; verify with ops and CLI tests (spec agent-interface "Agent waits for its branch", "Preview appears only after the push")
- [x] 5.4 Add `--preview` to `shelf app open`, with the preview's host and a refusal naming the existing previews; verify with CLI tests (spec app-exposure "Local access to a preview")
- [x] 5.5 Document `--previews`, `--preview` and the preview naming in the README (CLI reference, "Deploying an app") and in the tenant skill; verify with `TestTenantAgentInstructions`

## 6. Tenant workflow

- [x] 6.1 Publish branch artifacts in `.github/workflows/build.yml` (tags `sha-<short>` and `branch-<slug>`, never `main`; job summary with the preview commands); verify by reading the workflow against spec "Push to another branch" and with a dry run of its tag logic in a test script
- [x] 6.2 Delete a branch's package version on `delete` in `build.yml` (`/users/` or `/orgs/` API by owner type); verify the API calls against the GitHub REST documentation; the real deletion is level 3
- [x] 6.3 Change the trigger in `examples/tenant/.github/workflows/deploy.yml`, the header comment of `build.yml` and the README to all branches plus `delete`; verify with `just test`

- [x] 6.4 Add the registry build cache to `build.yml` (`--cache-from` always, `--cache-to ...,mode=max` only when publishing, `:buildcache` per component image); verify by reading the build arguments for a publishing run and a pull request, and that `shelf app tags` and the preview filter never list `buildcache`
- [x] 6.5 Update the tenant skill and `CLAUDE.md` (background wait, `gh run watch --exit-status` first, `--preview <branch>` on branches, slower first push); verify with `TestTenantAgentInstructions` (spec agent-interface "Waiting without idling", "Broken build found early", "Work on a branch")

## 7. Level 2 and CI

- [x] 7.1 Enable deletes in the dev registry in `hack/cluster-up.sh`, idempotently; verify that `just cluster-up` twice leaves `storage.delete.enabled: true` and that a tag can be deleted (spec dev-environment "Deleting a tag")
- [x] 7.2 Add `hack/smoke/previews.sh` and `just smoke-previews` as in design decision 8, including the seconds from push to Ready for a first push and a later commit; verify that it passes on the dev cluster and record RAM per preview and both times in design.md under "Results"
- [x] 7.3 Add `smoke-previews` to the full set in `.github/workflows/ci.yml` and to `.github/scripts/select-smokes.sh` (`platform/*`, `charts/*`, `hack/smoke/previews.sh`, `internal/cluster/*`); verify the selection on example paths and that the PR run shows it
- [x] 7.4 Add `just smoke-previews` to the CLAUDE.md dev-environment table and the README; run `just test` and `openspec validate --all --strict`; both pass

## Workflow follow-up

- Level-3 acceptance by the maintainer with `tweinmann/shelf-hello`: new `deploy.yml` trigger, `--previews` on `greeter`, a pushed branch gets a preview that `shelf app open --preview` shows, deleting the branch removes it (answers the open question about GHCR deletion); push-to-Ready times with CI recorded for a first push and a later commit, with and without the build cache.
- `/opsx:archive`; update CLAUDE.md Status, README Status and the context; stop for approval before Phase 11.
