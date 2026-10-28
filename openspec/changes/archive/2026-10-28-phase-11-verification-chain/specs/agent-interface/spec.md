# Spec Delta

## ADDED Requirements

### Requirement: Verification in the agent interface
The `AppStatus` of an app or preview SHALL hold `verification` with `verified`, `reason` and
`hint` when not verified, `source`, `signer`, `exposable`, and `scan`, one entry per component
with `component`, `digest`, `scanned`, `critical`, `high`, `scanner` and `scannedAt`; a
`WaitResult` with outcome `Ready` says nothing about verification.

#### Scenario: Verified app as JSON
- **WHEN** a user runs `shelf app status hello --json` for a verified app with `web` and `db`
- **THEN** `verification.verified` and `verification.exposable` are true and `scan` has two entries

#### Scenario: Agent reads why it cannot be exposed
- **WHEN** the running artifact has no scan summary
- **THEN** `verification.verified` is false and `reason` names the missing scan
