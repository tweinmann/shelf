# Spec Delta

## ADDED Requirements

### Requirement: Tests and isolation in the agent interface
The `AppStatus` of an app or preview SHALL hold `tests`, one entry per test of the running
revision with `name`, `phase` and, when it failed, `exitCode` and `log` (its last lines), and
`isolation` with `podSecurity`, `restrictedViolations` (each with `pod` and `reason`) and
`lan` (the exceptions).

#### Scenario: Failed test as JSON
- **WHEN** an agent runs `shelf app status hello --preview feature-login --json` and the test `smoke` failed
- **THEN** `tests` holds `smoke` with phase `Failed`, its exit code and its log, and the stage "the tests" is Failed

### Requirement: Verification report
The system SHALL provide `shelf app report <app> [--preview <branch>]`, read-only, which prints a
Markdown report and with `--json` a `Report` document: the app or preview, its address and how to
open it, the commit, the state with the first stage that is not Ready, the tests, the
verification with the scan per component, and, for a preview, the comparison with the app: each
component whose image digest differs, and the CRITICAL and HIGH counts of the preview against
the app's.

#### Scenario: Report for a pull request
- **WHEN** an agent runs `shelf app report hello --preview feature-login` for a Ready, verified preview whose `web` image differs from the app's
- **THEN** the Markdown names the commit, says Ready, lists the passed tests, the signature and scan, and lists `web` as changed with the findings of both

#### Scenario: Report of an app
- **WHEN** the report is printed without `--preview`
- **THEN** it has no comparison section

#### Scenario: Report as JSON
- **WHEN** `--json` is given
- **THEN** a `Report` document is printed with the same content, and no Markdown

## MODIFIED Requirements

### Requirement: Commands with JSON output
`--json` SHALL be offered by `shelf app status` (kind `AppList` without a name, `AppStatus` with
one, `WaitResult` with `--wait`), `shelf app report` (`Report`), `shelf app tags` (`TagList`),
`shelf connection list` (`ConnectionList`) and `shelf version` (`Version`).

#### Scenario: One app in detail
- **WHEN** a user runs `shelf app status hello --json` for a healthy app
- **THEN** the `AppStatus` holds the app's name, phase, address, artifact, registry connection, exposure, revision, commit, deploy time, components with their addresses, and the six stages each with name, object, phase and, where present, reason, message and hint

#### Scenario: Tags
- **WHEN** a user runs `shelf app tags hello --json`
- **THEN** the `TagList` lists the tags newest first, and the tag the app runs with has `current: true`

#### Scenario: Version
- **WHEN** a user runs `shelf version --json`
- **THEN** the `Version` document holds the shelf version and the app.yaml `apiVersion` it reads
