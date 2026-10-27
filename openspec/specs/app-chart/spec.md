# app-chart Specification

## Purpose
The Helm chart `charts/shelf-app` (version 0.1.0) turns one resolved app — the output of
`shelf render -o app` plus a `platform` block set by the platform — into the Kubernetes objects of
that app in its own namespace. It is generic: every component becomes a workload, an optional
Service and an optional route, and the chart never knows what a component does. It runs inside the
cluster through a Flux `HelmRelease`; its behaviour is pinned by the golden files in
`internal/chart/testdata`.

## Requirements

### Requirement: Values contract
The chart SHALL take as values a resolved app.yaml (`apiVersion`, `name`, `components`, `secrets`)
and a `platform` block with `domain`, `hostSuffix`, `ingressClassName` (default `traefik`) and
`imagePullSecret` (default empty), where the `name` passed by the HelmRelease is the app's name and
wins over the package name in the artifact's app.yaml. Secret values MUST never be part of the
values.

#### Scenario: App name overrides the package name
- **WHEN** the artifact's app.yaml names the package `hello` and the release passes `name: hello-copy` into namespace `hello-copy`
- **THEN** every route uses the host `hello-copy.<domain>`
- **AND** no object uses the host `hello.<domain>`

### Requirement: Early failure on invalid values
The chart MUST fail rendering, without producing objects, when `apiVersion` is not
`shelf.dev/v1alpha1`, when `name` is empty, when the release namespace differs from `name`, when
`components` is empty, when a component has a route but `platform.domain` is empty, or when a
route uses `stripPrefix` and the cluster lacks the API `traefik.io/v1alpha1/Middleware`.

#### Scenario: Wrong namespace
- **WHEN** the app `hello` is rendered into the namespace `default`
- **THEN** rendering fails with `app "hello" must be installed into namespace "hello"`

#### Scenario: Route without a domain
- **WHEN** a component has a route and `platform.domain` is `""`
- **THEN** rendering fails with a message that `platform.domain is not set`

#### Scenario: stripPrefix without Traefik
- **WHEN** a route sets `stripPrefix: true` and the Middleware CRD is not available
- **THEN** rendering fails with a message that it `needs the Traefik Middleware CRD`

### Requirement: Labels
The chart SHALL label every object with `shelf.dev/app: <app>`, `shelf.dev/component: <component>`,
`app.kubernetes.io/managed-by` and `helm.sh/chart: shelf-app-<version>`, and SHALL use only
`shelf.dev/app` and `shelf.dev/component` as selector and pod template labels, so that a chart
upgrade alone restarts no pod.

#### Scenario: Chart version with a Flux digest
- **WHEN** Flux sets the chart version to `0.1.0+<digest>`
- **THEN** the label `helm.sh/chart` holds `shelf-app-0.1.0_<digest>` with `+` replaced by `_`, truncated to 63 characters

#### Scenario: Pod template labels
- **WHEN** any workload is rendered
- **THEN** its selector and pod template carry exactly `shelf.dev/app` and `shelf.dev/component`

### Requirement: One workload per component
The chart SHALL render one workload per component, named after the component with one container of
the same name: a `StatefulSet` when the component has volumes, otherwise a `Deployment`. Each
workload MUST have `replicas` equal to `instances` (default 1) and `revisionHistoryLimit: 3`, and
its pods MUST set `enableServiceLinks: false` and `automountServiceAccountToken: false`.

#### Scenario: Component without volumes
- **WHEN** the component `api` has `instances: 2` and no volumes
- **THEN** a `Deployment` named `api` with `replicas: 2` is rendered

#### Scenario: Component with volumes
- **WHEN** the component `db` has volumes
- **THEN** a `StatefulSet` named `db` is rendered with `serviceName: db-headless` and `podManagementPolicy: Parallel`

### Requirement: Image as resolved
The chart SHALL use the component's `image` exactly as given in the values, which the renderer has
already pinned to a digest, and SHALL list the platform's `imagePullSecret` under
`imagePullSecrets` only when it is set.

#### Scenario: Pull secret set
- **WHEN** `platform.imagePullSecret` is `shelf-registry`
- **THEN** every pod spec has `imagePullSecrets` with the single name `shelf-registry`

#### Scenario: No pull secret
- **WHEN** `platform.imagePullSecret` is empty
- **THEN** the pod specs have no `imagePullSecrets`

### Requirement: Volumes
The chart SHALL turn every volume of a component into a `volumeClaimTemplate` named after the
volume with `accessModes: [ReadWriteOnce]`, the volume's `size` as storage request and the
component's selector labels, mounted at the volume's `path`. The StatefulSet MUST set
`persistentVolumeClaimRetentionPolicy` to `whenScaled: Retain` and `whenDeleted: Delete`.

#### Scenario: Data volume
- **WHEN** the component `db` declares the volume `data` at `/var/lib/postgresql/data` with size `5Gi`
- **THEN** the StatefulSet `db` has a claim template `data` requesting `5Gi` mounted at `/var/lib/postgresql/data`

### Requirement: Services
The chart SHALL render, for every component with ports, a `ClusterIP` Service named after the
component with one TCP port per named port, `targetPort` set to the port's name, whether the
component runs as a Deployment or a StatefulSet. A component with volumes MUST also get a headless
Service `<component>-headless` (`clusterIP: None`), with the same ports if it has any. The chart MUST
never render a Service of type `LoadBalancer`.

#### Scenario: Single port
- **WHEN** a component declares `port: 5432`
- **THEN** its Service and container port are named `main` with port `5432`

#### Scenario: StatefulSet without ports
- **WHEN** the component `worker` has volumes and no ports
- **THEN** only the headless Service `worker-headless` without ports is rendered for it

### Requirement: Secret injection
The chart SHALL give a container the variable `SHELF_SECRET_<NAME>` (the secret name upper-cased
with `-` replaced by `_`) through a `secretKeyRef` to the key `<name>` of the Secret `shelf-secrets`
for each secret it references as `${secrets.<name>}` in `command`, `args` or `env`, once per secret,
before all other env entries. A secret that is only referenced in escaped form (`$${secrets.<name>}`)
or not at all MUST NOT be injected.

#### Scenario: Referenced secrets
- **WHEN** a component references `api-token` in `command` and `session-key` in `env`
- **THEN** its env starts with `SHELF_SECRET_API_TOKEN` and `SHELF_SECRET_SESSION_KEY`, each from `shelf-secrets`, each defined once

#### Scenario: Escaped reference
- **WHEN** a component's only mention of `unused` is `--literal=$${secrets.unused}`
- **THEN** the container has no `SHELF_SECRET_UNUSED`

### Requirement: Reference expansion
The chart SHALL rewrite every unescaped `${secrets.<name>}` in `command`, `args` and `env` values to
`$(SHELF_SECRET_<NAME>)` so that kubelet expands it, and SHALL leave every literal `$`, which the
values carry escaped as `$$`, as `$$`. Secret values MUST never appear in the rendered objects.

#### Scenario: Secret in a connection string
- **WHEN** an env value is `postgres://app:${secrets.db-password}@db:5432/hello`
- **THEN** the rendered value is `postgres://app:$(SHELF_SECRET_DB_PASSWORD)@db:5432/hello`

#### Scenario: Literal dollar
- **WHEN** an env value in app.yaml is `$HOME`
- **THEN** the rendered value is `$$HOME`

### Requirement: Health probes
The chart SHALL render, for a component with `health`, an HTTP readiness probe with
`failureThreshold: 3` and an HTTP liveness probe with `failureThreshold: 6`, both on the health
`path` and `port` with `periodSeconds: 10` and `initialDelaySeconds` only when `initialDelay` is
set. A component without `health` MUST get no probes.

#### Scenario: Probe with delay
- **WHEN** a component has `health: { path: /healthz, port: http, initialDelay: 15 }`
- **THEN** both probes request `/healthz` on port `http` with `initialDelaySeconds: 15`

### Requirement: Resources
The chart SHALL request `cpu` and `memory` from the component's `resources`, defaulting to `50m`
and `64Mi`, and SHALL set a memory limit equal to `resources.memory` only when it is given; it MUST
never set a CPU limit.

#### Scenario: CPU only
- **WHEN** a component sets `resources: { cpu: 250m }`
- **THEN** it requests `250m` CPU and `64Mi` memory and has no limits

#### Scenario: Memory given
- **WHEN** a component sets `resources.memory: 64Mi`
- **THEN** it requests and is limited to `64Mi` memory

### Requirement: Routes as Ingresses
The chart SHALL render, for every component with a `route`, one `networking.k8s.io/v1` Ingress
named after the component with `ingressClassName` from the platform (default `traefik`), a single
rule for the app's host, and a `Prefix` path on the route's `path` to the component's Service and
the named `port`. Components without a route MUST get no Ingress.

#### Scenario: Routed component
- **WHEN** the component `web` of app `hello` has a route on `/` and the domain is `dev.local`
- **THEN** an Ingress `web` routes host `hello.dev.local`, path `/` (Prefix) to Service `web`

### Requirement: Host name
The chart SHALL use the host `<app><hostSuffix>.<domain>` for every Ingress of an app, treating an
empty or null `hostSuffix` as no suffix.

#### Scenario: Host suffix set
- **WHEN** the app `hello` has the domain `example.com` and `hostSuffix: -dev`
- **THEN** the Ingress host is `hello-dev.example.com`

#### Scenario: Null host suffix
- **WHEN** `platform.hostSuffix` arrives as null
- **THEN** the Ingress host is `hello.dev.local` with no `<nil>` in it

### Requirement: stripPrefix
The chart SHALL render, for a route with `stripPrefix: true`, a Traefik `Middleware`
(`traefik.io/v1alpha1`) named after the component that strips the route's `path`, and SHALL attach
it to that component's Ingress only, through the annotation
`traefik.ingress.kubernetes.io/router.middlewares: <namespace>-<component>@kubernetescrd`.

#### Scenario: API under a prefix
- **WHEN** the component `api` of app `features` routes `/api` with `stripPrefix: true`
- **THEN** a Middleware `api` strips the prefix `/api`
- **AND** the Ingress `api` carries `traefik.ingress.kubernetes.io/router.middlewares: features-api@kubernetescrd`
