# Spec Delta

## Purpose

Lets shelf and its operator know that what runs is what the reusable workflow built from a
known commit: signed by the expected identity, scanned, and verified by the cluster before it
runs, so that only a verified app goes beyond a preview.

## ADDED Requirements

### Requirement: Signing in the tenant workflow
When it publishes, the reusable workflow SHALL sign every built image and the deploy artifact by
digest with keyless cosign, using the job's GitHub OIDC token, so the caller's workflow MUST grant
`id-token: write`; a run that cannot sign MUST fail without moving the `main` or `branch-<slug>`
tag.

#### Scenario: Published build
- **WHEN** the workflow publishes for a push to `main`
- **THEN** every built image and the deploy artifact have a cosign signature whose certificate names shelf's workflow as subject and `https://token.actions.githubusercontent.com` as issuer

#### Scenario: Missing permission
- **WHEN** the caller's `deploy.yml` does not grant `id-token: write`
- **THEN** the run fails at signing, and the channel tag still points at the previous artifact

#### Scenario: Pull request
- **WHEN** the workflow runs for a pull request
- **THEN** it signs nothing, as it publishes nothing

### Requirement: Scan in the tenant workflow
When it publishes, the reusable workflow SHALL scan every image digest the rendered values name,
built and third-party, with a pinned Trivy, attach the full report to each built image as a
signed cosign attestation of type `vuln`, and write the annotation `dev.shelf.scan` to the deploy
artifact before it is signed: per image digest the CRITICAL and HIGH counts, the scanner version
and the scan time.

#### Scenario: App with a third-party database
- **WHEN** an app has a built `web` and a `db` with `image: postgres:16`
- **THEN** `dev.shelf.scan` lists both digests with their counts, and only the `web` image has a `vuln` attestation

#### Scenario: Findings do not fail the build
- **WHEN** an image has CRITICAL findings
- **THEN** the workflow still publishes, and the counts are in `dev.shelf.scan`

### Requirement: Signer of a cluster
The cluster settings SHALL name the signer every verification expects: keyless by default with
`SHELF_SIGNER_ISSUER` and the subject regex `SHELF_SIGNER_SUBJECT` covering shelf's `build.yml`
and `release.yml` (`--signer-issuer`, `--signer-subject`), or a cosign public key given with
`--signer-key <file>` and kept in `shelf-system/signer-key`; there MUST be no setting that turns
verification off.

#### Scenario: Default signer
- **WHEN** `shelf init cluster` runs without signer flags
- **THEN** `shelf-config` holds the GitHub Actions issuer and a subject regex matching `https://github.com/tweinmann/shelf/.github/workflows/build.yml@refs/tags/v0`

#### Scenario: Key for a test cluster
- **WHEN** `shelf init cluster --signer-key cosign.pub` runs
- **THEN** `shelf-system/signer-key` holds the key, and every app and preview namespace gets it as `shelf-signer`

#### Scenario: Unreadable key
- **WHEN** the file given to `--signer-key` is not a PEM public key
- **THEN** the command fails before it changes the cluster

### Requirement: Artifacts verified by Flux
Every OCIRepository the platform creates for an app or a preview, the deploy artifact and the
`shelf-app` chart, SHALL verify the cosign signature against the cluster's signer, so that an
artifact without a valid signature is not applied and what already runs keeps running.

#### Scenario: Unsigned deploy artifact
- **WHEN** an artifact without a signature is pushed under an app's tag
- **THEN** the app's `deploy` OCIRepository reports a failed verification, the release keeps the previous values, and the pods keep running

#### Scenario: Signed by someone else
- **WHEN** an artifact is signed by an identity the subject regex does not match
- **THEN** it is not applied, as if unsigned

### Requirement: Admission of built images
The platform SHALL run the Sigstore policy-controller and give every app a ClusterImagePolicy
`shelf-<app>` that admits an image under `ghcr.io/<owner>/<package>/` only with a valid signature
from the cluster's signer, in the namespaces of the app and its previews; images no policy
matches SHALL be admitted, as their digests are fixed in the signed deploy artifact.

#### Scenario: Unsigned built image
- **WHEN** the values of an app name a built image of its package that has no signature
- **THEN** the pod is denied by the admission webhook, and the pods stage shows the denial with a hint about the signature

#### Scenario: Third-party image
- **WHEN** an app runs `postgres:16@sha256:…` from Docker Hub
- **THEN** the pod is admitted

#### Scenario: Removed with the app
- **WHEN** an app is removed with `shelf app rm`
- **THEN** its ClusterImagePolicy is gone

### Requirement: Verified app
An app or preview SHALL count as verified when its deploy artifact and chart sources report a
verified signature for the revisions they hold, its release runs the values of that deploy
artifact revision, and the artifact's `dev.shelf.scan` names every image digest in those values;
otherwise the reason SHALL be the first of these that does not hold.

#### Scenario: Verified
- **WHEN** a signed and scanned artifact has rolled out
- **THEN** the app is verified, and its scan summary per component is known

#### Scenario: No scan summary
- **WHEN** a signed artifact without `dev.shelf.scan` runs
- **THEN** the app is not verified, with the reason that the artifact has no scan

#### Scenario: Rollout still pending
- **WHEN** a new signed artifact is fetched but its values are not released yet
- **THEN** the app is not verified until the release runs them

### Requirement: Verification in the status
`shelf app status <app>` and `--preview` SHALL print the artifact's source and commit, whether the
signature is verified and by which signer or why not, the CRITICAL and HIGH counts per component
with scanner and scan time, and whether the app may be exposed or why not.

#### Scenario: Verified app
- **WHEN** a user runs `shelf app status hello` for a verified app
- **THEN** the output shows the source and commit, the signer, `verified  yes`, a scan line per component, and `exposure  allowed`

#### Scenario: Unverified app
- **WHEN** the running artifact of `hello` is unsigned
- **THEN** the output shows `verified  no` with the reason and a hint to push again with the current workflow, and `exposure  refused`
