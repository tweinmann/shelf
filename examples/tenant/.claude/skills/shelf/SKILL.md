---
name: shelf
description: Check that a pushed commit of this app runs on shelf, on main or in the preview of a branch, and find out why it does not. Use after every git push, and whenever the user asks whether the app or a branch is deployed, healthy or broken.
---

# Deploying this app with shelf

This repository is a shelf tenant: CI builds the images, renders `app.yaml` and pushes a deploy
artifact; shelf notices it and rolls it out. You never deploy by hand. Your job after a push is
to get the verdict on *your* commit and fix what it names, without sitting idle while CI runs.

The app's name on shelf is in `CLAUDE.md` (the operator chose it; it is not `name` in
`app.yaml`). Below it is written `<app>`.

## Where your commit runs

- **`main`** runs as the app itself.
- **Any other branch** runs as a preview of its own, if the operator switched previews on: an
  isolated copy of the whole app, database included, named `<app>--<slug>` (`feature/login`
  becomes `<app>--feature-login`). For such a branch, add `--preview <branch>` to every command
  below.
- The first push of a new branch takes about a minute longer than later ones: the preview is
  created first.
- If the wait says the app has no previews, work on `main`, or ask the operator.

## The loop

1. Commit and push.
2. Start the wait in the background, and go on with other work. Do not wait in the foreground
   for CI:

   ```sh
   shelf app status <app> --wait --commit "$(git rev-parse HEAD)" --json
   shelf app status <app> --preview <branch> --wait --commit "$(git rev-parse HEAD)" --json
   ```

   The first line is for `main`, the second for a branch. The wait returns when that commit runs
   and is Ready, when a step failed, or after 10 minutes (`--timeout 20m` for a slow build). It
   only reads the cluster; never try to make it faster.
3. If the GitHub CLI is available, watch the CI run of your push in the background as well:
   `gh run watch --exit-status` on the run of your commit (`gh run list --commit <sha>`). A
   failed build shows there within seconds. Read it with `gh run view --log-failed`, fix it,
   and push again. Don't wait for the shelf wait to time out on an artifact that will never
   come.
4. Before you report the work as done, read the wait's exit status and the `WaitResult` on
   stdout:

   | Exit | `outcome` | What to do |
   |---|---|---|
   | 0 | `Ready` | Done. The commit runs. |
   | 1 | `Failed` | Read `stage.name`, `stage.reason`, `stage.message` and `stage.hint`, fix the cause in this repository, commit, push, and wait again. |
   | 2 | `TimedOut` | `stage` says where it waited. "waiting for commit …" or "waiting for the preview of branch …" means the artifact of your commit never arrived: check the CI run of the push first (`gh run list`, `gh run view --log-failed`). |

   After a failed commit, the next one can wait several minutes at "the app" with "waiting for
   the release to pick up the new values": Helm first gives up on the broken upgrade and rolls it
   back. That is expected; keep waiting.

   Any other failure prints a document of kind `Error` with a `message` (no cluster, no such
   app, no previews) and exits 1 without a `WaitResult`. Report it; it is not something to fix
   in code.

5. For the whole picture, run `shelf app status <app> --json` (or with `--preview <branch>`):
   - every stage from the deploy artifact to the pods and the tests;
   - the running `commit`, the components and their addresses;
   - `tests`, with the exit code and the end of the log of one that failed;
   - for the app, its `previews`.
6. When the branch has a pull request, post the report of its preview there:

   ```sh
   shelf app report <app> --preview <branch> | gh pr comment --body-file -
   ```

   It says whether the preview is Ready, how its tests went, whether it is signed and scanned,
   and which images differ from `main`.

## What the stages mean

The stages, in order:
1. `the deploy artifact`: CI did not push, the registry refuses the login, or the artifact is not
   signed by the cluster's signer (push again; `deploy.yml` needs `id-token: write`).
2. `the app values`: the rendered app.yaml was rejected.
3. `the chart`: a platform problem; report it.
4. `the app`: the Helm release; often a bad `app.yaml`.
5. `the pods`: image pull, crash loop, out of memory, missing secret, a preview's quota, or a
   built image the admission policy refused because it is not signed.
6. `the tests`: a test under `tests:` in `app.yaml` exited with something else than 0 against
   this version. The message has its exit code and the end of its log. On `main` the previous
   version runs again; on a branch the preview keeps the failing version, so the operator can
   look at it. Fix the code or the test, and push again.

The first stage that is not Ready is the cause; everything after it is a consequence.
`shelf validate app.yaml` catches most app.yaml mistakes before a push.

## Tests

`tests:` in `app.yaml` declares containers that run against the app after every rollout, inside
its namespace. A test reaches the components by their references, such as
`http://${web.host}:${web.port}/`, never through a route, and passes when it exits with 0. Add
one for what a change must not break; the wait counts the commit as Ready only once they pass.

## What you cannot do

- Make the app or a preview public. Exposure (a domain or a tunnel) is the operator's decision,
  made with `shelf app add` or `shelf app credentials` outside this repository. Nothing in
  `app.yaml` changes it, so do not try. Previews are never exposed.
- Switch previews on or off; that is the operator's decision too.
- Reach other apps, the operator's LAN or the Kubernetes API from the app. The app reaches its
  own components, DNS and the internet only; anything more is the operator's decision
  (`--allow-lan`), and a preview never gets it.
- Read secret values. `shelf app secrets` is for the operator; a preview's secrets are its own.
- `shelf app open <app>` (with `--preview <branch>` for a preview) makes the app reachable in a
  browser on the operator's machine. It is for the person, not for you; ask them to look if a
  page has to be checked by eye.
