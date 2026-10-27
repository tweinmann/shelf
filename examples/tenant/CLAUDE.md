# greeter

A shelf tenant: `app.yaml` describes the app, `.github/workflows/deploy.yml` builds and
publishes it, and shelf deploys every push to `main`.

## Deploying

The app runs on shelf as **`greeter`**. (Operator: replace this with the name you gave it in
`shelf app add <app> …`; it need not match `name` in `app.yaml`.)

After every push to `main`, use the `shelf` skill: wait with
`shelf app status greeter --wait --commit "$(git rev-parse HEAD)" --json` and fix what the
verdict names. The work is done when that command exits 0.
