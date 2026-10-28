# Spec Delta

## ADDED Requirements

### Requirement: Only a verified app is exposed
`shelf app add` and `shelf app credentials` SHALL refuse `--cloudflare` and `--quick` for an app
that has no tunnel yet unless the app is verified, with the reason, before any Cloudflare call or
tunnel; a new app with such a flag SHALL be added privately and waited for first, and stays added
and private when the check fails. An app that is already exposed keeps its exposure.

#### Scenario: Unverified app refused
- **WHEN** a user runs `shelf app credentials hello --quick` and the running artifact of `hello` is unsigned
- **THEN** the command fails with the reason and a hint to push again with the current workflow, and no cloudflared and no tunnel are created

#### Scenario: New app exposed after its rollout
- **WHEN** a user runs `shelf app add hello oci://ghcr.io/acme/hello:main --cloudflare acme --domain hello.example.com` for a signed and scanned artifact
- **THEN** the app is added, rolls out privately, is found verified, and only then gets its tunnel and DNS record

#### Scenario: New app that is not verified
- **WHEN** the same command runs for an artifact without a scan summary
- **THEN** the app is added and private, and the command exits 1 naming the missing scan

#### Scenario: Same gate in the admin UI
- **WHEN** the access form of the app page asks for a quick tunnel for an unverified app
- **THEN** the job fails with the same reason, as it calls the same operation

#### Scenario: Exposed app keeps its tunnel
- **WHEN** an exposed app gets a new artifact without a scan summary
- **THEN** it keeps its tunnel and its status shows it is not verified
