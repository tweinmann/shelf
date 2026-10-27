# Spec Delta

## ADDED Requirements

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
