# admin-ui Specification

## Purpose
`shelf serve` is the admin UI: an HTTP server with server-rendered pages embedded in the binary
that shows the apps of one cluster, explains which step of an app's chain is broken, and makes
changes to apps and connections from the browser. It is a second face on the same operations as
the CLI, protected by a one-time setup code, a single admin password and server-side sessions
stored under `~/.shelf`.

## Requirements

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

### Requirement: Claim with a setup code
The system SHALL generate a random setup code on the first start of an unclaimed instance, store
it in `~/.shelf/admin.yaml` so that it survives restarts until used, print it on every start
while the instance is unclaimed, and redirect every guarded route to `/setup` until the instance
is claimed.

#### Scenario: Unclaimed instance
- **WHEN** a browser requests `/` or any other guarded page before the instance is claimed
- **THEN** it is redirected (303) to `/setup`
- **AND** a request under `/api/` is answered with 401 instead of a redirect

#### Scenario: Claim with the printed code
- **WHEN** `POST /setup` carries the printed setup code and two equal passwords of at least 8
  characters
- **THEN** the password hash is stored, the setup code is removed, a session cookie is set, and
  the browser is redirected to `/`

#### Scenario: Wrong code or bad password
- **WHEN** `POST /setup` carries a wrong setup code, two different passwords, or a password
  shorter than 8 characters
- **THEN** the setup page is shown again with status 401 and the reason, keeping the typed code
- **AND** the instance stays unclaimed

### Requirement: Claim page after claiming
The system SHALL redirect `/setup` to `/login` once the instance is claimed, and redirect
`/login` to `/setup` while it is not.

#### Scenario: Setup page on a claimed instance
- **WHEN** `GET /setup` or `POST /setup` is requested on a claimed instance
- **THEN** the browser is redirected to `/login` and the password is not changed

### Requirement: Password storage
The system SHALL store the admin password only as a PBKDF2-SHA256 hash with 600000 iterations, a
16-byte random salt and the algorithm named in the stored string
(`pbkdf2-sha256$<iterations>$<salt>$<hash>`), in `~/.shelf/admin.yaml`, written atomically with
mode 0600 in a directory with mode 0700.

#### Scenario: Stored hash
- **WHEN** the instance is claimed
- **THEN** `~/.shelf/admin.yaml` holds a `password` value starting with `pbkdf2-sha256$600000$`
  and never the password itself
- **AND** the file has mode 0600 and `~/.shelf` has mode 0700

### Requirement: Password login with backoff
The system SHALL log in with the admin password at `POST /login`, open a session on success, and
slow down repeated failures from one client address with an exponentially growing wait capped at
5 minutes.

#### Scenario: Correct password
- **WHEN** `POST /login` carries the admin password
- **THEN** a session cookie is set and the browser is redirected to `/`

#### Scenario: Wrong password
- **WHEN** `POST /login` carries a wrong password
- **THEN** the login page is shown with status 401 and `wrong password`

#### Scenario: Too many attempts
- **WHEN** a client address tries again before its wait after a failed login has passed
- **THEN** the login page is shown with status 429 and `too many attempts; wait a moment and try
  again`, without checking the password

### Requirement: Server-side sessions
The system SHALL keep sessions in `~/.shelf/admin.yaml` and identify them by the cookie
`shelf_session` (HttpOnly, SameSite=Lax, Path `/`, Secure only when configured for HTTPS), with a
lifetime of 30 days that slides forward while the session is used, so that sessions survive a
restart of the server.

#### Scenario: Unauthenticated request
- **WHEN** a guarded page is requested on a claimed instance without a valid, unexpired session
- **THEN** the browser is redirected (303) to `/login`, and an `/api/` request gets 401

#### Scenario: Restart keeps the login
- **WHEN** the server is restarted while a session is open
- **THEN** the same cookie still opens the guarded pages

### Requirement: Logout
The system SHALL end the current session at `POST /logout` with a valid form token, removing it
from the stored sessions, clearing the cookie and redirecting to `/login`.

#### Scenario: Log out
- **WHEN** a logged-in user submits the `Log out` button in the header
- **THEN** the session is deleted from `~/.shelf/admin.yaml` and the browser is redirected to
  `/login`
- **AND** the old cookie no longer opens a guarded page

### Requirement: Request forgery protection
The system SHALL refuse state-changing requests sent from another site (judged by `Sec-Fetch-Site`
and `Origin`), and SHALL require every form of a logged-in page to carry a `csrf` token derived
from the session, refusing a mutating request without the matching token with status 403.

#### Scenario: Missing or stale form token
- **WHEN** a logged-in browser posts a change, a secret reveal or a logout without the session's
  `csrf` value
- **THEN** the server answers 403 with `this form is out of date; load the page again` and
  nothing is changed

#### Scenario: Cross-site post
- **WHEN** a `POST` arrives with `Sec-Fetch-Site: cross-site` or an `Origin` of another host
- **THEN** the request is refused before it reaches any route

### Requirement: Pages are not cached
The system SHALL send `Cache-Control: no-store` on every response, and SHALL bound every cluster
read of a page with a timeout of 8 seconds.

#### Scenario: Back button
- **WHEN** any page or API response is served
- **THEN** it carries `Cache-Control: no-store`

### Requirement: Dashboard
The system SHALL show at `/` the cluster's context and server, its host suffix, and every app
with its phase, its address (a link when it has a URL), its running revision shortened to 12 hex
characters with how long ago it was deployed, and, for an app that is not ready, the stage and
reason of the first broken step of its diagnosis.

#### Scenario: Broken app
- **WHEN** an app's deploy artifact cannot be pulled
- **THEN** the dashboard lists it as `Failed` with the line `<stage>: <reason>` naming the deploy
  artifact step

#### Scenario: Cluster does not answer
- **WHEN** the API server cannot be reached
- **THEN** the dashboard shows `The cluster does not answer` with the server and the error
  instead of the app list

#### Scenario: Platform not installed
- **WHEN** the cluster answers but shelf is not installed
- **THEN** the dashboard says `The platform is not installed` and points to `shelf init cluster`

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

### Requirement: Changes run as jobs
The system SHALL run every change made from the UI (add an app, deploy another artifact or tag,
deploy again, change an app's connections and domain, remove an app, save or remove a connection)
as a background job with a timeout of 5 minutes, answer the form with a 303 redirect to
`/jobs/<id>`, and keep the last 20 jobs in memory only.

#### Scenario: Add an app
- **WHEN** a valid `POST /apps` with name, artifact and connection choices arrives
- **THEN** the browser is redirected to `/jobs/<id>` for the job `Add <name>`

#### Scenario: Invalid form
- **WHEN** `POST /apps` carries an invalid app name or artifact reference
- **THEN** the add form is shown again with status 400, the reason and what was typed, and no job
  is started

### Requirement: One change at a time
The system SHALL run at most one job at a time across the whole server, and SHALL answer a
change requested while another runs with status 409 and a page linking to the running job.

#### Scenario: Second change while one runs
- **WHEN** a change is submitted while another job is running
- **THEN** the busy page `One at a time` is shown with status 409 and a link to `/jobs/<running
  id>`, and no second job is started
- **AND** the dashboard, app and connections pages show a notice linking to the running job

### Requirement: Live job log
The system SHALL render at `/jobs/<id>` the job's title, state (`running`, `done` or `failed`),
its log so far as the exact text the CLI prints for the same events, and its error, and SHALL
stream the remaining events from `/api/jobs/<id>/events` as server-sent events.

#### Scenario: Following a running job
- **WHEN** the job page of a running job is open
- **THEN** each new event arrives as an SSE `line` event with a numeric `id` and JSON data
  `{"text": ...}` and is appended to the log
- **AND** when the job ends an `end` event carries `{"error": ...}` (empty on success)

#### Scenario: Reload during a job
- **WHEN** the job page is reloaded, or the stream reconnects with `Last-Event-ID` or `?from=`
- **THEN** the log shows every event from the start with nothing missing or repeated

#### Scenario: Forgotten job
- **WHEN** `/jobs/<id>` names a job that is not kept any more, for example after a restart
- **THEN** a 404 error page says `that change is not in the list any more`

### Requirement: Audit log
The system SHALL append one line per change started from the UI and per secret reveal to
`~/.shelf/audit.log` (mode 0600), formatted `<RFC 3339 UTC time> <action>[ <detail>]`, and SHALL
report a failed audit write instead of hiding it.

#### Scenario: Change recorded
- **WHEN** a job `Redeploy greeter` is started
- **THEN** `audit.log` gains a line like `2026-09-20T10:00:00Z Redeploy greeter job <id>`
- **AND** if the line cannot be written, the job log says `the audit log could not be written`

#### Scenario: Reveal recorded
- **WHEN** the secret values of `greeter` are revealed
- **THEN** `audit.log` gains the line `<time> Reveal the secrets of greeter`

### Requirement: Deploy, redeploy and connection changes on the app page
The system SHALL offer on the app page a form that deploys the app from another artifact or tag
(`POST /apps/<name>/deploy`), a button that deploys the same tag again
(`POST /apps/<name>/redeploy`), and a form that changes the registry connection, the Cloudflare
connection or quick tunnel, and the domain (`POST /apps/<name>/access`), each running as a job.

#### Scenario: Roll back to a commit tag
- **WHEN** the deploy form is submitted with `oci://ghcr.io/owner/greeter:sha-905acce`
- **THEN** a job `Deploy <name> from oci://ghcr.io/owner/greeter:sha-905acce` points the app at
  that tag

#### Scenario: Take an app off the internet
- **WHEN** the access form is submitted with the Cloudflare choice `none`
- **THEN** a job `Change the connections of <name>` makes the app private; the choice `@quick`
  asks for a quick tunnel instead

### Requirement: Removing an app needs its name typed
The system SHALL remove an app with its namespace and volumes at `POST /apps/<name>/delete` only
when the field `confirm` equals the app's name.

#### Scenario: Name not typed
- **WHEN** the remove form is submitted with an empty or different `confirm` value
- **THEN** a 400 error page asks to type the name and nothing is removed

#### Scenario: Name typed
- **WHEN** `confirm` equals the app's name
- **THEN** a job `Remove <name>` removes the app

### Requirement: Secret values behind the password
The system SHALL show only the names of an app's generated secrets on the app page, with values
masked, and SHALL reveal the values only in the response to `POST /apps/<name>/secrets` carrying
the admin password again.

#### Scenario: Correct password
- **WHEN** the secrets form is submitted with the admin password
- **THEN** the app page is rendered with the secret values shown, and the reveal is audited

#### Scenario: Wrong password
- **WHEN** the secrets form is submitted with a wrong password
- **THEN** the app page is rendered with status 401, `wrong password` and the values still masked

### Requirement: Connections page
The system SHALL provide `/connections` as the only page with token fields: it lists the registry
and Cloudflare connections with the apps that use each, defines a connection or gives it a new
token (`POST /connections/registry`, `POST /connections/cloudflare`), and removes one
(`POST /connections/<kind>/<name>/delete`), each change running as a job; token fields are
password inputs that are never filled in, and a token is never shown again.

#### Scenario: Refused connection form
- **WHEN** a registry connection is submitted without a user name or token, or with an invalid name
- **THEN** the page is shown with status 400 and the reason next to that form, keeping the name
  and user name but not the token

#### Scenario: Unknown kind
- **WHEN** `POST /connections/<kind>/<name>/delete` names a kind other than `registry` or
  `cloudflare`
- **THEN** a 404 error page is shown

### Requirement: Selects with free-text fallback
The system SHALL let the add form and the app page offer the deploy artifacts and tags of the
chosen registry connection and the zones of the chosen Cloudflare connection as selects, fetched
from `/api/connections/registry/<name>/packages`, `/api/connections/registry/<name>/tags?artifact=`
and `/api/connections/cloudflare/<name>/zones`, while the plain text field remains what the form
sends and is shown whenever there is no connection, no JavaScript, or the list cannot be read.

#### Scenario: List available
- **WHEN** a registry connection is chosen and its packages can be read
- **THEN** the artifact field becomes a package select and a tag select, each with an `other …`
  entry that shows the text field again

#### Scenario: List cannot be read
- **WHEN** a choices endpoint fails
- **THEN** it answers 502 with `{"items": [], "error": "<reason>"}`, and the form shows the text
  field with the reason

### Requirement: Every UI action has a CLI command
The system SHALL make each action the UI offers available as a CLI command that calls the same
operation and prints the same progress lines: add and deploy another tag (`shelf app add`),
change connections and domain (`shelf app credentials`), redeploy (`shelf app redeploy`), remove
(`shelf app rm`), secrets (`shelf app secrets`), connections (`shelf connection add|list|rm`),
choices (`shelf connection packages|zones`, `shelf app tags`) and the diagnosis (`shelf app
status`).

#### Scenario: Same output in both faces
- **WHEN** an app is added from the browser and the same app is added with `shelf app add`
- **THEN** the job log in the browser and the CLI output consist of the same lines
