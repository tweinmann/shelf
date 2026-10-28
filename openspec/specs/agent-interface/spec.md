# agent-interface Specification

## Purpose
The contract between shelf and the coding agents that work in tenant repositories: machine-readable
JSON documents for the read-only commands, a wait that ends in a verdict an agent can act on, and
the instructions the tenant template gives an agent about that loop.

## Requirements

### Requirement: Versioned JSON documents
A command run with `--json` SHALL print exactly one JSON object to stdout and nothing else there,
with the fields `apiVersion` (`shelf.dev/v1alpha1`) and `kind` naming the document. Progress,
hints and warnings SHALL go to stderr only.

#### Scenario: Status of all apps as JSON
- **WHEN** a user runs `shelf app status --json` against a cluster with the apps `greeter` and `hello`
- **THEN** stdout is one JSON object with `apiVersion` `shelf.dev/v1alpha1`, `kind` `AppList` and an `items` array with one entry per app
- **AND** stdout parses as JSON without stripping any line

#### Scenario: No apps
- **WHEN** a user runs `shelf app status --json` against a cluster without apps
- **THEN** it prints an `AppList` with an empty `items` array instead of "No apps yet."

### Requirement: Errors as JSON
A command run with `--json` that fails SHALL print a document of kind `Error` with a `message` to
stdout, write the same message to stderr, and exit with status 1.

#### Scenario: Unknown app
- **WHEN** a user runs `shelf app status nope --json` and no app `nope` exists
- **THEN** stdout is `{"apiVersion":"shelf.dev/v1alpha1","kind":"Error","message":"app nope does not exist"}` and the exit status is 1

#### Scenario: Cluster unreachable
- **WHEN** a user runs `shelf app status --json` and the API server does not answer
- **THEN** stdout is an `Error` document with the reason, and the exit status is 1

### Requirement: Stable fields within a version
Within one `apiVersion`, a field of a JSON document SHALL never be removed, renamed or change its
type or meaning; new fields MAY be added, and a consumer is expected to ignore fields it does not
know. A field without a value SHALL be omitted, never set to `null`.

#### Scenario: Field added in a later release
- **WHEN** a later release adds a field to `AppStatus`
- **THEN** `apiVersion` stays `shelf.dev/v1alpha1` and every earlier field keeps its name, type and meaning

#### Scenario: Incompatible change
- **WHEN** a field has to change its meaning
- **THEN** the change ships as a new `apiVersion`, and the golden file of the old document fails until it is renamed with it

### Requirement: Commands with JSON output
`--json` SHALL be offered by `shelf app status` (kind `AppList` without a name, `AppStatus` with
one, `WaitResult` with `--wait`), `shelf app tags` (`TagList`), `shelf connection list`
(`ConnectionList`) and `shelf version` (`Version`).

#### Scenario: One app in detail
- **WHEN** a user runs `shelf app status hello --json` for a healthy app
- **THEN** the `AppStatus` holds the app's name, phase, address, artifact, registry connection, exposure, revision, commit, deploy time, components with their addresses, and the five stages each with name, object, phase and, where present, reason, message and hint

#### Scenario: Tags
- **WHEN** a user runs `shelf app tags hello --json`
- **THEN** the `TagList` lists the tags newest first, and the tag the app runs with has `current: true`

#### Scenario: Version
- **WHEN** a user runs `shelf version --json`
- **THEN** the `Version` document holds the shelf version and the app.yaml `apiVersion` it reads

### Requirement: No secret values in JSON
No JSON document SHALL contain a secret value or a token. `shelf app secrets` SHALL NOT offer
`--json`, and `ConnectionList` SHALL hold each connection's kind, name, account or user, and the
apps that use it, never its token.

#### Scenario: Connections as JSON
- **WHEN** a user runs `shelf connection list --json` with a registry and a Cloudflare connection
- **THEN** each entry has `kind`, `name` and `apps`, the registry entry its user, the Cloudflare entry its account or that this machine does not hold it, and no field of the document holds either token

#### Scenario: Secrets
- **WHEN** a user runs `shelf app secrets hello --json`
- **THEN** the command fails because the flag is unknown

### Requirement: Waiting for an app to settle
`shelf app status <app> --wait` SHALL poll the app's chain until it is Ready or a stage is
Failed, or until `--timeout` (default 10 minutes) runs out, and then print a verdict.

#### Scenario: App already healthy
- **WHEN** a user runs `shelf app status hello --wait` for a Ready app
- **THEN** it returns at once with the outcome `Ready`

#### Scenario: Rollout still running
- **WHEN** the app's pods are starting and become ready a minute later
- **THEN** the command returns after they are ready, with the outcome `Ready`

### Requirement: Waiting for a commit
With `--commit <sha>`, the wait SHALL first wait until the app's deploy artifact was built from that
git commit, matching a full SHA or a prefix of at least 7 hex digits, and then until every stage
has caught up with that artifact; it SHALL NOT report Ready for the previous artifact.

#### Scenario: Push still in CI
- **WHEN** an agent runs `shelf app status hello --wait --commit 42a964e --json` right after `git push`, and the artifact of that commit arrives four minutes later
- **THEN** the command keeps waiting while the previous artifact runs, and returns `Ready` only once the release built from `42a964e` has rolled out

#### Scenario: Short prefix
- **WHEN** `--commit` is given fewer than 7 hex digits or something that is not hex
- **THEN** the command fails at once without waiting

#### Scenario: Artifact without a commit
- **WHEN** the app's deploy artifact carries no git revision, as one pushed by hand may not
- **THEN** the wait for a commit runs into its timeout, and the verdict says that the artifact names no commit

### Requirement: Wait verdict and exit status
The wait SHALL end with a verdict: outcome `Ready`, `Failed` or `TimedOut`, the commit and revision
that run, and, unless Ready, the first stage that is not Ready with its reason, message and hint.
The exit status SHALL be 0 for Ready, 1 for Failed and 2 for TimedOut.

#### Scenario: Image cannot be pulled
- **WHEN** the awaited commit's release starts a pod that waits in `ImagePullBackOff`
- **THEN** the verdict is `Failed` with the stage "the pods", the reason `ImagePullBackOff` and the hint that the image cannot be pulled, and the exit status is 1

#### Scenario: Timeout
- **WHEN** the app is still Working when `--timeout 2m` runs out
- **THEN** the verdict is `TimedOut` naming the stage that is still Working, and the exit status is 2

#### Scenario: Error before a verdict
- **WHEN** the app does not exist or the cluster cannot be reached
- **THEN** the command prints an error, not a verdict, and exits with status 1

### Requirement: Waiting is read-only
`shelf app status` with or without `--wait` SHALL NOT write to the cluster, ask a Flux controller
to reconcile, or write to `~/.shelf`.

#### Scenario: Waiting for a new artifact
- **WHEN** an agent waits for a commit that is not yet in the cluster
- **THEN** shelf only reads, and the artifact arrives at the deploy artifact's own polling interval

### Requirement: Agent instructions in the tenant template
`examples/tenant` SHALL hold a Claude Code skill `.claude/skills/shelf/SKILL.md` and a `CLAUDE.md`
section that tell an agent to push, run `shelf app status <app> --wait --commit <sha> --json`
(with `--preview <branch>` on a branch), read the verdict, and fix what its stage and hint name;
the app name is a placeholder the operator fills in. The skill SHALL tell the agent to run the
wait in the background and, when the GitHub CLI is available, to check the CI run of its push
first.

#### Scenario: Agent loop described
- **WHEN** an agent working in a copy of `examples/tenant` reads the skill
- **THEN** it finds the wait command with `--commit` and `--json`, the meaning of the three exit statuses, and that it cannot make the app public

#### Scenario: No hook
- **WHEN** the tenant template is inspected
- **THEN** it ships no Claude Code hook that runs the wait

#### Scenario: Waiting without idling
- **WHEN** an agent reads the skill after a push
- **THEN** it is told to run the wait in the background, to go on with other work, and to read the verdict before it reports the work as done

#### Scenario: Broken build found early
- **WHEN** the CI run of the push fails
- **THEN** the skill tells the agent to read the failed run (`gh run watch --exit-status`, `gh run view --log-failed`) instead of waiting for the wait's timeout

#### Scenario: Work on a branch
- **WHEN** the agent pushed to a branch other than `main`
- **THEN** the skill tells it to add `--preview <branch>` to the wait, and that a first push of a branch takes about a minute longer than a later one

### Requirement: Previews in the agent interface
`shelf app status <app> --preview <branch>` SHALL accept `--wait`, `--commit`, `--timeout` and
`--json` exactly as for the app, about that preview; the `AppStatus` of an app SHALL list its
previews under `previews`, each with `slug`, `tag`, `namespace`, `host`, `phase` and `commit`,
and `previewLimit` and `previewLimitReached` when the app has previews.

#### Scenario: Agent waits for its branch
- **WHEN** an agent pushes commit `3f2a9c1` to the branch `feature/login` and runs `shelf app status hello --preview feature/login --wait --commit 3f2a9c1 --json`
- **THEN** it gets a `WaitResult` for `hello--feature-login` with exit status 0, 1 or 2 as for an app, and the app's own verdict plays no part

#### Scenario: Preview appears only after the push
- **WHEN** the agent waits for a branch whose first artifact is still being built
- **THEN** the wait counts the missing preview as Working and does not fail before its timeout

#### Scenario: Previews in the JSON of an app
- **WHEN** a user runs `shelf app status hello --json` for an app with two previews and `--previews=3`
- **THEN** `previews` has two entries, `previewLimit` is 3 and `previewLimitReached` is false

#### Scenario: App without previews
- **WHEN** an app has no previews switched on
- **THEN** its `AppStatus` has no `previews`, `previewLimit` or `previewLimitReached` field

### Requirement: Verification in the agent interface
The `AppStatus` of an app or preview SHALL hold `verification` with `verified`, `reason` and
`hint` when not verified, `source`, `signer`, `exposable`, and `scan`, one entry per component
with `component`, `digest`, `scanned`, `critical`, `high`, `scanner` and `scannedAt`; a
`WaitResult` with outcome `Ready` says nothing about verification.

#### Scenario: Verified app as JSON
- **WHEN** a user runs `shelf app status hello --json` for a verified app with `web` and `db`
- **THEN** `verification.verified` and `verification.exposable` are true and `scan` has two entries

#### Scenario: Agent reads why it cannot be exposed
- **WHEN** the running artifact has no scan summary
- **THEN** `verification.verified` is false and `reason` names the missing scan
