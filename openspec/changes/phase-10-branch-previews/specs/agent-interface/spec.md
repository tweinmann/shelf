# Spec Delta

## ADDED Requirements

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

## MODIFIED Requirements

### Requirement: Agent instructions in the tenant template
`examples/tenant` SHALL hold a Claude Code skill `.claude/skills/shelf/SKILL.md` and a `CLAUDE.md`
section that tell an agent to push, run `shelf app status <app> --wait --commit <sha> --json`, read
the verdict, and fix what its stage and hint name; the app name is a placeholder the operator
fills in. The skill SHALL tell the agent to start the wait in the background and keep working
until it needs the verdict, to check the CI run of its push first when the GitHub CLI is
available, and to wait for its branch's preview with `--preview` when it works on a branch.

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
