# Proposal

## Why

Since Phase 10, every branch a coding agent pushes runs as a preview. A preview may run anything
the branch built, and so far shelf has no way of telling a running digest that CI built from a
known commit from one someone pushed by hand. Before an app goes beyond a preview, shelf has to
know that the running deploy artifact and its images are the ones the reusable workflow built,
signed by the expected identity, and scanned. Only then can the exposure decision rest on more
than trust in whoever can write to the registry.

## What Changes

- **Reusable workflow** (`build.yml`):
  - signs every built image and the deploy artifact with keyless cosign (GitHub OIDC, the public
    Sigstore);
  - scans every image the app runs, built and third-party, with Trivy;
  - attaches the full report to each built image as a signed attestation;
  - writes a summary per image digest into the deploy artifact as the annotation
    `dev.shelf.scan`, covered by the artifact's signature;
  - gets an input `platforms` (default `linux/arm64`, for example `linux/arm64,linux/amd64` for
    cloud targets).
- **Release** (`release.yml`): signs the `shelf-app` chart and the platform artifact the same
  way.
- **Cluster: Flux verifies.** The deploy artifact, the chart and the platform artifact are
  verified (OCIRepository `verify`), for apps and previews alike. An artifact without a valid
  signature is not applied.
- **Cluster: admission.** The platform installs the Sigstore policy-controller. A
  ClusterImagePolicy per app admits a built image of the app only with a valid signature from
  the expected identity, in the app's namespace and in its previews' namespaces. Third-party
  images are pinned by digest in the signed deploy artifact.
- **Signer.** `shelf init cluster` takes the signer as a cluster setting: keyless with the
  identity of shelf's workflows by default, or `--signer-key <file>` with a cosign public key for
  test clusters. The dev cluster uses a dev key pair, so level 2 verifies like production.
- **Exposure gate.** `--cloudflare` and `--quick` (CLI and admin UI, one function in
  `internal/ops`) refuse an app whose running deploy artifact is not verified or not scanned. A
  new app with an exposure flag is added privately, waited for, checked, and only then exposed.
- **Status.** `shelf app status` shows the chain: commit → deploy artifact → signature → images →
  scan → deployed. `--json` gets a `verification` object, and the admin UI's app page shows the
  same.
- **Findings are shown, never block.** A scan must exist for exposure; how many critical and high
  findings it has is shown and left to the operator.
- **CLAUDE.md** gets the guardrail "only a verified digest goes beyond a preview".

## Capabilities

### New Capabilities

- `verification`:
  - signing and scanning in the reusable workflow and the release;
  - the signer setting;
  - verification of artifacts by Flux;
  - the admission policy for built images;
  - what "verified" means for a running app;
  - how the chain is reported.

### Modified Capabilities

- `app-delivery`: the reusable workflow builds for the input `platforms`; the release signs chart
  and platform; `shelf app status` prints the verification chain.
- `app-exposure`: `--cloudflare` and `--quick` refuse an unverified app (new requirement), and a
  new app with an exposure flag is exposed only after it is verified.
- `cluster-init`:
  - the platform contains the policy-controller;
  - the cluster settings hold the signer;
  - the FluxInstance verifies the platform artifact.
- `branch-previews`: a preview's deploy chain verifies like an app's, and its built images are
  admitted by the app's policy.
- `admin-ui`: the app page shows the verification chain and why an app cannot be exposed.
- `agent-interface`: `AppStatus` carries a `verification` object.
- `dev-environment`:
  - cosign is pinned in the devcontainer;
  - the dev cluster verifies with a dev key pair, and every dev artifact is signed;
  - `smoke-verify` joins level 2.

## Impact

- **Workflows:** `.github/workflows/build.yml` (cosign, Trivy, `id-token: write`,
  `attestations`), `.github/workflows/release.yml`.
- **Platform and cluster code:** `platform/` (policy-controller, `verify` in `apps` and
  `previews`, ClusterImagePolicy per app), `internal/cluster` (signer settings, FluxInstance
  patch, verification state), `internal/ops` (exposure gate, order of add and expose).
- **Interfaces:** `internal/cli` (`--signer-key`, status, JSON), `internal/server` (app page).
- **Dev environment:** `.devcontainer/Dockerfile` (cosign), `hack/` (dev key, signing every
  pushed artifact, `smoke-verify`), CI selection.
- **Tenants:** need `id-token: write` in their `deploy.yml` (in `examples/tenant` and
  `shelf-hello`). Every app needs one push with the new workflow before its cluster is upgraded:
  an unsigned artifact is no longer applied.
- **Levels:** level 2 for the cluster and gate; level 3 with `shelf-hello` and real Cloudflare
  for keyless signing and the exposure gate.
