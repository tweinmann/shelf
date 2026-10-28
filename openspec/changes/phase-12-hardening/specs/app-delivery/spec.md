# Spec Delta

## RENAMED Requirements

- FROM: `### Requirement: Five-stage diagnosis`
- TO: `### Requirement: Stage diagnosis`

## MODIFIED Requirements

### Requirement: Stage diagnosis
The system SHALL diagnose an app as six ordered stages: the deploy artifact (OCIRepository
`deploy`), the app values (Kustomization `deploy`), the chart (OCIRepository `shelf-app`), the
app (HelmRelease), the pods, and the tests; each stage is Ready, Working, Failed or Missing, and
the app's state is that of the first stage that is not Ready.

#### Scenario: Image cannot be pulled
- **WHEN** all Flux stages are Ready and a pod waits in `ImagePullBackOff`
- **THEN** the pods stage is Failed with a hint that the image cannot be pulled, and the app is Failed

#### Scenario: Secret missing from the app's Secret
- **WHEN** a pod fails with `CreateContainerConfigError` because a key is missing in `shelf-secrets`
- **THEN** the hint names the secret and says to deploy the app again so shelf generates it

#### Scenario: Not yet created
- **WHEN** the platform has not created the app's objects yet
- **THEN** the first stage is Missing with the message that the platform has not created the app yet

#### Scenario: Failing test
- **WHEN** every pod is ready and the test `smoke` exited with 1
- **THEN** the tests stage is Failed naming `smoke`, and the app is Failed

### Requirement: App status command
`shelf app status` SHALL list every app with its state and, when not Ready, the failing stage and
reason in one line; `shelf app status <app>` SHALL print the app's address, artifact, registry
connection, running revision and commit, deploy time, components, its verification, its
isolation, every stage with its message and, if not Ready, its hint, and its previews with tag,
state and commit; `--preview <branch>` SHALL print the same for one preview.

#### Scenario: No apps
- **WHEN** the cluster has no registered apps
- **THEN** `shelf app status` prints "No apps yet."

#### Scenario: One app in detail
- **WHEN** a user runs `shelf app status hello` for a healthy app
- **THEN** the output shows `hello: Ready`, the artifact reference, the revision, and all six stages as Ready

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
- **THEN** the output shows the preview's namespace `hello--feature-login`, its host, artifact tag, commit, components and six stages
