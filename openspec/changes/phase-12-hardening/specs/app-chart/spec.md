# Spec Delta

## ADDED Requirements

### Requirement: Tests as Helm test hooks
The chart SHALL render one Pod per entry in `tests`, named `<app>-test-<test>` and labelled
`shelf.dev/app` and `shelf.dev/test` but not `shelf.dev/component`, the annotations `helm.sh/hook: test` and
`helm.sh/hook-delete-policy: before-hook-creation`, `restartPolicy: Never`, the test's image,
command, args, env with references and secrets expanded as for a component, its resources, and
the image pull secret; it MUST NOT render a Service, Ingress or volume for a test.

#### Scenario: Test pod
- **WHEN** the values of `hello` declare a test `smoke` with `image` and `args`
- **THEN** the chart renders the Pod `hello-test-smoke` with the hook annotations and `restartPolicy: Never`, and no other object for `smoke`

#### Scenario: Test reads a secret
- **WHEN** the test `smoke` sets `env: { TOKEN: ${secrets.token} }`
- **THEN** the Pod reads `TOKEN` from the Secret `shelf-secrets`, as a component does

### Requirement: Security defaults of every pod
The chart SHALL give every pod it renders, components and tests, the seccomp profile
`RuntimeDefault` and every container `allowPrivilegeEscalation: false`, and SHALL NOT set a user,
group or capabilities, which depend on the image.

#### Scenario: Component
- **WHEN** the chart renders the component `web`
- **THEN** its pod template has `securityContext.seccompProfile.type: RuntimeDefault` and its container `securityContext.allowPrivilegeEscalation: false`

#### Scenario: Image that starts as root
- **WHEN** an app runs `postgres:16`, whose entrypoint switches from root to the user postgres
- **THEN** it runs with these defaults, and Pod Security still lists it for `restricted`, because it runs as root
