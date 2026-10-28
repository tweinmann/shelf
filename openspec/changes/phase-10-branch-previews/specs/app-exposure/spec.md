# Spec Delta

## ADDED Requirements

### Requirement: Local access to a preview
`shelf app open <app> --preview <branch>` SHALL forward like `shelf app open <app>`, on the
loopback interface only, with the Host header of the preview's host `<app>--<slug>.shelf.internal`,
so that the preview's routes and `stripPrefix` behave as the app's.

#### Scenario: Preview in a browser
- **WHEN** a user runs `shelf app open hello --preview feature/login`
- **THEN** it prints `http://127.0.0.1:<port>/`, and requests reach the preview `hello--feature-login`, not the app

#### Scenario: Branch without a preview
- **WHEN** `hello` has no preview for the branch `gone`
- **THEN** the command fails saying so and naming the previews `hello` has, before it listens on any port
