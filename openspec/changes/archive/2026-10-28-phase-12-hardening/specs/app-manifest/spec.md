# Spec Delta

## ADDED Requirements

### Requirement: Tests section
The system SHALL accept an optional top-level `tests` map; each test name MUST be a DNS-1035 label
of at most 40 characters that is not also a component name, and each test MUST have exactly one of
`image` or `build` and MAY have `command`, `args`, `env` and `resources`, with the same rules and
references as a component; a test MUST NOT have ports, a route, volumes, health or instances.

#### Scenario: Test against the web component
- **WHEN** an app.yaml has `tests: { smoke: { image: curlimages/curl:8.10.1, args: [-fsS, "http://${web.host}:${web.port}/healthz"] } }`
- **THEN** validation passes, and `shelf render` pins the test image to its digest like a component image

#### Scenario: Name taken by a component
- **WHEN** a test and a component are both named `web`
- **THEN** validation reports an error at `tests.web`

#### Scenario: Test with a route
- **WHEN** a test declares `route: /`
- **THEN** parsing fails, as `route` is not a field of a test

## MODIFIED Requirements

### Requirement: Document structure and strict parsing
The system SHALL accept an app.yaml only as a single YAML document with the top-level fields
`apiVersion`, `name`, `components` and optional `secrets` and `tests`, and SHALL reject unknown
fields, duplicate keys, wrong types, empty files and multiple documents as parse errors.

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

### Requirement: Built components and --image
The system SHALL take the image of every component and test with `build` from a repeatable flag
`--image <name>=<reference>` and SHALL fail when a built component or test has no `--image`, when
`--image` names an unknown component or test or one with `image`, when a name is given twice, or
when the flag is not of the form `<name>=<reference>`.

#### Scenario: Built image passed back
- **WHEN** `shelf render --image web=ghcr.io/owner/greeter/web:sha-abc1234` is run for `examples/tenant/app.yaml`
- **THEN** the resolved `web` image is `ghcr.io/owner/greeter/web:sha-abc1234@sha256:...`

#### Scenario: Missing --image
- **WHEN** a component has `build: ./web` and no `--image web=...` is given
- **THEN** `shelf render` fails with an error telling the user to pass `--image web=<reference>`

#### Scenario: Built test
- **WHEN** a test `e2e` has `build: ./e2e` and `--image e2e=ghcr.io/owner/greeter/e2e:sha-abc1234` is given
- **THEN** the resolved test `e2e` has that image pinned to its digest

### Requirement: shelf build-plan
The system SHALL provide `shelf build-plan <app.yaml>` that prints JSON
`{"package": <name>, "builds": [{"component", "context"}]}` listing every component and test with
`build`, components first, each in name order, with each context relative to the working
directory, an empty list when nothing is built, and an error when a build directory does not
exist.

#### Scenario: Tenant example
- **WHEN** `shelf build-plan examples/tenant/app.yaml` is run
- **THEN** it prints `package` `greeter` and one build with `component` `web` and `context` `examples/tenant/web`

#### Scenario: Missing build directory
- **WHEN** a component's `build` directory does not exist
- **THEN** the command fails with an error naming the component and the directory

#### Scenario: Built test
- **WHEN** an app.yaml has a built component `web` and a built test `e2e`
- **THEN** the plan lists `web`, then `e2e`, so the workflow builds and pushes `ghcr.io/<owner>/<package>/e2e`
