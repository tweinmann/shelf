# Spec Delta

## MODIFIED Requirements

### Requirement: App status command
`shelf app status` SHALL list every app with its state and, when not Ready, the failing stage and
reason in one line; `shelf app status <app>` SHALL print the app's address, artifact, registry
connection, running revision, the git commit that revision was built from, deploy time,
components, and every stage with its message and, if not Ready, its hint.

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
