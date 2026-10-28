# Spec Delta

## MODIFIED Requirements

### Requirement: App page
The system SHALL show at `/apps/<name>` the app's phase, the first stage that is not ready with
its message and hint, its address, artifact, revision and deploy time, its components each with
its phase and address (routed ones as links, the others marked `inside the cluster only` or `not
exposed`), its access settings, the names of its generated secrets, its verification and its
isolation as `shelf app status` prints them, and the diagnosis chain with the tests under `How
it gets here`.

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

#### Scenario: Failing test
- **WHEN** the test `smoke` of an app failed
- **THEN** the page shows the tests stage as Failed with the test's name and the end of its log

### Requirement: Deploy, redeploy and connection changes on the app page
The system SHALL offer on the app page a form that deploys the app from another artifact or tag
(`POST /apps/<name>/deploy`), a button that deploys the same tag again
(`POST /apps/<name>/redeploy`), and a form that changes the registry connection, the Cloudflare
connection or quick tunnel, the domain and the LAN exceptions (`POST /apps/<name>/access`), each
running as a job.

#### Scenario: Roll back to a commit tag
- **WHEN** the deploy form is submitted with `oci://ghcr.io/owner/greeter:sha-905acce`
- **THEN** a job `Deploy <name> from oci://ghcr.io/owner/greeter:sha-905acce` points the app at
  that tag

#### Scenario: Take an app off the internet
- **WHEN** the access form is submitted with the Cloudflare choice `none`
- **THEN** a job `Change the connections of <name>` makes the app private; the choice `@quick`
  asks for a quick tunnel instead

#### Scenario: LAN exception from the browser
- **WHEN** the access form is submitted with the LAN exceptions `192.168.1.10/32`
- **THEN** the job gives the app that exception, as `shelf app credentials --allow-lan 192.168.1.10/32` does; an empty field takes the exceptions away, and an invalid range is answered with status 400 and the reason, without a job
