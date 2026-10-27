# app-exposure Specification

## Purpose
Defines how an app becomes reachable. Every app answers inside the cluster by default; it reaches
the internet only when asked, either through its own named Cloudflare tunnel in the account of a
Cloudflare connection (with a domain of its own and a DNS record written by shelf), or through a
quick tunnel at a random trycloudflare.com address that needs no account. The CLI (`shelf app add`,
`shelf app credentials`, `shelf app rm`) and the admin UI offer the same choices through one
operations layer.

## Requirements

### Requirement: Host name of an app
The system SHALL give an app with a domain of its own the host `<app><host-suffix>.<domain>`, and
an app without a domain the host `<app>.shelf.internal`, which carries no host suffix and is
reachable inside the cluster only.

#### Scenario: App with a domain
- **WHEN** the app `greeter` is added with `--domain example.com` to a cluster with host suffix `-dev`
- **THEN** Traefik routes it by the host `greeter-dev.example.com`

#### Scenario: App without a domain
- **WHEN** the app `greeter` is added without `--domain`
- **THEN** it answers at `greeter.shelf.internal` inside the cluster
- **AND** no page offers a link to that host

### Requirement: Private by default
The system SHALL keep a newly added app inside the cluster when no exposure is chosen, and SHALL
keep an existing app's exposure on a later `shelf app add` or `shelf app credentials` that does not
change it.

#### Scenario: New app without exposure flags
- **WHEN** an app is added without `--cloudflare` or `--quick`
- **THEN** no cloudflared runs in its namespace and no tunnel or DNS record is created

#### Scenario: Re-adding keeps the exposure
- **WHEN** an app exposed through a quick tunnel is added again with a new tag and no exposure flag
- **THEN** it keeps its quick tunnel

### Requirement: Exposure choices are exclusive
The system MUST reject a request that asks for more than one of a Cloudflare connection, a quick
tunnel and private at once, before anything is written, and the app's provider MUST never carry
both a `tunnel` and `quick` input.

#### Scenario: Conflicting flags
- **WHEN** `shelf app add` is given both `--cloudflare main` and `--quick`
- **THEN** it fails with "an app is exposed through a Cloudflare connection, through a quick tunnel, or not at all; choose one"
- **AND** nothing in the cluster or in Cloudflare is changed

### Requirement: Cloudflare exposure needs a public domain and a held connection
The system MUST refuse to expose an app through a Cloudflare connection, before anything is
written, when the app has no domain of its own, when the domain is not public (ends in `.local`,
`.localhost`, `.internal`, `.test`, `.invalid`, `.example` or `.home.arpa`, or has no dot), or when
this machine holds no Cloudflare connection of that name.

#### Scenario: No domain
- **WHEN** an app is added with `--cloudflare main` and without a domain
- **THEN** it fails, saying the app needs a domain of its own, one of the zones of that account

#### Scenario: Unknown connection
- **WHEN** an app is added with `--cloudflare other` and no connection `other` is defined
- **THEN** it fails, telling to define it with `shelf connection add cloudflare other`

### Requirement: Own named tunnel per app
The system SHALL expose an app with a Cloudflare connection through a tunnel of its own named
`shelf<host-suffix>-<app>`, in the account of that connection, reusing an existing tunnel of that
name only when the cluster holds its credentials and otherwise deleting it and creating a new one.

#### Scenario: First exposure
- **WHEN** an app `greeter` on a cluster with host suffix `-dev` is added with a Cloudflare connection and a domain
- **THEN** a locally managed tunnel `shelf-dev-greeter` is created in the connection's account
- **AND** the progress reports it as created

#### Scenario: Tunnel without credentials in the cluster
- **WHEN** a tunnel `shelf-dev-greeter` exists in the account but the cluster does not hold its credentials
- **THEN** that tunnel is deleted and a new one is created, reported as replaced

#### Scenario: Unchanged tunnel
- **WHEN** an exposed app is added again with the same connection
- **THEN** the existing tunnel is kept and reported as unchanged

### Requirement: Tunnel credentials stay out of the Cloudflare token path
The system SHALL store the credentials of an app's named tunnel in the Secret `tunnel-<app>` in
`shelf-system` (key `credentials.json`, labelled `shelf.dev/app`), have the platform copy it into
the app namespace for cloudflared, and MUST never put the Cloudflare API token into the cluster.

#### Scenario: Tunnel secret written
- **WHEN** a tunnel is created for an app
- **THEN** `shelf-system/tunnel-<app>` holds its credentials and the app namespace gets a copy named `shelf-tunnel`

#### Scenario: Tunnel secret removed
- **WHEN** an app is no longer exposed through a named tunnel
- **THEN** `shelf-system/tunnel-<app>` is deleted

### Requirement: cloudflared in the app namespace
The system SHALL run cloudflared as the Deployment `cloudflared` in the app's own namespace for an
app with a named or a quick tunnel, with a single catch-all rule to Traefik
(`http://traefik.traefik.svc.cluster.local:80`), and SHALL run none for a private app.

#### Scenario: Named tunnel
- **WHEN** an app has a named tunnel
- **THEN** its namespace has the ConfigMap `cloudflared` naming the tunnel and the Deployment `cloudflared` running it
- **AND** cloudflared restarts when the credentials or the configuration change

#### Scenario: Private app
- **WHEN** an app is private
- **THEN** its namespace has no cloudflared

### Requirement: DNS record written by shelf
The system SHALL point the app's host at its named tunnel with a proxied CNAME to
`<tunnel-id>.cfargotunnel.com` in the zone of the app's domain, written from the operator's machine
with the connection's token after cloudflared runs the tunnel, creating or updating it as needed.

#### Scenario: Record created
- **WHEN** an app is exposed for the first time
- **THEN** a proxied CNAME for `<app><host-suffix>.<domain>` is created and reported

#### Scenario: Record already correct
- **WHEN** the record already points at the tunnel and is proxied
- **THEN** it is left alone and reported as unchanged

### Requirement: Moving off a named tunnel cleans up
The system SHALL, after cloudflared has stopped serving the old tunnel, delete the old DNS record
when the app is taken off Cloudflare, moves to another host or another account, and delete the old
tunnel when the app no longer uses it.

#### Scenario: Taken off with --private
- **WHEN** an app with a named tunnel is changed with `shelf app credentials <app> --private`
- **THEN** shelf waits until its cloudflared is gone, deletes the app's CNAME and deletes its tunnel
- **AND** the app answers inside the cluster only

#### Scenario: Switch to a quick tunnel
- **WHEN** an app with a named tunnel is changed with `--quick`
- **THEN** its record and its tunnel are deleted and cloudflared runs a quick tunnel instead

### Requirement: Removal withdraws record and tunnel
The system SHALL, on `shelf app rm` of an app with a named tunnel, delete the app's DNS record and
its tunnel after the namespace (and with it cloudflared) is gone.

#### Scenario: Removing an exposed app
- **WHEN** `shelf app rm greeter` runs for an app exposed at `greeter-dev.example.com`
- **THEN** the CNAME for `greeter-dev.example.com` and the tunnel `shelf-dev-greeter` are deleted

### Requirement: Exact-name deletion
The system MUST delete DNS records only by the exact host name of the app and tunnels only by the
ID of the tunnel the app used, closing the tunnel's remaining connections first, and never by a
prefix or pattern.

#### Scenario: Deleting a tunnel
- **WHEN** a tunnel of an app is deleted
- **THEN** its connections are closed and then the tunnel with that ID is deleted, and no other tunnel is touched

### Requirement: Connection not held on this machine
The system SHALL leave an app's tunnel and record untouched when the app is exposed through a
Cloudflare connection this machine does not hold, and SHALL warn and name the record or tunnel to
delete by hand when it would have to remove them without that connection.

#### Scenario: Re-adding without the connection
- **WHEN** an exposed app is added again on a machine that does not hold its Cloudflare connection
- **THEN** a warning says its tunnel and record were left as they are
- **AND** the app keeps running its tunnel

#### Scenario: Removing without the connection
- **WHEN** such an app is removed
- **THEN** a warning names the host and the tunnel that stay in Cloudflare

### Requirement: Quick tunnel
The system SHALL, for an app added or changed with `--quick` (UI: "none: a quick tunnel at a
temporary trycloudflare.com address"), run cloudflared without an account so that it gets a random
`https://<words>.trycloudflare.com/` address, needing neither a Cloudflare connection, a domain nor
a DNS record.

#### Scenario: Adding with --quick
- **WHEN** `shelf app add hello oci://… --quick` runs
- **THEN** cloudflared in `hello` runs a quick tunnel and the add waits until it has an address
- **AND** the address is printed as the detail of the wait for the tunnel

#### Scenario: Quick tunnel on a reserved domain
- **WHEN** an app with the domain `dev.local` asks for a quick tunnel
- **THEN** it is exposed at its trycloudflare.com address

### Requirement: Quick tunnel Host header
The system SHALL have cloudflared of a quick tunnel rewrite the Host header of every request to the
app's own host (`<app><host-suffix>.<domain>`, or `<app>.shelf.internal` without a domain), so that
Traefik routes it to the app.

#### Scenario: Request through the quick tunnel
- **WHEN** a request arrives at the app's trycloudflare.com address
- **THEN** Traefik receives it with the Host header of the app's own host

### Requirement: Quick tunnel address read live
The system SHALL read a quick tunnel's address from cloudflared's metrics endpoint `/quicktunnel`
(port 2000) through the API server's pod proxy, only from a ready pod that is not being deleted,
store it nowhere, and show it as the app's address in `shelf app status` and the admin UI.

#### Scenario: Address after a restart
- **WHEN** cloudflared of a quick tunnel restarts and gets a new name
- **THEN** the next status shows the new trycloudflare.com address

#### Scenario: No address yet
- **WHEN** no cloudflared pod of the app is ready
- **THEN** the app has no address and the UI says the quick tunnel has no address yet

### Requirement: Quick tunnel requires a current platform
The system MUST refuse to add an app with a quick tunnel when the platform's `apps` ResourceSet
does not know the `quick` input. It MUST refuse before anything is written and before anything
changes in Cloudflare. The error SHALL say to run `shelf init cluster` first or to add the app
without `--quick`.

#### Scenario: Old platform
- **WHEN** an app is added with `--quick` against a cluster whose ResourceSet predates quick tunnels
- **THEN** it fails with "the platform in this cluster does not run quick tunnels yet"
- **AND** the hint names `shelf init cluster` and adding the app without `--quick`, never a `--private` flag of `shelf app add`

#### Scenario: Switching an existing app to a quick tunnel
- **WHEN** `shelf app credentials greeter --quick` runs against such a platform for an app with a named tunnel
- **THEN** it fails with the same error, and the app's tunnel and DNS record are left as they were

### Requirement: Waiting for the tunnel to settle
The system SHALL, after a change, wait until cloudflared runs the app's new named tunnel, or runs a
quick tunnel and reports an address, or, for an app no longer exposed, until cloudflared is gone,
and only then clean up old records and tunnels.

#### Scenario: Moving from a named to a quick tunnel
- **WHEN** an app moves from a named tunnel to a quick tunnel
- **THEN** a cloudflared that still runs the named tunnel does not end the wait

### Requirement: Links only for working addresses
The system SHALL offer a link to an app only when it has a quick tunnel address, or a named tunnel
on a public domain, and the components' links SHALL follow the app's address.

#### Scenario: Named tunnel link
- **WHEN** an app has a named tunnel on `example.com`
- **THEN** its address is `https://<app><host-suffix>.example.com/`

#### Scenario: Private app
- **WHEN** an app has no tunnel
- **THEN** the pages show its host as inside the cluster only, without a link

### Requirement: Local access through a port-forward
`shelf app open <app>` SHALL listen on a local port and forward every request through the API
server to Traefik with the Host header set to the app's own host, so that routes and `stripPrefix`
behave as through a tunnel; it SHALL work for private and exposed apps alike and run until stopped.

#### Scenario: Private app in a browser
- **WHEN** a user runs `shelf app open hello` for an app without exposure that routes `/` and `/api` with `stripPrefix`
- **THEN** it prints `http://127.0.0.1:<port>/`, a request to `/api/x` reaches the `/api` component as `/x`, and no tunnel, DNS record or cloudflared is created

#### Scenario: Stopped
- **WHEN** the user presses Ctrl-C
- **THEN** the listener and the forward are closed and the command exits with status 0

#### Scenario: App does not exist
- **WHEN** a user runs `shelf app open nope`
- **THEN** it fails saying that app nope does not exist, before it listens on any port

### Requirement: Local access stays on the loopback interface
`shelf app open` MUST listen on `127.0.0.1` only and offer no option for another address. The
port SHALL be chosen freely unless `--port` names one, and `--browser` SHALL open the printed
address in the default browser; without it no browser is started.

#### Scenario: Chosen port
- **WHEN** a user runs `shelf app open hello --port 8081` and the port is free
- **THEN** it listens on `127.0.0.1:8081` and on no other address

#### Scenario: Port in use
- **WHEN** the port given with `--port` is in use
- **THEN** the command fails saying so, without forwarding anything

#### Scenario: No browser by default
- **WHEN** a user runs `shelf app open hello` without `--browser`
- **THEN** no browser is started and only the address is printed

### Requirement: Local access is not an exposure
`shelf app open` SHALL NOT change the app's registration, exposure, tunnel or DNS record, SHALL NOT
write to the cluster beyond the port-forward the API server opens, and SHALL NOT appear in the
admin UI.

#### Scenario: Status while forwarding
- **WHEN** `shelf app open hello` runs and the user runs `shelf app status hello`
- **THEN** the app's exposure and address are unchanged
