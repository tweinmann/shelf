# Spec Delta

## MODIFIED Requirements

### Requirement: Serve command and listener
The system SHALL provide `shelf serve`, which listens on `:7654` by default (overridable with
`--listen`), without TLS, prints the address of the UI, the cluster context and the state
directory, shuts down gracefully when its context ends (Ctrl-C or SIGTERM), and reads the
kubeconfig again before every operation, keeping the one it started with while the file cannot be
read.

#### Scenario: Default start
- **WHEN** `shelf serve` is started without `--listen`
- **THEN** it listens on port 7654 on every address of the machine
- **AND** it prints `The admin UI is at http://localhost:7654/`, the cluster context and the
  state directory

#### Scenario: Health check
- **WHEN** `GET /healthz` is requested, with or without a session
- **THEN** the server answers 200 with the plain-text body `ok`

#### Scenario: Cluster recreated under a running server
- **WHEN** the cluster is recreated with a new certificate authority and its kubeconfig rewritten
  while `shelf serve` runs
- **THEN** the next page reads the cluster through the new kubeconfig, without a restart

### Requirement: App page
The system SHALL show at `/apps/<name>` the app's phase, the first stage that is not ready with
its message and hint, its address, artifact, revision and deploy time, its components each with
its phase and address (routed ones as links, the others marked `inside the cluster only` or `not
exposed`), its access settings, the names of its generated secrets, its verification as `shelf
app status` prints it, and the diagnosis chain under `How it gets here`.

#### Scenario: Routed and internal components
- **WHEN** an app has a routed `web` component and an unrouted `db` component
- **THEN** `web` is shown with a link to its URL and `db` with its address and `inside the
  cluster only`

#### Scenario: Unknown app
- **WHEN** `/apps/<name>` is requested for an app that does not exist or a name that is not a
  valid app name
- **THEN** an error page with status 404 is shown

#### Scenario: Unverified app
- **WHEN** the running artifact of an app is unsigned
- **THEN** the page says it is not verified and why, and that it cannot be exposed
