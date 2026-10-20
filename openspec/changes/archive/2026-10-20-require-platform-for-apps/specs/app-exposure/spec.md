## MODIFIED Requirements

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
