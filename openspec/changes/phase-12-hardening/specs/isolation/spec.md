# Spec Delta

## Purpose

Fences in what an app or preview runs: which pods its namespace admits, and which addresses its
pods can reach, so that code an agent wrote reaches neither other apps nor the operator's LAN
unless the operator allowed it for one app.

## ADDED Requirements

### Requirement: Pod Security of app and preview namespaces
The platform SHALL label every app and preview namespace to enforce the Pod Security level
`baseline` and to warn and audit `restricted`, both at the version `latest`.

#### Scenario: Privileged pod
- **WHEN** an app's values would start a privileged container or one with a `hostPath` volume
- **THEN** the pod is refused, and the pods stage shows the refusal with a hint about Pod Security

#### Scenario: Image that starts as root
- **WHEN** an app runs `postgres:16`, which starts as root
- **THEN** the pod runs

### Requirement: Network isolation of app and preview namespaces
The platform SHALL give every app and preview namespace a NetworkPolicy that selects all its pods
and allows ingress only from pods in the same namespace and from the Traefik namespace, and
egress only to pods in the same namespace, to the cluster DNS on port 53, and to any address
outside `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, `169.254.0.0/16`,
`fc00::/7` and `fe80::/10`.

#### Scenario: Another app
- **WHEN** a pod of the preview `hello--feature-login` connects to the service of the app `greeter`
- **THEN** the connection fails

#### Scenario: Traffic within the app
- **WHEN** the `web` pod of `hello` connects to `db:5432` in its own namespace
- **THEN** the connection succeeds

#### Scenario: LAN and metadata
- **WHEN** a pod connects to `192.168.1.10` or `169.254.169.254`
- **THEN** the connection fails, and a connection to a public address succeeds

#### Scenario: Routed and exposed
- **WHEN** a request reaches the app through Traefik, or through cloudflared in the app namespace
- **THEN** it is answered as before

### Requirement: LAN exceptions are the operator's decision
`shelf app add` and `shelf app credentials` SHALL take `--allow-lan <cidr>`, repeatable, which
replaces the app's list of private address ranges it may reach, and `--deny-lan`, which clears
the list; each range MUST lie inside one of the private ranges of the isolation, and the
exceptions MUST NOT be part of `app.yaml` and MUST NOT apply to the app's previews.

#### Scenario: NAS
- **WHEN** an operator runs `shelf app credentials hello --allow-lan 192.168.1.10/32`
- **THEN** pods of `hello` reach `192.168.1.10` and no other private address, and pods of its previews reach none

#### Scenario: Not a private range
- **WHEN** `--allow-lan 8.8.8.0/24` or `--allow-lan nas.local` is given
- **THEN** the command fails before it changes anything, saying the value must be a private CIDR

#### Scenario: Range of the cluster's pods
- **WHEN** `--allow-lan 10.42.0.0/16` is given on a cluster whose nodes have pod ranges inside it
- **THEN** the command fails before it changes anything, saying the range would reach other apps

#### Scenario: Unchanged by redeploy
- **WHEN** an app with an exception is deployed again or pointed at another tag without `--allow-lan`
- **THEN** it keeps its exception

### Requirement: Isolation in the status
`shelf app status <app>` and `--preview` SHALL show the Pod Security level, the pods that would
violate `restricted` with the first reason for each, and the LAN exceptions, or `none`; reading
them MUST NOT change the cluster.

#### Scenario: Image that starts as root
- **WHEN** `hello` runs `postgres:16` in its `db` component
- **THEN** the status shows `pod security  baseline`, and lists the `db` pod as violating `restricted` with its reason

#### Scenario: No exceptions
- **WHEN** an app has no LAN exceptions
- **THEN** the status shows `lan  none`
