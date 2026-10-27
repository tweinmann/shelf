---
name: shelf
description: Check that a pushed commit of this app runs on shelf, and find out why it does not. Use after every git push to main, and whenever the user asks whether the app is deployed, healthy or broken.
---

# Deploying this app with shelf

This repository is a shelf tenant: CI builds the images, renders `app.yaml` and pushes a deploy
artifact; shelf notices it within a minute and rolls it out. You never deploy by hand. Your job
after a push is to wait for the verdict on *your* commit and fix what it names.

The app's name on shelf is in `CLAUDE.md` (the operator chose it; it is not `name` in
`app.yaml`). Below it is written `<app>`.

## The loop

1. Commit and push to `main`.
2. Wait for the verdict on exactly that commit:

   ```sh
   shelf app status <app> --wait --commit "$(git rev-parse HEAD)" --json
   ```

   This blocks until the app runs that commit and is Ready, a step failed, or 10 minutes
   passed (`--timeout 20m` for a slow build). It only reads the cluster; never try to make it
   faster.
3. Read the exit status and the `WaitResult` document on stdout:

   | Exit | `outcome` | What to do |
   |---|---|---|
   | 0 | `Ready` | Done. The commit runs. |
   | 1 | `Failed` | Read `stage.name`, `stage.reason`, `stage.message` and `stage.hint`, fix the cause in this repository, commit, push, and wait again. |
   | 2 | `TimedOut` | `stage` says where it waited. "waiting for commit …" means the artifact of your commit never arrived: check the CI run of the push first (`gh run list`, `gh run view --log-failed`). |

   After a failed commit, the next one can wait several minutes at "the app" with "waiting for
   the release to pick up the new values": Helm first gives up on the broken upgrade and rolls it
   back. That is expected; keep waiting.

   Any other failure prints a document of kind `Error` with a `message` (no cluster, no such
   app) and exits 1 without a `WaitResult`; report it, it is not something to fix in code.

4. For the whole picture of one app, run `shelf app status <app> --json`: every stage from the
   deploy artifact to the pods, with the running `commit`, the components and their addresses.

## What the stages mean

`the deploy artifact` (CI did not push, or the registry refuses the login) → `the app values`
(the rendered app.yaml was rejected) → `the chart` (platform problem; report it) → `the app`
(the Helm release; often a bad `app.yaml`) → `the pods` (image pull, crash loop, out of memory,
missing secret). The first stage that is not Ready is the cause; everything after it is a
consequence. `shelf validate app.yaml` catches most app.yaml mistakes before a push.

## What you cannot do

- Make the app public. Exposure (a domain or a tunnel) is the operator's decision, made with
  `shelf app add` or `shelf app credentials` outside this repository. Nothing in `app.yaml`
  changes it, so do not try.
- Read secret values. `shelf app secrets` is for the operator.
- `shelf app open <app>` makes the app reachable in a browser on the operator's machine. It is
  for the person, not for you; ask them to look if a page has to be checked by eye.
