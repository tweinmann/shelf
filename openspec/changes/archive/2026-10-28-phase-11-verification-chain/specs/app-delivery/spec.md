# Spec Delta

## MODIFIED Requirements

### Requirement: Reusable tenant workflow
The reusable workflow `.github/workflows/build.yml` SHALL validate the app.yaml, build every
`build:` component for the platforms of its input `platforms` (default `linux/arm64`) as
`ghcr.io/<owner>/<package>/<component>`, render the app.yaml, and push the deploy artifact
`ghcr.io/<owner>/<package>` tagged `sha-<shortsha>` and `main` or `branch-<slug>`, annotated with
`dev.shelf.package=<package>` and the commit's source and revision. When a branch is deleted, it
SHALL delete that branch's artifact.

#### Scenario: Push to the default branch
- **WHEN** the workflow runs for a push to the repository's default branch
- **THEN** it pushes the images and the deploy artifact, and outputs `oci://ghcr.io/<owner>/<package>:main`
- **AND** its job summary shows a `shelf app add` command for that artifact

#### Scenario: Push to another branch
- **WHEN** the workflow runs for a push to the branch `feature/login`
- **THEN** it pushes the images and the deploy artifact tagged `sha-<shortsha>` and `branch-feature-login`, and never `main`

#### Scenario: Pull request or other branch
- **WHEN** the workflow runs for a pull request, from a fork or not
- **THEN** it builds the images but pushes neither images nor a deploy artifact
- **AND** a push to another branch is no longer such a case: it publishes, as in "Push to another branch"

#### Scenario: Branch deleted
- **WHEN** the workflow runs for the deletion of the branch `feature/login`
- **THEN** it deletes the package version that carries `branch-feature-login` from `ghcr.io/<owner>/<package>`, and nothing else

#### Scenario: Pinned shelf release
- **WHEN** the caller sets the input `shelf-version`
- **THEN** the workflow installs that release's linux/arm64 binary after checking its checksum, and otherwise the latest release

#### Scenario: Two platforms
- **WHEN** the caller sets `platforms: linux/arm64,linux/amd64`
- **THEN** every built image is a multi-arch index of both, and the rendered values pin the index digest

### Requirement: Release publication
Pushing a `v*` tag MUST publish a shelf release: CLI binaries for linux and darwin (amd64 and
arm64) with checksums, the `shelf-app` chart and the platform artifact, both signed with keyless
cosign by `release.yml`, all at the release version, and then move the major tag (for example
`v0`) before marking the release as latest.

#### Scenario: Tenant on the major tag
- **WHEN** a tenant uses `tweinmann/shelf/.github/workflows/build.yml@v0` and a new v0 release is published
- **THEN** the tenant's next build uses the new workflow together with the matching latest binary

#### Scenario: Signed chart and platform
- **WHEN** release `v0.8.0` is published
- **THEN** `ghcr.io/tweinmann/shelf/charts/shelf-app:0.8.0` and the platform artifact `v0.8.0` carry a signature whose subject is `release.yml` of `tweinmann/shelf` at that tag

### Requirement: App status command
`shelf app status` SHALL list every app with its state and, when not Ready, the failing stage and
reason in one line; `shelf app status <app>` SHALL print the app's address, artifact, registry
connection, running revision and commit, deploy time, components, its verification, every stage
with its message and, if not Ready, its hint, and its previews with tag, state and commit;
`--preview <branch>` SHALL print the same for one preview.

#### Scenario: No apps
- **WHEN** the cluster has no registered apps
- **THEN** `shelf app status` prints "No apps yet."

#### Scenario: One app in detail
- **WHEN** a user runs `shelf app status hello` for a healthy app
- **THEN** the output shows `hello: Ready`, the artifact reference, the revision, and all five stages as Ready

#### Scenario: Commit of the running artifact
- **WHEN** the app's deploy artifact was pushed by the reusable workflow for commit `42a964eb0b4b…`
- **THEN** `shelf app status hello` shows that commit, read from the artifact's revision annotation as the cluster reports it, without calling a forge API

#### Scenario: Artifact without a commit
- **WHEN** the deploy artifact carries no git revision
- **THEN** the commit line is left out

#### Scenario: App with previews
- **WHEN** `hello` has previews for `feature/login` and `fix-typo`
- **THEN** `shelf app status hello` lists both with their tags, states and commits, and the list of all apps shows them under `hello`, not as apps of their own

#### Scenario: One preview
- **WHEN** a user runs `shelf app status hello --preview feature/login`
- **THEN** the output shows the preview's namespace `hello--feature-login`, its host, artifact tag, commit, components and five stages
