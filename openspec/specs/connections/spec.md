# connections Specification

## Purpose
Connections replace platform-wide credentials: a registry login for `ghcr.io` or a Cloudflare
API token is defined once, by name, and assigned to any number of apps. Registry connections live
in the cluster, which pulls with them; Cloudflare connections live only on the machine running
shelf, under `~/.shelf`. The CLI (`shelf connection ...`) and the admin UI page `/connections`
manage them through the same operations, and both can list what a connection reaches (packages,
tags, zones) so that forms offer real choices.

## Requirements

### Requirement: Connection names
The system SHALL accept as a connection name only a DNS label of at most 40 characters, and SHALL refuse any other name before anything is written or deleted.

#### Scenario: Invalid name refused
- **WHEN** `shelf connection add registry Bad_Name` is run
- **THEN** the command fails with a message that the connection name must be a DNS label of at most 40 characters
- **AND** no Secret is written

### Requirement: Registry connection storage
The system SHALL store a registry connection as the Secret `shelf-system/connection-registry-<name>` of type `kubernetes.io/dockerconfigjson`, labelled `shelf.dev/connection=registry`, holding a login for `ghcr.io` only, and SHALL keep a Secret `shelf-system/registry-anonymous` with no login for apps without a registry connection.

#### Scenario: Registry connection saved
- **WHEN** a registry connection `ghcr` is saved with a user name and a token
- **THEN** the Secret `shelf-system/connection-registry-ghcr` is applied with the label `shelf.dev/connection=registry`
- **AND** the progress output names the Secret and `for <user>@ghcr.io`, never the token

#### Scenario: Saving under an existing name
- **WHEN** a registry connection is saved under a name that already exists and apps use it
- **THEN** the Secret is replaced with the new login
- **AND** the output says that those apps pull with it from now on

### Requirement: Registry login copied only to apps that chose it
The system SHALL copy a registry connection into the namespace of an app only when that app chose the connection, and SHALL copy `shelf-system/registry-anonymous` into the namespace of every app without a registry connection.

#### Scenario: App with a registry connection
- **WHEN** an app is added with `--registry ghcr`
- **THEN** its namespace receives a copy of `shelf-system/connection-registry-ghcr`
- **AND** no other app's namespace receives it unless that app also chose `ghcr`

#### Scenario: App without a registry connection
- **WHEN** an app is added without `--registry`
- **THEN** its namespace receives a copy of `shelf-system/registry-anonymous` and the registry is read anonymously

### Requirement: Cloudflare connection storage on the host
The system SHALL store a Cloudflare connection only on the machine running shelf, as the file `$SHELF_HOME/connections/cloudflare/<name>.yaml` (default `~/.shelf`) holding name, token and account, and SHALL never write a Cloudflare token into the cluster.

#### Scenario: Cloudflare connection saved
- **WHEN** a Cloudflare connection `cf` is saved
- **THEN** the file `~/.shelf/connections/cloudflare/cf.yaml` holds the name `cf`, the token and the account
- **AND** no Secret or other cluster object receives the token

#### Scenario: Renamed file refused
- **WHEN** the file `cloudflare/<a>.yaml` holds a connection whose name is not `<a>`, or holds no token
- **THEN** reading the connection fails with an error naming the file instead of using it

### Requirement: Private, atomic host files
The system SHALL write connection files with mode 0600, SHALL make the directories `connections` and `connections/cloudflare` mode 0700 (also when they already exist), and SHALL replace a file atomically by writing a temporary file in the same directory and renaming it.

#### Scenario: Directory permissions repaired
- **WHEN** a Cloudflare connection is saved and `~/.shelf/connections` already exists with wider permissions
- **THEN** both connection directories end up with mode 0700 and the file with mode 0600

### Requirement: Tokens from the environment only on the command line
The system SHALL read connection credentials on the command line only from the environment variables `GHCR_USERNAME` and `GHCR_TOKEN` (registry) or `CF_API_TOKEN` and optionally `CF_ACCOUNT_ID` (Cloudflare), and SHALL offer no flag that takes a token.

#### Scenario: Missing registry variables
- **WHEN** `shelf connection add registry ghcr` is run without `GHCR_USERNAME` or `GHCR_TOKEN` set
- **THEN** the command fails with `a registry connection needs GHCR_USERNAME and GHCR_TOKEN`

#### Scenario: Missing Cloudflare token
- **WHEN** `shelf connection add cloudflare cf` is run without `CF_API_TOKEN` set
- **THEN** the command fails with `a Cloudflare connection needs CF_API_TOKEN`

### Requirement: Cloudflare token verified before it is kept
The system SHALL verify a Cloudflare token with the Cloudflare API before saving it, and SHALL determine the account from the token when no account is given, refusing when the token sees no account or several accounts.

#### Scenario: Refused token
- **WHEN** a Cloudflare connection is saved with a token Cloudflare rejects
- **THEN** the save fails with `the Cloudflare token was refused` and no file is written

#### Scenario: Several accounts without an account ID
- **WHEN** the token sees several accounts and `CF_ACCOUNT_ID` is not set
- **THEN** the save fails and the message lists the accounts and asks for `CF_ACCOUNT_ID`

### Requirement: Cloudflare connection cannot change account while in use
The system SHALL refuse to save an existing Cloudflare connection with a different account while any app uses it, naming those apps; with the same account, or without apps, it SHALL replace the token.

#### Scenario: Account change refused
- **WHEN** the Cloudflare connection `cf` in account A is used by app `shop` and is saved again with account B
- **THEN** the save fails, naming account A and `shop`, and advising a new connection for account B
- **AND** the stored file is unchanged

#### Scenario: New token, same account
- **WHEN** an existing Cloudflare connection is saved with a new token and the same account
- **THEN** the file is replaced and the output reports the connection as updated with its account

### Requirement: Listing connections
The system SHALL list registry connections of the cluster and Cloudflare connections of this machine with the apps that use each, showing the registry user name and the Cloudflare account but never a token, and SHALL list a Cloudflare connection that an app uses but this machine does not hold as missing.

#### Scenario: List output
- **WHEN** `shelf connection list` is run with a registry connection `ghcr` used by `shop` and an unused Cloudflare connection `cf`
- **THEN** it prints `registry ghcr <user>@ghcr.io used by shop` and `cloudflare cf account <id> unused`

#### Scenario: Connection not on this machine
- **WHEN** an app uses the Cloudflare connection `cf` and no file for `cf` exists on this machine
- **THEN** the list shows `cf` as `not on this machine` with the apps that use it

#### Scenario: Nothing defined
- **WHEN** there are no connections of either kind
- **THEN** the command prints `No connections yet.`

### Requirement: Removing connections
The system SHALL remove a connection with `shelf connection rm registry|cloudflare <name>` only when no app uses it, SHALL name the apps when it refuses, and SHALL fail when no connection of that kind and name exists.

#### Scenario: Connection in use
- **WHEN** `shelf connection rm registry ghcr` is run while apps `shop` and `blog` use `ghcr`
- **THEN** the command fails with a message naming `shop, blog` and nothing is deleted

#### Scenario: Unknown kind or name
- **WHEN** the kind is neither `registry` nor `cloudflare`, or no connection of that name exists
- **THEN** the command fails with an error and nothing is deleted

### Requirement: Choosing connections per app
The system SHALL let `shelf app add` and `shelf app credentials` choose connections by name with `--registry <name>` and `--cloudflare <name>`, SHALL let `shelf app credentials` drop the registry connection with `--no-registry` and the Cloudflare connection with `--no-cloudflare` (the same as `--private`), and SHALL read no token in these commands.

#### Scenario: Unknown registry connection
- **WHEN** `shelf app add shop <artifact> --registry nope` is run and no `connection-registry-nope` exists
- **THEN** the command fails before anything is written, suggesting `shelf connection add registry nope`

#### Scenario: Cloudflare connection not held by this machine
- **WHEN** an app is given `--cloudflare cf` and this machine holds no connection `cf`
- **THEN** the command fails before anything is written, suggesting `shelf connection add cloudflare cf`

#### Scenario: Cloudflare connection without a domain
- **WHEN** an app is given a Cloudflare connection but has no domain of its own
- **THEN** the command fails before anything is written, saying the app needs a domain of its own

### Requirement: Reading deploy artifacts with the app's registry login
The system SHALL read an app's deploy artifact from the machine running shelf with the login of the app's registry connection, and SHALL offer that login only when the artifact is on `ghcr.io`; an explicitly configured pull authenticator SHALL take precedence, and the local Docker keychain SHALL be the last resort.

#### Scenario: Artifact on ghcr.io
- **WHEN** an app with the registry connection `ghcr` is added from `oci://ghcr.io/<owner>/<name>:<tag>`
- **THEN** shelf reads the artifact with the user name and token of `ghcr`, with no Docker config needed

#### Scenario: Artifact on another registry
- **WHEN** an app with a registry connection is added from an artifact on a registry other than `ghcr.io`
- **THEN** the connection's login is not sent to that registry

### Requirement: Listing packages and tags of a registry connection
The system SHALL list the deploy artifacts of a registry connection's user through the GitHub API with the connection's token (`shelf connection packages <registry>`), leaving out component images whose package name contains `/`, and SHALL list the tags of an artifact of that user newest first, leaving out tags starting with `sha256-` (`shelf app tags <app>` for an app's own artifact, marking the current tag with `*`).

#### Scenario: Packages listed
- **WHEN** `shelf connection packages ghcr` is run and the user owns `greeter` and `greeter/web`
- **THEN** it prints `greeter` with `oci://ghcr.io/<owner>/greeter` and its update time, and not `greeter/web`

#### Scenario: Tags of a package of another owner
- **WHEN** tags are requested for an artifact whose owner is not the connection's user, or that is not on `ghcr.io`
- **THEN** the request fails with an error and the GitHub API is not called

#### Scenario: App without a registry connection
- **WHEN** `shelf app tags shop` is run for an app without a registry connection
- **THEN** the command fails, saying shelf cannot list its tags

### Requirement: GitHub token confined to the GitHub API
The system SHALL send a registry connection's token only to the GitHub API base it queries, and SHALL not follow a pagination link that points to another host.

#### Scenario: Next page on another host
- **WHEN** a GitHub API reply has a `Link: rel="next"` header pointing outside the API base URL
- **THEN** the listing stops and the token is not sent to that URL

### Requirement: Listing zones of a Cloudflare connection
The system SHALL list the zones in the account of a Cloudflare connection held by this machine, sorted and without duplicates (`shelf connection zones <cloudflare>`), and SHALL fail with a hint to define the connection when this machine does not hold it.

#### Scenario: Zones listed
- **WHEN** `shelf connection zones cf` is run
- **THEN** it prints one zone per line, filtered to the connection's account, or `No zones.`

### Requirement: Connections page is the only page with token fields
The admin UI SHALL define connections only on the page `/connections`, which SHALL be the only page with token fields; its token fields SHALL never be filled in, a refused form SHALL be shown again without the token, and saving or removing a connection SHALL run as a job through the same operations as the CLI.

#### Scenario: Refused registry form
- **WHEN** the registry form on `/connections` is posted with a token but without a user name
- **THEN** the page is shown with status 400 and the error, keeping the name but not the token

#### Scenario: Removing through the UI
- **WHEN** `POST /connections/<kind>/<name>/delete` is sent for a connection apps still use
- **THEN** the job fails with the same message as `shelf connection rm`, naming the apps

### Requirement: Form choices with free-text fallback
The admin UI SHALL serve the packages and tags of a registry connection and the zones of a Cloudflare connection as JSON under `/api/connections/registry/<name>/packages`, `/api/connections/registry/<name>/tags?artifact=...` and `/api/connections/cloudflare/<name>/zones` to signed-in users only, answering a list that cannot be read with status 502, an empty `items` array and the reason, so that the form keeps its free-text field.

#### Scenario: List cannot be read
- **WHEN** the packages route is called for a registry connection whose token GitHub refuses
- **THEN** the reply is status 502 with `{"items": [], "error": "<reason>"}`

#### Scenario: Not signed in
- **WHEN** one of these routes is called without a session
- **THEN** the reply is status 401
