## ADDED Requirements

### Requirement: App operations require the platform
`shelf app add`, `shelf app credentials` and `shelf app redeploy`, and their admin UI jobs, MUST
fail at once when the ResourceSet `apps` is missing from `shelf-system`, before writing anything to
the cluster, the host backup or Cloudflare. The error SHALL say that the cluster has no platform and
that `shelf init cluster` installs it.

#### Scenario: Adding an app to a cluster without the platform
- **WHEN** `shelf app add greeter oci://ghcr.io/owner/greeter:main --cloudflare tobile --domain example.com` runs against a cluster where `shelf init cluster` never installed the platform
- **THEN** it fails within seconds, saying the cluster has no platform and to run `shelf init cluster`
- **AND** no Secret or provider exists in `shelf-system` for `greeter`, no backup file was written, and nothing was changed in Cloudflare

#### Scenario: Redeploying in a cluster without the platform
- **WHEN** `shelf app redeploy greeter` runs and the ResourceSet `apps` is missing
- **THEN** it fails at once with the same message instead of waiting for the deploy artifact until its timeout

#### Scenario: Cluster with the platform
- **WHEN** the ResourceSet `apps` exists
- **THEN** the operation runs as before, and its output is unchanged
