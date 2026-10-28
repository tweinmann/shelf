# Spec Delta

## ADDED Requirements

### Requirement: Generated secrets in preview mode
With `platform.preview` set, the chart SHALL render the Secret `shelf-secrets` itself, with one key
per secret app.yaml declares as `generate: true`: the value already stored in that Secret if it
exists, otherwise a new random URL-safe value of the same length shelf generates for apps.
Without `platform.preview`, the chart MUST NOT render `shelf-secrets`.

#### Scenario: First install of a preview
- **WHEN** a preview of an app that declares `db-password: { generate: true }` is installed
- **THEN** `shelf-secrets` in the preview namespace has a key `db-password` with a new random value

#### Scenario: Upgrade of a preview
- **WHEN** the preview is upgraded to a new artifact
- **THEN** `db-password` keeps the value it had

#### Scenario: An app
- **WHEN** the chart renders an app without `platform.preview`
- **THEN** it renders no `shelf-secrets`, which the platform copies from `shelf-system/app-<app>` as before

#### Scenario: Golden files
- **WHEN** the chart tests render preview mode
- **THEN** the golden files hold a placeholder instead of the random value, and no generated value is written to any file in the repository
