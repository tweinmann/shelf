# Spec Delta

## MODIFIED Requirements

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
