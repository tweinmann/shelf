# greeter

A shelf tenant: `app.yaml` describes the app, `.github/workflows/deploy.yml` builds and
publishes it, and shelf deploys every push to `main`, and every other branch as a preview of its
own if the operator switched previews on.

## Deploying

The app runs on shelf as **`greeter`**. (Operator: replace this with the name you gave it in
`shelf app add <app> …`; it need not match `name` in `app.yaml`.)

After every push, use the `shelf` skill: start
`shelf app status greeter --wait --commit "$(git rev-parse HEAD)" --json` in the background (on a
branch, with `--preview <branch>`), keep working, and fix what the verdict names. The work is
done when that command exits 0, which needs the tests under `tests:` in `app.yaml` to pass. On
a branch with a pull request, post `shelf app report greeter --preview <branch>` there.
