# app-manifest Specification

## Purpose
Defines the `app.yaml` format (`apiVersion: shelf.dev/v1alpha1`) that a tenant repository uses
to describe its app as a set of components, and the CLI commands that work on it: `shelf
validate`, `shelf render` (including `--image`), `shelf schema` and `shelf build-plan`. The
resolved app.yaml produced by `shelf render` is the handover contract between the tenant's CI
and the platform, carried in the deploy artifact as the ConfigMap `shelf-values`.

## Requirements

### Requirement: Document structure and strict parsing
The system SHALL accept an app.yaml only as a single YAML document with the top-level fields
`apiVersion`, `name`, `components` and optional `secrets`, and SHALL reject unknown fields,
duplicate keys, wrong types, empty files and multiple documents as parse errors.

#### Scenario: Unknown field
- **WHEN** an app.yaml contains a field that is not part of the schema, at any level including inside a long-form `route`
- **THEN** parsing fails with an error that names the file
- **AND** no findings are produced for the document

#### Scenario: Scalar env values
- **WHEN** a component sets `env: { PORT: 8080, DEBUG: true }`
- **THEN** the values are taken as the text `8080` and `true`

#### Scenario: Wrong apiVersion
- **WHEN** `apiVersion` is anything other than `shelf.dev/v1alpha1`
- **THEN** validation reports an error at `apiVersion`

### Requirement: Package name
The system SHALL treat `name` as the package name only: it MUST be present, at most 40 characters
and a DNS-1123 label (lowercase letters, digits and `-`, starting and ending with a letter or
digit); the app's name in shelf is not part of app.yaml.

#### Scenario: Invalid package name
- **WHEN** `name` is empty, longer than 40 characters or contains an uppercase letter or `_`
- **THEN** validation reports an error at `name`

### Requirement: Component names and image source
The system SHALL require at least one component; each component name MUST be a DNS-1035 label of
at most 40 characters, MUST NOT be `secrets`, `app` or `shelf`, and MUST NOT end with
`-headless`; each component MUST have exactly one of `image` (a valid image reference) or
`build` (a relative directory inside the repository).

#### Scenario: Neither or both image sources
- **WHEN** a component has neither `image` nor `build`, or has both
- **THEN** validation reports an error for that component

#### Scenario: Build directory leaves the repository
- **WHEN** `build` is absolute, is `.` or `..`, starts with `../`, or contains characters outside `[A-Za-z0-9._-]` and `/`
- **THEN** validation reports an error at `components.<name>.build`

#### Scenario: Reserved component name
- **WHEN** a component is named `shelf` or `db-headless`
- **THEN** validation reports an error for that component name

### Requirement: Ports
The system SHALL accept either a single `port` or a map `ports` of name to number on a component,
never both; port numbers MUST be between 1 and 65535, port names MUST be valid Kubernetes port
names, a `ports` map MUST NOT be empty, and two ports of a component MUST NOT share a number.

#### Scenario: port and ports together
- **WHEN** a component sets both `port` and `ports`
- **THEN** validation reports an error at `components.<name>.ports`

#### Scenario: Duplicate port number
- **WHEN** two entries of `ports` use the same number
- **THEN** validation reports an error naming the port that already uses it

### Requirement: Routes
The system SHALL accept `route` as a path string or as `{ path, port, stripPrefix }`; the path
MUST start with `/` and MUST NOT contain spaces, `?` or `#`; a route MUST belong to a component
with a port, MUST name a port when the component has several, MUST NOT name a port when the
component has a single `port`, and no two components MAY use the same route path, where a
trailing `/` is ignored.

#### Scenario: Route without port
- **WHEN** a component has a `route` but neither `port` nor `ports`
- **THEN** validation reports an error that the route needs a port

#### Scenario: Several ports without route port
- **WHEN** a component has two named ports and `route: /api`
- **THEN** validation reports an error listing the port names to choose from

#### Scenario: Colliding routes
- **WHEN** one component routes `/api` and another `/api/`
- **THEN** validation reports an error that the path is already used by the other component

### Requirement: Volumes, health, resources and instances
The system SHALL validate the remaining component fields: each volume name MUST be a DNS-1123
label of at most 40 characters with an absolute, clean `path` other than `/` and a positive
`size` quantity, and mount paths of one component MUST NOT be equal or nested; `health.path` MUST
be a valid HTTP path and `health.port` follows the same port rules as a route; `initialDelay`
MUST NOT be negative; `resources.cpu` and `resources.memory` MUST be positive quantities;
`instances` MUST be at least 1.

#### Scenario: Nested mount paths
- **WHEN** a component mounts one volume at `/data` and another at `/data/cache`
- **THEN** validation reports an overlap error at the second volume's `path`

#### Scenario: Malformed quantity
- **WHEN** `resources.memory` is `lots` or a volume `size` is `0`
- **THEN** validation reports an error at that field

### Requirement: Environment variable names
The system SHALL require every `env` key to be a C identifier (`[A-Za-z_][A-Za-z0-9_]*`) and
SHALL reject keys with the reserved prefix `SHELF_SECRET_`.

#### Scenario: Reserved prefix
- **WHEN** a component sets `env: { SHELF_SECRET_X: foo }`
- **THEN** validation reports an error telling the author to use `${secrets.<name>}` instead

### Requirement: References in env, command and args
The system SHALL interpret `${<component>.host}`, `${<component>.port}`,
`${<component>.ports.<name>}` and `${secrets.<name>}` in `env` values, `command` and `args`,
treat `$$` and any other lone `$` as a literal `$`, and report every other `${...}`, an
unterminated `${`, or a reference to an unknown component, port or secret as an error.

#### Scenario: Unknown reference form
- **WHEN** an env value contains `${db.address}`
- **THEN** validation reports an error listing the supported reference forms

#### Scenario: Single-port reference on a multi-port component
- **WHEN** a value references `${api.port}` and `api` declares several `ports`
- **THEN** validation reports an error suggesting `${api.ports.<name>}`

#### Scenario: Reference to a component without ports
- **WHEN** a value references `${check.host}` and `check` has no port
- **THEN** validation reports an error that the component has no host name

### Requirement: Secrets declaration
The system SHALL accept top-level `secrets` as a map of name to `{ generate: true }`, where a
secret name MUST be at most 63 characters of lowercase letters, digits and `-` (no `_`), SHALL
reject secrets that are not generated, and SHALL warn about secrets no component references.

#### Scenario: Non-generated secret
- **WHEN** a secret is declared as `{ generate: false }`
- **THEN** validation reports an error that only generated secrets are supported

#### Scenario: Unreferenced secret
- **WHEN** a declared secret appears in no `${secrets.<name>}` reference
- **THEN** validation reports a warning, not an error

### Requirement: Generated secret values
The system SHALL generate secret values as 26 characters from `A-Z` and `2-7` (130 bits of
randomness), so that they are URL-safe and need no escaping in connection URIs, shells or YAML.

#### Scenario: Value fits into a connection URI
- **WHEN** a secret value is generated
- **THEN** it contains none of `@ : / ? # [ ]` and no `$`

### Requirement: shelf validate
The system SHALL provide `shelf validate <app.yaml>...` that checks one or more files without
contacting a registry, prints each finding to stderr as `<file>:<line>: <error|warning>: <path>:
<message>` sorted by line, prints a summary per file to stdout (`valid`, `valid (N warnings)` or
`invalid (N errors, M warnings)`), and exits with code 1 if any file fails to parse or has an
error, 0 otherwise.

#### Scenario: Valid example
- **WHEN** `shelf validate examples/hello/app.yaml` is run
- **THEN** stdout reads `examples/hello/app.yaml: valid`
- **AND** the exit code is 0

#### Scenario: Warnings only
- **WHEN** a file has an unreferenced secret and no errors
- **THEN** the warning is printed to stderr, the summary reads `valid (1 warning)`, and the exit code is 0

#### Scenario: Errors in one of several files
- **WHEN** two files are given and one has errors
- **THEN** both files are checked and summarized, and the exit code is 1

### Requirement: shelf render resolves the app.yaml
The system SHALL provide `shelf render <app.yaml>` that validates the file, refuses to render on
any validation error (exit code 1), and otherwise produces the resolved app.yaml: every image
pinned by digest while keeping its tag (`postgres:16@sha256:...`, an existing digest replaced),
host references replaced by the component name, port references replaced by the number, every
literal `$` written as `$$`, and `${secrets.<name>}` left in place.

#### Scenario: Literal dollar and references
- **WHEN** `check` has `args: ["psql \"$DATABASE_URL\""]` and `env: { DATABASE_URL: postgres://app:${secrets.db-password}@${db.host}:${db.port}/hello }` with `db` on port 5432
- **THEN** the resolved arg is `psql "$$DATABASE_URL"`
- **AND** the resolved env value is `postgres://app:${secrets.db-password}@db:5432/hello`

#### Scenario: Validation errors block rendering
- **WHEN** the app.yaml has a validation error
- **THEN** the findings are printed to stderr, nothing is written to stdout, and the exit code is 1

### Requirement: Normalized shape of the resolved app.yaml
The system SHALL normalize the resolved app.yaml so that the chart needs no case analysis:
`ports` is always a map (a single `port` becomes `ports.main`), `route.port` and `health.port`
are always set, `instances` is always set (default 1), and
`build` is replaced by the pinned `image`.

#### Scenario: Single port
- **WHEN** a component has `port: 80`, `route: /` and `health: { path: /health }`
- **THEN** the resolved component has `ports: { main: 80 }`, `route.port: main`, `health.port: main` and `instances: 1`

### Requirement: Image lookup during render
The system SHALL look up every image in its registry during `shelf render`, with credentials from
the Docker config, SHALL fail if an image has no `linux/arm64` variant, SHALL pin a
multi-platform image to its index digest, and SHALL warn when a declared port is not in the
image's TCP `EXPOSE` list (images without any `EXPOSE` are not checked).

#### Scenario: Image without arm64
- **WHEN** an image only provides `linux/amd64`
- **THEN** `shelf render` fails with an error naming the component and the missing `linux/arm64` variant

#### Scenario: Port not exposed
- **WHEN** a component declares port 8080 and its image exposes only 80
- **THEN** a warning `port 8080 is not exposed by image ... (EXPOSE 80)` is printed and rendering continues

### Requirement: Built components and --image
The system SHALL take the image of every component with `build` from a repeatable flag
`--image <component>=<reference>` and SHALL fail when a built component has no `--image`, when
`--image` names an unknown component or a component with `image`, when a component is given
twice, or when the flag is not of the form `<component>=<reference>`.

#### Scenario: Built image passed back
- **WHEN** `shelf render --image web=ghcr.io/owner/greeter/web:sha-abc1234` is run for `examples/tenant/app.yaml`
- **THEN** the resolved `web` image is `ghcr.io/owner/greeter/web:sha-abc1234@sha256:...`

#### Scenario: Missing --image
- **WHEN** a component has `build: ./web` and no `--image web=...` is given
- **THEN** `shelf render` fails with an error telling the user to pass `--image web=<reference>`

### Requirement: Render output
The system SHALL write the render result to stdout and everything else to stderr: by default
(`--output configmap`) a ConfigMap `v1` named `shelf-values` without namespace or labels whose
key `app.yaml` holds the resolved app.yaml, with `--output app` the resolved app.yaml alone; on
stderr it prints the findings and a summary of the objects to be created. Secret values MUST
never appear in the output.

#### Scenario: Default output
- **WHEN** `shelf render examples/hello/app.yaml` succeeds
- **THEN** stdout holds a ConfigMap named `shelf-values` with the data key `app.yaml`
- **AND** stderr lists per component its Deployment or StatefulSet with instance count and image, its Service with ports, its Route and its PVC names `<volume>-<component>-<ordinal>`
- **AND** stderr lists `secret db-password (generated) → env SHELF_SECRET_DB_PASSWORD`

#### Scenario: Invalid output format
- **WHEN** `--output` is neither `configmap` nor `app`
- **THEN** the command fails with an error

### Requirement: Secret embedding in the cluster
The system SHALL deliver each secret referenced by a component as the environment variable
`SHELF_SECRET_<NAME>` (name uppercased, `-` replaced by `_`) from `secretKeyRef` on the Secret
`shelf-secrets`, key `<name>`, placed before all other env entries, and SHALL rewrite every
`${secrets.<name>}` to `$(SHELF_SECRET_<NAME>)`, so that no secret value appears in a manifest.

#### Scenario: Password in a connection string
- **WHEN** the resolved env value is `postgres://app:${secrets.db-password}@db:5432/hello`
- **THEN** the container gets `SHELF_SECRET_DB_PASSWORD` from `shelf-secrets` first
- **AND** `DATABASE_URL` is `postgres://app:$(SHELF_SECRET_DB_PASSWORD)@db:5432/hello`

### Requirement: shelf build-plan
The system SHALL provide `shelf build-plan <app.yaml>` that prints JSON
`{"package": <name>, "builds": [{"component", "context"}]}` listing every component with `build`
in component name order, with each context relative to the working directory, an empty list when
nothing is built, and an error when a build directory does not exist.

#### Scenario: Tenant example
- **WHEN** `shelf build-plan examples/tenant/app.yaml` is run
- **THEN** it prints `package` `greeter` and one build with `component` `web` and `context` `examples/tenant/web`

#### Scenario: Missing build directory
- **WHEN** a component's `build` directory does not exist
- **THEN** the command fails with an error naming the component and the directory

### Requirement: shelf schema
The system SHALL provide `shelf schema` that prints the JSON Schema (draft 2020-12) for app.yaml,
generated from the same definitions as the parser and committed as `schema/app.schema.json`,
including the name patterns, the exclusive choice of `image` or `build`, and the exclusion of
`port` together with `ports`.

#### Scenario: Editor support
- **WHEN** `shelf schema` is run
- **THEN** it prints a JSON Schema with `$id` `https://raw.githubusercontent.com/tweinmann/shelf/main/schema/app.schema.json`
- **AND** the schema accepts every app.yaml under `examples/`
