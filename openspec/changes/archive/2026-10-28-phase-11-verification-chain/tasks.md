# Tasks

## 1. Spike and toolchain

- [x] 1.1 Spike on the dev cluster, by hand and not committed:
  - install the policy-controller chart;
  - sign an image and a `flux push artifact` in `shelf-registry:5000` with a key and
    `--tlog-upload=false`;
  - check that Flux's OCIRepository `verify` (key and keyless forms) and a ClusterImagePolicy
    with that key accept them, and refuse an unsigned image;
  - measure the policy-controller's RAM.

  Record the results and any deviation in design.md before task group 4 (design risks
  "Plain-HTTP dev registry" and "Key verification and the transparency log").
- [x] 1.2 Pin cosign (v3.1.3) and crane (go-containerregistry v0.20.6) in
  `.devcontainer/Dockerfile` (an `ARG` each, like the other tools), and check `cosign version`
  and `crane version` in the container build.
- [x] 1.3 Rename the dev registry to `shelf-registry.local:5000` (design decision 18):
  - `hack/lib.sh`, `hack/cluster-up.sh` (k3d `--registry-create`, `/etc/hosts`), every hack
    script and justfile recipe that names it;
  - the CLAUDE.md dev table and the README;
  - the dev-environment delta spec.

  Then `just cluster-reset`, `just init-cluster` and `just smoke-init` (level 2). Afterwards, re-add
  `gretter` with the maintainer: the reset removes it.

## 2. Workflows

- [x] 2.1 `build.yml`:
  - input `platforms` (default `linux/arm64`), passed to `docker buildx build --platform`;
  - `permissions: id-token: write` documented in the header;
  - after the push of each built image, `cosign sign` its digest (spec verification "Signing in
    the tenant workflow").
- [x] 2.2 `build.yml` scan step (spec verification "Scan in the tenant workflow"):
  - after `shelf render`, scan every image digest of the rendered values with a pinned Trivy, its
    DB cached with `actions/cache`;
  - `cosign attest --type vuln` the report on each built image;
  - build the `dev.shelf.scan` JSON (per digest `critical`, `high`; `scanner`, `scannedAt`) and
    pass it to `flux push artifact --annotations`.
- [x] 2.3 `build.yml`: `cosign sign` the deploy artifact digest after the push and before
  `flux tag artifact`, so the channel tag only moves to a signed artifact. A failed signing fails
  the run.
- [x] 2.4 `release.yml`: keyless `cosign sign` of the chart and the platform artifact by digest,
  before the major tag moves (spec app-delivery "Release publication").
- [x] 2.5 `examples/tenant/.github/workflows/deploy.yml`: `permissions: id-token: write`
  (with `contents: read`, `packages: write`); README tenant section. Validate the YAML of all
  three workflows.

## 3. Signer setting and init

- [x] 3.1 `internal/cluster`:
  - signer settings `SHELF_SIGNER_MODE`, `SHELF_SIGNER_ISSUER` and `SHELF_SIGNER_SUBJECT` in
    `shelf-config`, with the defaults of design decision 3;
  - `shelf-system/signer-key` for the key form;
  - copy it as `shelf-signer` into app and preview namespaces wherever the registry login is
    copied.

  Table-driven tests for both forms (spec verification "Signer of a cluster",
  cluster-init "System namespace and cluster settings").
- [x] 3.2 `internal/cli` `init cluster`:
  - flags `--signer-issuer`, `--signer-subject` and `--signer-key <file>`, with the PEM checked
    before anything is applied;
  - golden output.
- [x] 3.3 FluxInstance: add `verify` to the sync OCIRepository through the existing kustomize
  patch, in both signer forms (spec cluster-init "FluxInstance syncing the platform"). Unit test
  of the rendered patch.
- [x] 3.4 Confirmation on upgrade: list the apps whose `deploy` OCIRepository has no
  `SourceVerified=True`, with the sentence from the spec scenario "Apps without a verified
  artifact". CLI test with the fake cluster.

## 4. Platform

- [x] 4.1 `platform/`: the policy-controller as a pinned HelmRepository plus HelmRelease in
  `cosign-system` with `no-match-policy: allow`, its resources set and the replica count from
  the spike; add it to the platform kustomization. kubeconform passes.
- [x] 4.2 ResourceSet `apps`:
  - `verify` on `deploy` and `shelf-app`, keyless or with `secretRef: shelf-signer`, chosen
    through `${SHELF_SIGNER_MODE}`;
  - the namespace label `policy.sigstore.dev/include: "true"`;
  - a ClusterImagePolicy `shelf-<app>` for `ghcr.io/<owner>/<package>/**` with the cluster's
    signer.

  The package path comes from the provider input `url`. Extend the ResourceSet test in
  `internal/cluster` to both signer forms.
- [x] 4.3 ResourceSet `previews`: the same `verify` and namespace label (spec branch-previews
  "Preview contents"). Extend `TestPreviewsResourceSet`.
- [x] 4.4 `shelf app rm`: wait until the ClusterImagePolicy `shelf-<app>` is gone too (spec
  verification "Admission of built images", scenario "Removed with the app").

## 5. Verification state

- [x] 5.1 `internal/cluster`:
  - a `Verification` per app and preview from the OCIRepositories' `SourceVerified` conditions,
    the Phase 9 configDigest comparison, the values' image digests and the `dev.shelf.scan`
    metadata, with the reason order of design decision 7;
  - table-driven tests: verified, unsigned, foreign signer, rollout pending, no scan, scan
    missing a digest, chart unverified.
- [x] 5.2 Diagnosis:
  - a failed verification in the deploy artifact stage gets a hint to push again with the
    current workflow;
  - a pod denied by the admission webhook (ReplicaSet `FailedCreate` with the webhook's message)
    shows in the pods stage with a hint about the signature.

  Tests with fake events.
- [x] 5.3 `shelf app status`:
  - the verification block (source, signed, scan per component, exposure) for an app and for
    `--preview`;
  - golden text output.
- [x] 5.4 `--json`: `AppStatus.verification` as in spec agent-interface "Verification in the
  agent interface"; golden files `appstatus.json` and `appstatus-preview.json` extended, and a
  new one for an unverified app.

## 6. Exposure gate

- [x] 6.1 `internal/ops`: the gate before every change that gives an app a tunnel it does not
  have yet (`--cloudflare`, `--quick`), refusing with the reason and hint before any Cloudflare
  call. Tests with the fake cluster and a Cloudflare fake that fails if it is called (spec
  app-exposure "Only a verified app is exposed").
- [x] 6.2 `shelf app add` with an exposure flag: add privately, wait for the rollout, check,
  then expose; on refusal the app stays private and the command exits 1. ops and CLI tests for
  both outcomes; the progress golden file gets the new lines.
- [x] 6.3 Admin UI:
  - the app page shows the verification block, with a golden file per state;
  - the access form's job fails with the gate's message, through the same ops call (spec admin-ui
    "App page").

## 7. Dev environment and level 2

- [x] 7.1 `hack/cluster-up.sh`: create the dev key pair under `.local/cosign/` once, with an empty
  password. `just init-cluster` passes `--signer-key .local/cosign/cosign.pub`.
- [x] 7.2 `hack/lib.sh` `sign_artifact <ref>` (dev key, classic format, no tlog, plain-HTTP
  registry, design decision 17), called by `platform-push.sh`, `chart-push.sh` and every `flux push artifact` in
  `hack/smoke/*.sh`. The smoke tests init with `--signer-key`. Run `just smoke-apps` and
  `just smoke-previews` to show that they still pass (level 2).
- [x] 7.3 `hack/smoke/verify.sh` and `just smoke-verify`, as in design decision 16:
  - signed artifact with a signed built image is verified;
  - unsigned artifact not applied;
  - unsigned built image denied;
  - no scan summary means not exposable;
  - a signed preview is verified;
  - prints the policy-controller's RAM.

  Run it on the dev cluster (level 2) and record the RAM in design.md under "Results".
- [x] 7.4 CI: add `smoke-verify` to the full set in `ci.yml` and to `select-smokes.sh`
  (`platform/*`, `internal/cluster/*`, `hack/lib.sh`, `hack/smoke/verify.sh`); every smoke job
  creates its own key pair. Verify the selection on example paths and in the PR run.

## 8. Documentation and checks

- [x] 8.1 CLAUDE.md:
  - the guardrail "Only a verified digest goes beyond a preview: `--cloudflare` and `--quick`
    refuse an app whose running deploy artifact is not signed by the cluster's signer and
    scanned";
  - the safety rule that the dev key under `.local/cosign/` signs dev artifacts only and never
    leaves the devcontainer;
  - conventions for `dev.shelf.scan`, `shelf-signer`, `shelf-<app>` ClusterImagePolicy and the
    signer settings;
  - `smoke-verify` in the dev table;
  - the cluster-up row mentions the dev key.
- [x] 8.2 README:
  - a section on the verification chain that states exactly what is verified and what is not
    (the shared workflow identity, third-party images by digest only, findings shown not
    enforced, the public Rekor entries);
  - the tenant permissions;
  - the migration order;
  - CLI table rows.
- [x] 8.3 `openspec/config.yaml` context: the verification chain in the architecture; the
  exposure line names the gate.
- [x] 8.4 Run `just test` and `openspec validate --all --strict`; both pass.

## 9. Acceptance (level 3, after release v0.8.0)

- [x] 9.1 With `tweinmann/shelf-hello`:
  - add `id-token: write`, push, and check the keyless signatures and the attestation with
    `cosign verify`;
  - upgrade the dev cluster with the release platform: the confirmation lists `gretter`, and
    `gretter` keeps running unverified until the next push, then is verified;
  - push an unsigned artifact by hand under a branch tag: no preview applies it.
- [x] 9.2 Exposure gate against the real Cloudflare connection:
  - `shelf app credentials gretter --private`, then `--cloudflare tobile` succeeds while
    `gretter` is verified;
  - the same for an app whose artifact has no scan is refused.

  Record the CI time and push-to-Ready of a branch against Phase 10 (design decision 15) in
  design.md under "Results".

## 10. Follow-ups from the acceptance

- [x] 10.1 `shelf serve` reads the kubeconfig again before every operation (`ops.Ops.Reload`), so
  a cluster reset needs no restart of the server; test in `internal/ops`.
- [x] 10.2 The add form offers "Registry without TLS" unticked on a cluster with a dev registry;
  test in `internal/server`.
