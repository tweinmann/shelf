# Spec Delta

## MODIFIED Requirements

### Requirement: App naming
The system SHALL name an app at `shelf app add <app>`, independent of app.yaml's `name`, and MUST
reject a name that is not a DNS label of at most 40 characters, that contains `--` (which separates
an app from its previews), or that is reserved for the platform (`default`, `flux-system`,
`traefik`, `cloudflared`, or a name starting with `kube-` or `shelf-`).

#### Scenario: Reserved name
- **WHEN** a user runs `shelf app add shelf-web oci://ghcr.io/acme/web:main`
- **THEN** the command fails saying the name is reserved for the platform, before contacting the cluster

#### Scenario: Invalid name
- **WHEN** the name contains upper-case letters or is longer than 40 characters
- **THEN** the command fails saying it must be a DNS label of at most 40 characters

#### Scenario: Double hyphen
- **WHEN** a user runs `shelf app add hello--copy oci://ghcr.io/acme/hello:main`
- **THEN** the command fails saying `--` is reserved for previews, before contacting the cluster

### Requirement: Build plan for tenant CI
`shelf build-plan <app.yaml>` SHALL print JSON `{package, builds}`, where `package` is app.yaml's
`name` and `builds` lists each component with a `build` directory and its context relative to
the working directory; with `--branch <name>` it SHALL also print `tag`, the branch tag of that
branch.

#### Scenario: Component with a build directory
- **WHEN** app.yaml declares a component with `build: ./web` and that directory exists
- **THEN** `builds` contains that component with the context path of `./web` relative to the working directory

#### Scenario: Missing build directory
- **WHEN** a component's build directory does not exist
- **THEN** the command fails naming the component and the directory

#### Scenario: Branch tag
- **WHEN** the workflow runs `shelf build-plan app.yaml --branch feature/Login`
- **THEN** the JSON holds `"tag": "branch-feature-login"`

### Requirement: Reusable tenant workflow
The reusable workflow `.github/workflows/build.yml` SHALL validate the app.yaml, build every
`build:` component for `linux/arm64` as `ghcr.io/<owner>/<package>/<component>`, render the
app.yaml, and push the deploy artifact `ghcr.io/<owner>/<package>` tagged `sha-<shortsha>` and
`main` for the default branch or `branch-<slug>` for another branch, annotated with
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

### Requirement: App removal
`shelf app rm <app>` SHALL ask for confirmation unless `--yes` is given, delete the app's
provider and its preview provider, wait until its namespace and every preview namespace with all
objects and volumes are gone, and delete `app-<app>` and `tunnel-<app>` from `shelf-system`,
while keeping the host secret backup and saying where it is.

#### Scenario: Remove an app
- **WHEN** a user confirms `shelf app rm hello`
- **THEN** the namespace `hello`, its volumes and the app's Secrets in `shelf-system` are deleted
- **AND** the output names the kept backup file, so a later `shelf app add hello` restores the old secret values

#### Scenario: Remove an app with previews
- **WHEN** a user confirms `shelf app rm hello` and `hello` has the previews `hello--feature-login` and `hello--fix-typo`
- **THEN** both preview namespaces and their volumes are deleted along with the app

#### Scenario: Namespace not created by the platform
- **WHEN** a namespace named after the app exists without the label `shelf.dev/app=<app>`
- **THEN** removal fails saying the namespace does not belong to the app

#### Scenario: Unknown app
- **WHEN** neither provider, namespace nor secrets exist for the name
- **THEN** the command fails saying the app does not exist

### Requirement: App status command
`shelf app status` SHALL list every app with its state and, when not Ready, the failing stage and
reason in one line; `shelf app status <app>` SHALL print the app's address, artifact, registry
connection, running revision, the git commit that revision was built from, deploy time,
components, every stage with its message and, if not Ready, its hint, and the app's previews with
their branch tag, state and commit; `--preview <branch>` SHALL print the same for one preview.

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

## ADDED Requirements

### Requirement: Build cache in the tenant workflow
The reusable workflow SHALL reuse image layers between runs through a build cache in the
registry, `ghcr.io/<owner>/<package>/<component>:buildcache`, which only runs that publish write.

#### Scenario: Build cache
- **WHEN** a push changes only the source of one component
- **THEN** the build of every component reads its layers from `ghcr.io/<owner>/<package>/<component>:buildcache`, a publishing run writes that cache back, and a pull request only reads it
