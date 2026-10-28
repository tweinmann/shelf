# Design

## Context

What runs today, and what it trusts:

- **Workflow.** The reusable workflow `build.yml` builds the `build:` components, renders the
  app.yaml with `shelf render` and pushes the deploy artifact with `flux push artifact`. `render`
  pins every image by digest, built and third-party (for example `postgres:16` of
  `examples/hello`), so the artifact names exactly what runs. Nothing is signed, and nothing is
  scanned.
- **Cluster.** Per app and per preview, Flux has an OCIRepository `deploy` (the artifact) and
  one `shelf-app` (the chart). The platform itself comes through the FluxInstance's sync
  OCIRepository. All three trust whoever can write to the registry.
- **What shelf reads.** shelf learns about a running app only through the API server. The
  OCIRepository's `status.artifact.metadata` already carries the artifact's annotations; that
  is how `shelf app status` shows the commit without a forge API.
- **Tenants are private.** `tweinmann/shelf-hello` is a private repository on a free plan. As far
  as we know, GitHub's own artifact attestations are only available for private repositories on
  GitHub Enterprise Cloud, so they are out; cosign is what is left.
- **Hardware.** The target host is small (a Mac mini or a small Linux box), so every controller
  added to the platform counts in RAM.

## Goals / Non-Goals

**Goals:**

- **Signed.** Every deploy artifact, built image, chart and platform artifact the cluster takes
  from a release or a tenant build is signed by the expected identity, and the cluster refuses
  anything that is not.
- **Scanned.** Every image an app runs is scanned in CI. The result reaches shelf through the
  signed deploy artifact.
- **Gate.** Only a verified app can be exposed (`--cloudflare`, `--quick`), in the CLI and the
  UI alike.
- **Queryable.** The chain can be read per running version, for people and as JSON for agents.
- **Dev mirrors production.** The dev cluster verifies like production, with a key pair instead
  of keyless signing, so level 2 tests the real checks.

**Non-Goals:**

- **Blocking on findings.** A scan must exist; its findings are shown, never enforced.
- **Signing the CLI binaries** or their checksums.
- **SBOMs and SLSA provenance** beyond what cosign and Trivy produce.
- **Admission for third-party images** beyond the digest pinned in the signed artifact.
- **A switch that turns verification off.** A cluster always verifies, with keyless signing or
  a key.
- **Re-checking already running pods** after the cluster is upgraded. A pod that runs stays
  running.

## Decisions

### 1. Admission with the Sigstore policy-controller

The platform installs the Sigstore policy-controller (Helm chart, pinned, namespace
`cosign-system`):
- **Policy per app.** The ResourceSet `apps` generates a ClusterImagePolicy `shelf-<app>`
  covering the app's built images, `ghcr.io/<owner>/<package>/**`. Its identity is the cluster's
  signer.
- **Namespaces opt in.** App and preview namespaces carry `policy.sigstore.dev/include: "true"`,
  and only those are checked.
- **Unmatched images pass.** Images no policy matches, the third-party ones, are allowed
  (`no-match-policy: allow`).
- **Webhook failure.** If the webhook does not answer, pod creation in those namespaces fails
  (`failurePolicy: Fail`).

*Alternatives:*
- **Kyverno** is a general policy engine and could serve Phase 12 too, but a default install is
  several controllers with several hundred Mi of RAM.
- **No admission controller** is the leanest option. But then an unsigned image could start as
  long as someone writes a values ConfigMap by hand, and the acceptance "an unsigned artefact
  does not reach the cluster" would hold for artifacts only.

The maintainer chose the policy-controller. ClusterImagePolicy is a Sigstore CRD, not a
shelf-specific one; an ejected app can keep it or drop it.

### 2. Keyless cosign with the public Sigstore

CI signs with `cosign sign` and `cosign attest` using the job's GitHub OIDC token, so the
callers' `deploy.yml` needs `permissions: id-token: write`. There is no key to create, store or
rotate.

The cost is privacy. Every signature is recorded in the public Rekor log, and with it the name of
the private tenant repository, the workflow and the commit SHA, permanently.

*Alternatives:*
- **A cosign key pair per tenant:** nothing public, but keys to create, keep secret in the
  tenant repo and rotate.
- **Keyless with a key pair option per app:** more code and tests.

The maintainer chose keyless signing, accepting the entries in the public log.

### 3. The expected identity is shelf's workflow

With a reusable workflow, the Fulcio certificate's subject is the reusable workflow, not the
caller:
`https://github.com/tweinmann/shelf/.github/workflows/build.yml@refs/tags/v0`.

The caller's repository is only in a certificate extension. Neither Flux's `matchOIDCIdentity` nor
the policy-controller's identities can match extensions. So the expected identity is:

- issuer `https://token.actions.githubusercontent.com`;
- subject regex `^https://github\.com/tweinmann/shelf/\.github/workflows/(build|release)\.yml@refs/(tags|heads)/.+$`.

These are the defaults of the cluster settings `SHELF_SIGNER_ISSUER` and
`SHELF_SIGNER_SUBJECT`. A fork of shelf sets its own with `shelf init cluster --signer-subject`.

What this does and does not prove:

- **What it proves:** shelf's workflow built the artifact. It does not prove which tenant
  repository ran the workflow.
- **Repository:** the binding to the tenant comes from the registry. Only someone with write
  access to `ghcr.io/<owner>/<package>` can push an artifact or a signature there.
- **Source annotation:** shelf also shows the artifact's `org.opencontainers.image.source`. That
  annotation is part of the signed manifest.

*Alternatives:*
- **Signing in the tenant's own workflow** would put the caller into the subject. But it moves
  the signing steps into every tenant's boilerplate, against Phase 4b's one-line workflow.
- **Checking the extension with cosign on the host** would need the Sigstore Go libraries in
  shelf and a registry call from `app status`. A later phase can add it.

### 4. Signatures everywhere, the gate only for exposure

Branch builds are signed by the same workflow. The verification of the deploy chain and the
admission policy therefore apply to previews exactly as to apps: an unsigned artifact reaches no
namespace. The exposure gate (Decision 8) concerns only the app, since a preview is never
exposed.

*Alternative:* previews unchecked, which means fewer refusals while working. But the acceptance
would then hold for `main` only. The maintainer chose signatures everywhere.

### 5. Third-party images: pinned by the signed artifact

An image such as `postgres:16@sha256:…` is not ours to sign: the signature would have to be
pushed into Docker Hub's repository.

- **Integrity:** the digest is fixed in the deploy artifact, which is signed and verified. A
  values ConfigMap cannot change without a new signed artifact, so the digest that runs is the
  one CI resolved.
- **Admission:** the policy does not match third-party images (Decision 1). The scan covers them
  (Decision 6).

*Alternatives:*
- **Copy third-party images** into `ghcr.io/<owner>/<package>/` and sign the copies. That means
  more registry storage and a slower build, and it changes the image references in app.yaml.
- **Deny unmatched images.** That would break every app with a database.

### 6. Scan in CI, summary in the signed artifact

**What CI does:**
1. After `shelf render`, CI scans every image digest the rendered values name with Trivy (pinned,
   its DB cached between runs).
2. For each built image, it attaches the full report as a cosign attestation (`--type vuln`).
3. It writes a summary into the deploy artifact as the annotation `dev.shelf.scan`: per image
   digest the CRITICAL and HIGH counts, plus scanner version and scan time, as compact JSON. It
   uses `flux push artifact --annotations`.
4. Then it signs the artifact.

The signature covers the manifest, and with it the summary.

**How shelf reads it:** Flux copies manifest annotations into the OCIRepository's
`status.artifact.metadata`. shelf reads the summary from the cluster: no Sigstore library in
shelf, and no registry call from `app status`.

**What it blocks:** a scan must exist for exposure; how many findings it reports blocks nothing
(Decision 8).

*Alternatives:*
- **Read and verify the attestations from shelf:** needs the Sigstore libraries and registry
  access from the host.
- **Require the attestation at admission:** blocks previews and pods for a missing scan, against
  Decision 4.
- **Block on findings:** a new CVE could make an app impossible to expose overnight. The
  maintainer chose to show findings only.

### 7. What "verified" means for a running app

An app is **verified** when all of these hold:

- **Signature:** its `deploy` OCIRepository has `SourceVerified=True` for the revision it holds.
- **Release:** the release runs that revision's values (the Phase 9 configDigest comparison).
- **Chart:** its `shelf-app` OCIRepository has `SourceVerified=True`.
- **Scan:** `dev.shelf.scan` names every image digest in the running values.

A running pod's built images are admitted by the policy-controller, so they need no extra check.
The definition is computed in `internal/cluster` from what the API server reports, and reported
as a `Verification`:

- `Verified`;
- `Reason` (the first thing missing, in the order above);
- `Source` (the artifact's source annotation);
- `Signer` (the cluster's identity);
- `Scan` (per component: digest, critical, high, scanner, time).

### 8. The exposure gate, and add before expose

- **Where it runs:** `internal/ops` checks the gate whenever a call would give an app a named or
  quick tunnel it does not have yet: `shelf app add` or `shelf app credentials` with
  `--cloudflare` or `--quick`, and the UI's access form. The CLI and the UI share the code.
- **Refusal:** the gate refuses with the reason from Decision 7 and touches no Cloudflare API and
  no tunnel.
- **Order for a new app:** an app that does not run yet cannot be verified. `shelf app add` with
  an exposure flag first adds the app privately and waits for its rollout as today. Then it
  checks verification, and only then creates the tunnel.
- **If the check fails:** the app stays added and private, and the command exits 1 with the
  reason and a hint: push again with the current workflow, then run `shelf app credentials`.
- **Already exposed apps:** they keep their exposure. Their new artifacts are verified by Flux
  anyway, so an unsigned one never runs. A missing scan on a new artifact is shown in status and
  does not take the app off the internet.

*Alternative:* verify before adding, on the host. That needs cosign on the operator's machine;
rejected for the same reason as in Decision 6.

### 9. The signer is a cluster setting, keyless or a key

`shelf init cluster` writes the signer into the cluster settings.

**Keyless (the default):**
- `SHELF_SIGNER_ISSUER` and `SHELF_SIGNER_SUBJECT`, from `--signer-issuer` and
  `--signer-subject`, with the defaults of Decision 3.

**A key (test clusters):**
- `--signer-key <file>` with a cosign public key writes `shelf-system/signer-key`.
- shelf copies that Secret into every app and preview namespace as `shelf-signer`, as it does
  with the registry login.
- The platform's `verify` uses `secretRef: shelf-signer`, and the ClusterImagePolicy uses the
  key.

**Templating:** the ResourceSets choose the form at render time. The sync Kustomization
substitutes `${SHELF_SIGNER_MODE}` into the template's `<< if >>` before the operator renders it.

**No off switch:** `--signer-key` is for clusters that test shelf. A cluster without verification
would make the gate meaningless.

*Alternative:* an `--verify=off` mode for test clusters. But then level 2 would not test what
production runs.

### 10. The dev cluster signs with a dev key

- **Key:** `just cluster-up` creates a cosign key pair once, with an empty password, under
  `.local/cosign/`. That directory is machine-local and gitignored, and the key never leaves the
  devcontainer.
- **Install:** `just init-cluster` installs with `--signer-key .local/cosign/cosign.pub`.
- **Signing:** a helper in `hack/lib.sh` (`sign_artifact`) signs every artifact a recipe or smoke
  test pushes: platform, chart, deploy artifacts and built images. It signs with
  `--tlog-upload=false`, so nothing from the dev cluster reaches Rekor.
- **CI:** each CI job creates its own key pair.

### 11. Chart and platform signed by the release

- **Signing:** `release.yml` signs the chart and the platform artifact keyless. Its subject is
  `release.yml@refs/tags/v…`, which the default subject regex covers.
- **Platform:** the FluxInstance's sync OCIRepository gets `verify` through the existing
  kustomize patch, so `shelf init cluster` installs only a signed platform.
- **Chart:** the `shelf-app` OCIRepository of every app and preview gets `verify` too.

### 12. Platforms

`build.yml` gets an input `platforms` (default `linux/arm64`), passed to `docker buildx build
--platform`. More than one platform builds a multi-arch index, which is what gets signed and
pinned. Trivy scans the index's first platform. Building amd64 on the arm64 runner goes through
QEMU and is slow; the input's description says so.

### 13. Migration

An app's running artifact was built before this phase and is unsigned, so the upgraded platform
no longer applies a new artifact of it. The pods that run keep running. A pod that is recreated
with a built image is denied, because the old image has no signature.

The order is therefore:
1. Release.
2. Every tenant pushes once with the new workflow. On `@v0` that is automatic, apart from adding
   `id-token: write`.
3. The operator upgrades the cluster.

Two places say so:
- **`shelf init cluster`:** on an upgrade, its confirmation lists the apps and says that each
  needs one build with the new workflow.
- **`shelf app status`:** shows an app whose artifact is unverified with the reason and the hint.

### 14. Status, JSON and UI

- **`shelf app status <app>`:** a block between the components and the stages:
  - `source`: repository and commit;
  - `signed`: `verified, signer <identity>`, or the reason;
  - `scan`: per component `<critical> critical, <high> high`, with scanner and time;
  - `exposure`: `allowed`, or `refused: <reason>`.
- **`--json`:** `AppStatus.verification` holds `verified`, `reason`, `source`, `signer`,
  `exposable` and `scan[]` with `component`, `digest`, `critical`, `high`, `scanner` and
  `scannedAt`. Fields are only added (Phase 9 rule).
- **Previews:** they show `signed` and `scan` the same way.
- **Admin UI:** the app page shows the same block. The access form shows the gate's refusal as
  the job log's last line.
- **Diagnosis:**
  - A denied pod shows in the pods stage with the webhook's message and a hint about the
    signature.
  - A failed verification shows in the deploy artifact stage with Flux's message and a hint.

### 15. Turnaround

Signing adds a few seconds per image. Trivy's DB download is cached with `actions/cache`, so a
scan should take seconds per image. The agent wait (`--wait --commit`) is unchanged: it waits
for the rollout, which now includes verification by Flux. `smoke-verify` and the level-3 run
measure the CI and push-to-Ready times again, against Phase 10's 93 s and 11 s.

### 16. Tests

**Level 1:**
- `Verification` from fake cluster objects;
- the gate and the order of add then expose in `internal/ops` (fake cluster and Cloudflare);
- the status text and the JSON golden file;
- the UI page golden file;
- the ResourceSets rendered in both signer modes;
- the FluxInstance patch.

**Level 2, `just smoke-verify`:**
- **Signed:** with the dev key, a signed artifact with a signed built image becomes Ready and
  verified.
- **Unsigned artifact:** it is not applied, and the stage says why.
- **Unsigned image:** an artifact naming an unsigned built image is applied, but its pod is
  denied.
- **No scan:** an artifact without a scan summary runs, but is not exposable.
- **Previews:** a preview of a signed branch artifact is verified.
- **RAM:** prints the policy-controller's RAM.

The refusal itself is tested at level 1, because only `smoke-quick` may pass `--quick`.

**Level 3:**
- keyless signing in `shelf-hello`;
- the migration of `gretter`;
- the gate against the real Cloudflare connection.

### 17. Signatures in cosign's classic format (from the spike)

cosign v3 stores a signature by default as a Sigstore bundle attached as an OCI referrer. Flux
reads it, but the policy-controller (chart 0.10.8, controller 0.13.1) does not: it reports "no
signatures found". Every signature and attestation is therefore made with
`--new-bundle-format=false`, which writes the classic `sha256-<digest>.sig` tag that both read.
Signing without a transparency log (the dev key) also needs `--use-signing-config=false
--tlog-upload=false`. A ClusterImagePolicy with a key and no `ctlog` does not look up a log.

*Alternative:* wait for a policy-controller release that reads bundles. Rejected for now, but
switching back is one flag once it does.

### 18. The dev registry is `shelf-registry.local:5000` (from the spike)

The policy-controller has no setting for a registry without TLS, but go-containerregistry
speaks HTTP to a registry whose name ends in `.local`. With the dev registry named
`shelf-registry.local`, it verifies signatures there as on GHCR.
- **What changes:** `just cluster-up` creates the registry under that name, so k3d writes it into
  CoreDNS and the containerd mirror configuration, and `/etc/hosts` in the devcontainer gets the
  new name.
- **Moving over:** one `just cluster-reset`.

*Alternatives:*
- **TLS with a CA of our own,** trusted by containerd, Flux, the policy-controller
  (`registryCaBundle`) and the devcontainer. Closest to production, but the most work.
- **Admission tested at level 3 only.**

The maintainer chose the rename.

## Risks / Trade-offs

- **Public log** → the names of private tenant repos and their commit SHAs become public in
  Rekor. Accepted by the maintainer (Decision 2). A key pair option can be added later without
  changing the cluster side (Decision 9 already supports keys).
- **Shared identity** → any repository calling shelf's workflow produces a valid signature.
  Mitigated by registry write access and by showing the source annotation (Decision 3). It is
  stated in the README, so nobody reads more into the signature than it proves.
- **Sigstore down** → no new artifact or pod with a built image can be verified, so new deploys
  wait while running pods keep running. A recreated pod with a built image fails while the
  webhook cannot verify. Accepted for now; Phase 15 (operations) covers outages.
- **Webhook down** → with `failurePolicy: Fail`, no pod in an app or preview namespace can be
  created. The policy-controller runs two replicas if RAM allows; otherwise one, measured in
  `smoke-verify`.
- **Plain-HTTP dev registry** → the spike showed the policy-controller only reaches it under a
  `.local` name (Decision 18).
- **Signature format** → the policy-controller reads only cosign's classic format (Decision 17).
  A later cosign that drops `--new-bundle-format=false` before the policy-controller reads
  bundles would block an upgrade of cosign; it is pinned.
- **Migration** → an operator who upgrades the cluster before the tenants pushed sees new
  deploys stop. The confirmation and the status say so (Decision 13). Nothing is deleted.
- **Turnaround** → the scan and signing add to every push, also of branches. Measured and
  recorded (Decision 15). If the scan dominates, it moves to a job of its own that runs parallel
  to the build of other components.
- **More of a "security" claim than exists** → the README says exactly what is verified and what
  is not (third-party images only by digest, the shared identity, findings not enforced).

## Migration Plan

1. **Release** v0.8.0: workflow, chart and platform, signed.
2. **Tenants:** add `id-token: write` to `deploy.yml`. `examples/tenant` shows it, and
   `shelf-hello` is updated in the acceptance. Push once.
3. **Clusters:** `shelf init cluster` with the new platform. The confirmation lists apps without
   a verified artifact.
4. **Rollback:** the previous platform release removes verification and the policy-controller.
   The signed artifacts keep working there.

## Open Questions

- Whether two replicas of the policy-controller fit; one uses 34 Mi (spike), measured again in
  `smoke-verify`.

## Results

Spike on the dev cluster (2026-10-28):
- **Versions:** cosign v3.1.3, policy-controller chart 0.10.8 (controller 0.13.1), Flux 2.9.5.

| Check | Result |
|---|---|
| Flux `verify` with a key, no tlog, plain HTTP | verifies a signed artifact (`SourceVerified=True`) and refuses an unsigned one, in both signature formats |
| policy-controller and `shelf-registry:5000` | fails: it only tries HTTPS |
| policy-controller and `shelf-registry.local:5000` | reaches the registry over HTTP |
| policy-controller and a cosign v3 bundle | "no signatures found" |
| policy-controller, classic `.sig`, key, no tlog | admits the signed image, denies the unsigned one |
| policy-controller RAM, one replica, idle | 34 Mi |

Building images: the devcontainer's Docker has no buildx and does not push to a registry
without TLS. Smoke tests push images with `crane` (go-containerregistry), which is pinned with
the other tools.

`just smoke-verify` on the dev cluster (2026-10-28), with the dev registry renamed to
`shelf-registry.local:5000`, passed:
- a signed and scanned artifact naming a signed built image is verified and exposable;
- the policy admits that image and `postgres:16`, and denies the unsigned image, in the app's
  namespace and in a preview's;
- a preview of a signed branch artifact is verified, and never exposable;
- a signed artifact without `dev.shelf.scan` rolls out but is not verified;
- an unsigned artifact is not applied: the wait ends Failed at the deploy artifact with the
  `id-token: write` hint, and the app keeps running the last verified commit;
- `shelf app rm` removes `shelf-<app>`.

The policy-controller used 34 Mi and 40 Mi for its two replicas.

`smoke-apps`, `smoke-agent` (`SMOKE_FULL=0`) and `smoke-previews` passed on the same cluster,
every artifact signed with the dev key. Their times are within a few seconds of Phase 10's, so
verification adds no noticeable wait in the cluster:

| Measurement | Phase 10 | Phase 11 |
|---|---|---|
| First push of a branch to Ready | 23 s | 29 s |
| Later commit on that branch | 17 s | 18 s |
| Deleted tag to preview gone | 26 s | 31 s |

Two fixes came out of these runs:
- **The wait for an evicted preview.** One run of `smoke-previews` saw the evicted preview
  outlast the 120 s wait for it; the wait is now 180 s.
- **Leftover branch tags.** The script now deletes the branch tags a failed run left in the
  registry before it starts.

Deviations found while implementing:
- **No pod to show.** A pod the admission webhook (or a preview's quota) refuses never exists, so
  the pods stage reads the namespace's `FailedCreate` events, one list call for all namespaces.
  A refusal counts only while no pod is newer than it.
- **Waits fail fast.** `shelf app add` and `shelf init cluster` fail at once when Flux reports
  `SourceVerified=False`, instead of waiting for the timeout: Flux retries forever, but a
  signature does not appear by waiting.
- **Public domain checked first.** The check that the app's domain is public moved before the
  private deploy, so a name that cannot exist is still refused before anything is written.
- **Tools in the dev scripts.**
  - `flux tag artifact` has no option for a registry without TLS, so the dev scripts tag with
    `crane tag --insecure`.
  - `shelf render` looks built images up over TLS, so `smoke-verify` renders the Docker Hub
    image its dev images were copied from and swaps the reference.
- **Dev scan summary.** Dev artifacts carry a scan summary whose scanner is `none (dev
  registry)`: nothing scans in level 2, and the summary only lets a dev app count as verified.
- **`smoke-tenant`.** It installs a released platform with keyless verification, because the
  tenant's artifacts are signed keyless and a cluster accepts one signer.
