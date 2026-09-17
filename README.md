# shelf

**A small, self-hosted app platform for a single Apple Silicon Mac mini.**

Describe your app in one short `app.yaml`, build your images in your own CI, and shelf runs
them on Kubernetes: with persistent volumes, generated secrets, health checks and a public URL
at `<app>.<your-domain>`.

> [!WARNING]
> shelf is a learning project for platform engineering. It is **not meant for production**,
> and it is **still under construction**: a push to your repository already deploys your app to
> a Kubernetes cluster, but the app is not reachable from the internet yet, and the Mac mini
> setup is missing. See [Status](#status).

## Contents

- [Why shelf](#why-shelf)
- [How it works](#how-it-works)
- [Status](#status)
- [Quick start](#quick-start)
- [Deploying an app](#deploying-an-app)
- [Writing an `app.yaml`](#writing-an-appyaml)
- [CLI reference](#cli-reference)
- [Developing shelf](#developing-shelf)
- [Further reading](#further-reading)
- [License](#license)

## Why shelf

Running a few side projects on a machine at home usually means handwritten Kubernetes
manifests, or a pile of `docker run` commands. shelf sits in between:

- **One file per app.** Components, ports, routes, volumes and secrets go in a single
  `app.yaml`. shelf turns it into Deployments, StatefulSets, Services, Ingresses and PVCs.
- **Your CI stays yours.** Your pipeline builds the images and pushes them to GitHub Container
  Registry (GHCR). shelf never talks to Git or to your forge: the registry is the only
  interface.
- **Secrets never show up in manifests.** Passwords are generated on the platform and
  referenced as `${secrets.db-password}`.
- **Standard building blocks.** Flux, Helm, Traefik, Cloudflare Tunnel and external-dns do the
  heavy lifting. shelf is mostly glue.
- **Apps are isolated.** Each app gets its own namespace, and nothing is shared between apps.

## How it works

```mermaid
flowchart LR
    subgraph ci["Your CI (GitHub Actions)"]
        A[app.yaml] -->|shelf render| B[resolved app.yaml]
        I[images, linux/arm64]
    end
    B --> R[(GHCR)]
    I --> R
    subgraph mini["Mac mini (k3s)"]
        F[Flux] --> H[Helm chart shelf-app]
        H --> K[Deployments, StatefulSets,<br/>Services, Ingress, PVCs]
        K --> X[Traefik]
    end
    F -->|watches| R
    X --> T[Cloudflare Tunnel] --> U(("https://app.example.com"))
```

1. Your workflow builds the images and runs `shelf render` on your `app.yaml`. This pins every
   image to a digest, fills in host names and ports, and checks your file.
2. It pushes the result to GHCR as a small deploy artifact.
3. Flux on the Mac mini notices the new artifact and hands it to the generic `shelf-app` Helm
   chart, which creates the Kubernetes objects.
4. Traefik routes requests by host name, and a Cloudflare Tunnel makes the app reachable from
   the internet without opening ports.

The design, all decisions and the roadmap are in [docs/plan.md](docs/plan.md).

## Status

shelf is built in phases. After each phase the maintainer reviews the result before the next
one starts.

| Phase | Content | State |
|---|---|---|
| 0 | Devcontainer, local k3d cluster, smoke tests | ✅ done |
| 1 | `app.yaml` schema, `shelf validate`, `shelf render`, CI | ✅ done |
| 0b | Switch the devcontainer to Docker-in-Docker | ✅ done |
| 2 | Helm chart `shelf-app` | ✅ done |
| 3 | `shelf init cluster`: Flux, Traefik | ✅ done |
| 4 | Delivery: deploy artifact, `shelf app add` / `rm`, reusable workflow | ✅ done |
| 4b | `build:` in `app.yaml`, release binaries, tenant repo without image names | 🔍 in review |
| 5 | `shelf init expose`: Cloudflare Tunnel, DNS | planned |
| 6 | Installation on the Mac mini | planned |
| 7 | Reference apps | planned |

## Quick start

Download the latest release for your platform (linux or darwin, amd64 or arm64):

```sh
curl -fsSL -o shelf.tar.gz \
  https://github.com/tweinmann/shelf/releases/latest/download/shelf_darwin_arm64.tar.gz
tar -xzf shelf.tar.gz && sudo install shelf /usr/local/bin/shelf
```

Or build it from source (Go 1.27 or later):

```sh
git clone https://github.com/tweinmann/shelf.git
cd shelf
go build -o bin/shelf ./cmd/shelf
```

Check the example app. `validate` works offline:

```console
$ shelf validate examples/hello/app.yaml
examples/hello/app.yaml: valid
```

Render it. This looks up the images in their registries:

```console
$ shelf render -o app examples/hello/app.yaml
apiVersion: shelf.dev/v1alpha1
name: hello
components:
  db:
    image: postgres:16@sha256:f1c3376c…
    ports:
      main: 5432
    ...

App hello (namespace hello):
  check  Deployment × 1   postgres:16@sha256:f1c3376c…
  db     StatefulSet × 1  postgres:16@sha256:f1c3376c…
         Service db       main=5432
         PVC data-db-0    1Gi at /var/lib/postgresql/data
  web    Deployment × 2   traefik/whoami:v1.11.0@sha256:20068979…
         Service web      main=80
         Route            / → port main
  secret db-password (generated) → env SHELF_SECRET_DB_PASSWORD
```

The manifest goes to stdout and the summary to stderr, so you can redirect the manifest to a
file.

## Deploying an app

Your repository needs two files. In `app.yaml`, a component you build yourself points at its
directory instead of an image:

```yaml
components:
  web:
    build: ./web          # directory with the Dockerfile
    port: 8080
    route: /
```

The second file is the same for every app:

```yaml
# .github/workflows/deploy.yml
on:
  push:
    branches: [main]
  pull_request:
permissions:
  contents: read
  packages: write
jobs:
  shelf:
    uses: tweinmann/shelf/.github/workflows/build.yml@v0
```

The workflow builds every `build` directory for `linux/arm64`, pushes the images, pins them to
their digests, and publishes the deploy artifact `ghcr.io/<owner>/<app>-deploy`. Image names
never appear in `app.yaml`. Ready-made images such as `postgres:16` stay as `image:`.
[examples/tenant](examples/tenant) is a complete example repository.

Once per cluster, install the platform. The GHCR login lets the cluster pull private images
and artifacts; use a classic personal access token with `read:packages`:

```sh
export GHCR_USERNAME=<you> GHCR_TOKEN=<token>
shelf init cluster --domain example.com
```

Once per app, add it. shelf generates the app's secrets and keeps a backup in
`~/.shelf/apps/<app>/secrets.yaml`:

```sh
docker login ghcr.io          # shelf reads the artifact with your Docker credentials
shelf app add <app> oci://ghcr.io/<owner>/<app>-deploy:main
```

From then on, every push to `main` reaches the cluster by itself, usually within two minutes.
`shelf app rm <app>` removes the app with all its data.

## Writing an `app.yaml`

A complete example with a web frontend, an API and a Postgres database:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/tweinmann/shelf/main/schema/app.schema.json
apiVersion: shelf.dev/v1alpha1
name: shop                       # becomes the namespace and shop.<your-domain>

components:
  web:
    image: ghcr.io/you/shop-web:1.4.0
    port: 8080
    route: /                     # reachable from outside
    instances: 2
    health: { path: /healthz }
    resources: { cpu: 500m, memory: 512Mi }

  api:
    image: ghcr.io/you/shop-api:1.4.0
    port: 3000
    route: /api
    env:
      DATABASE_URL: postgres://app:${secrets.db-password}@${db.host}:${db.port}/shop
    volumes:
      uploads: { path: /data/uploads, size: 5Gi }

  db:
    image: postgres:16
    port: 5432                   # no route: only reachable inside the app
    env:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: ${secrets.db-password}
      POSTGRES_DB: shop
      PGDATA: /var/lib/postgresql/data/pgdata
    volumes:
      data: { path: /var/lib/postgresql/data, size: 20Gi }

secrets:
  db-password: { generate: true }
```

The first line gives you autocompletion and inline checks in VS Code (with the YAML extension)
and other editors that support JSON Schema. `shelf schema` prints the same schema.

### Component fields

| Field | Meaning |
|---|---|
| `image` | A ready-made image, e.g. `postgres:16`. `shelf render` pins tags to a digest. The image must provide `linux/arm64`. |
| `build` | A directory of this repository with a Dockerfile, e.g. `./web`. Your CI builds and pushes it. Use either `image` or `build`. |
| `command`, `args` | Override the image's entrypoint and command, as in Kubernetes. |
| `env` | Environment variables as a map. Numbers and booleans are fine (`PORT: 8080`). |
| `port` | The single port the container listens on. |
| `ports` | Several named ports, e.g. `{ http: 8080, metrics: 9100 }`. Use either `port` or `ports`. |
| `route` | Makes the component reachable from outside: `route: /api`, or `route: { path: /api, port: http, stripPrefix: true }`. The long form is required when the component has several ports. |
| `instances` | Number of replicas, default 1. Replicas do not form a cluster; that is up to your app. |
| `volumes` | Persistent volumes: `name: { path, size }`. A component with volumes runs as a StatefulSet. |
| `health` | HTTP probe: `{ path, port?, initialDelay? }`, with `initialDelay` in seconds. It tells Kubernetes (and your deploy) when the component is ready. |
| `resources` | `{ cpu, memory }` requests. Memory is also the hard limit, which protects the other apps on the machine. |

### References

Inside `env`, `command` and `args` you can use:

| Reference | Becomes |
|---|---|
| `${db.host}` | the host name of component `db` |
| `${db.port}` | its port, if it has exactly one |
| `${api.ports.metrics}` | a named port |
| `${secrets.db-password}` | the secret value, injected at runtime; it never appears in any manifest |

`$` works like in docker compose: `$$` is a literal `$`, and a `$` that is not followed by `{`
stays as it is. So `sh -c 'echo $HOME'` works without escaping.

### Things to know

- **Volume sizes are not enforced.** The local storage on the Mac mini does not limit
  capacity, so `size` is documentation. A component can fill the disk.
- **Plan volumes up front.** Kubernetes does not allow changing the volumes of a StatefulSet in
  place, and the local storage cannot grow, so changing an existing volume later needs manual
  steps. Giving a component its first volumes, or removing all of them, works: the component
  restarts, and `${<component>.host}` keeps pointing at it. **Removing volumes deletes their
  data.**
- **Names:** app and component names use lowercase letters, digits and `-`, and are at most
  40 characters long. `secrets`, `app` and `shelf` are reserved component names, and component
  names must not end with `-headless`.
- **Secrets:** only generated secrets (`generate: true`) are supported for now. Generated
  values are URL-safe, so you can put them into connection strings as they are.

## CLI reference

| Command | What it does |
|---|---|
| `shelf validate <app.yaml>...` | Checks one or more files without network access. Errors and warnings show file, line and field. Exits with 1 on errors. |
| `shelf render <app.yaml>` | Validates, resolves images and prints the deploy manifest (a ConfigMap). `-o app` prints the resolved `app.yaml` instead. `--image <component>=<reference>` supplies the image of a component with a `build` directory. Registry credentials come from `docker login`. |
| `shelf schema` | Prints the JSON Schema for `app.yaml`. |
| `shelf build-plan <app.yaml>` | Prints the components with a `build` directory as JSON. The workflow uses it to know what to build. |
| `shelf init cluster --domain <domain>` | Installs Flux and the platform (Traefik, app management) into the cluster of the current kubecontext, and waits until everything is ready. The GHCR login comes from `GHCR_USERNAME` and `GHCR_TOKEN`. Shows the target cluster and asks before changing anything (`--yes` skips the question); `--context` and `--kubeconfig` pick another cluster. Safe to run again. |
| `shelf app add <app> <oci://…:tag>` | Deploys an app from its deploy artifact and keeps it updated. Generates the app's secrets, stores them in the cluster and in `~/.shelf/apps/<app>/secrets.yaml`, and restores them from there after a cluster rebuild. Waits until the app is ready. Safe to run again, e.g. after adding a secret. |
| `shelf app rm <app>` | Removes an app with its namespace, volumes and secrets, after asking. The secret backup stays. |
| `shelf version` | Prints the version. |

Example of an error message:

```text
app.yaml:12: error: components.web.route: route needs a port; add port or ports to the component
```

Commands for exposing apps (`shelf init expose`) and for the Mac mini (`shelf init host`)
follow in later phases.

## Developing shelf

All development happens in a [devcontainer](https://containers.dev). On your machine you only
need Docker Desktop and VS Code with the Dev Containers extension. Every tool version is pinned
in [.devcontainer/Dockerfile](.devcontainer/Dockerfile).

The devcontainer runs its own Docker daemon (Docker-in-Docker, which makes it a privileged
container), and the local Kubernetes cluster runs inside it. Your other Docker containers stay
out of reach.

1. Open the repository in VS Code and choose **Reopen in Container**.
2. Run the checks, the same ones CI runs:

   ```sh
   just test
   ```

Useful commands:

| Command | Purpose |
|---|---|
| `just test` | Formatting, `go vet`, unit tests, chart lint and golden files, manifest validation, examples |
| `just golden` | Rewrite golden files and `schema/app.schema.json` after an intended change |
| `just build` | Build `bin/shelf` |
| `just cluster-up` / `cluster-stop` / `cluster-down` / `cluster-reset` | Manage the local k3d cluster `shelf-dev` |
| `just platform-push`, `just chart-push`, `just init-cluster` | Push `platform/` and the chart to the local registry and install them with `shelf init cluster` |
| `just smoke-secrets`, `just smoke-chart`, `just smoke-init`, `just smoke-apps` | Smoke tests against the local cluster, also run in CI: secret expansion, the chart, `shelf init cluster` with routing through Traefik, and `shelf app add`/`rm` with rollouts from the local registry |
| `just smoke-registry <image>`, `just smoke-tenant <app> <artifact>` | Smoke tests that need a GHCR login: a private image pull, and a real tenant repository end to end |
| `hack/nuke.sh` | **Run in a terminal on your machine, not in the container.** Removes every Docker object shelf created. |

On your machine's Docker daemon, shelf only creates the devcontainer with its image and
volumes, as listed in [docs/plan.md](docs/plan.md#footprint-on-the-existing-docker-desktop-setup).
`hack/nuke.sh` removes exactly those, by name. The cluster disappears with them.

Repository layout:

```text
LICENSE, NOTICE     Apache-2.0, and what shelf redistributes
cmd/shelf/          CLI entry point
internal/           schema, validation, rendering, cluster installation, CLI
charts/shelf-app/   the generic Helm chart every app is installed with
platform/           what Flux installs into every cluster (Traefik, the app ResourceSet)
schema/             generated JSON Schema for app.yaml
examples/hello/     reference app
examples/tenant/    example tenant repository (app.yaml, image, workflow)
.github/workflows/  CI, and build.yml, the reusable workflow for tenants
hack/               dev cluster, platform push, smoke tests, cleanup
docs/plan.md        design, decisions, phase plan
```

## Further reading

- [docs/plan.md](docs/plan.md): architecture, decisions, the schema in detail, and the phase
  plan
- [CLAUDE.md](CLAUDE.md): working agreements for AI-assisted development in this repository

## License

[Apache License 2.0](LICENSE). shelf redistributes the installation manifest of the
[Flux Operator](https://github.com/controlplaneio-fluxcd/flux-operator) (Apache-2.0); see
[NOTICE](NOTICE).
