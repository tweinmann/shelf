# app-tests Specification

## Purpose
Lets an app check itself after every rollout: the tests its `app.yaml` declares run against the
app or preview in its own namespace, and an app counts as green only when they pass.

## Requirements

### Requirement: Tests run after every rollout
The platform SHALL run every test an app declares after each install and upgrade of the app's or
preview's release, in its namespace, one pod per test with the app's resources defaults and a
timeout of 5 minutes for all of them together; a test passes when its container exits with 0.

#### Scenario: New commit with a test
- **WHEN** a new artifact of an app with a test `smoke` rolls out
- **THEN** the pod for `smoke` runs after the components are ready, and its result is recorded for that release revision

#### Scenario: App without tests
- **WHEN** an app declares no tests
- **THEN** its rollout runs no test, and the tests stage is Ready

### Requirement: A failing test on an app keeps the previous version
When a test fails after an upgrade of an app, the platform SHALL count the upgrade as failed, so
the release returns to the last revision whose tests passed; after a failed first install the
failing version keeps running.

#### Scenario: Broken commit on main
- **WHEN** a new artifact of `hello` makes its test `smoke` exit with 1
- **THEN** `hello` runs its previous version again, and the tests stage is Failed naming `smoke`

### Requirement: A failing test on a preview keeps the preview
When a test fails on a preview, the platform SHALL keep the version that failed running, so the
branch can be looked at with `shelf app open --preview`.

#### Scenario: Broken branch
- **WHEN** the artifact of `branch-feature-login` makes the test `smoke` fail
- **THEN** the preview runs that artifact, and its tests stage is Failed naming `smoke`

### Requirement: Tests in the status and the wait
The tests stage SHALL follow the pods stage: Ready when every test of the running revision passed
or there are none, Working while they run, Failed when one failed, with the failing test's name,
its exit code and the last lines of its log as the message; `shelf app status --wait --commit`
SHALL end with `Ready` only after the tests of that commit passed.

#### Scenario: Waiting for a commit whose test fails
- **WHEN** an agent runs `shelf app status hello --preview feature-login --wait --commit <sha>` and the test `smoke` fails for that commit
- **THEN** the verdict is `Failed` with the stage "the tests", the test name and the end of its log, and the exit status is 1

#### Scenario: Tests still running
- **WHEN** the pods are ready and the test `smoke` still runs
- **THEN** the app is Working at the stage "the tests"
