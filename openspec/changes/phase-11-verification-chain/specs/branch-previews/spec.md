# Spec Delta

## MODIFIED Requirements

### Requirement: Preview contents
The platform SHALL give each preview the same deploy chain as an app (its deploy artifact at the
branch tag, checked every 30 seconds, the app values, the chart, the release), verified against
the cluster's signer, the app's registry login, a ResourceQuota (requests of 1 CPU, 2Gi memory,
5Gi storage) and a LimitRange that gives containers without requests a default; it MUST NOT give
a preview a tunnel, cloudflared or a DNS record.

#### Scenario: Preview of an exposed app
- **WHEN** an app exposed through a Cloudflare connection has a preview
- **THEN** the preview namespace has no cloudflared, and nothing outside the cluster reaches it

#### Scenario: Over the quota
- **WHEN** a preview's pods would request more than 2Gi memory together
- **THEN** the pods beyond it are refused, and the preview's pods stage says so

#### Scenario: Unsigned branch artifact
- **WHEN** an artifact without a signature is pushed as `branch-feature-login`
- **THEN** the preview `hello--feature-login` does not apply it, and its deploy artifact stage says the verification failed

#### Scenario: Built image of a preview
- **WHEN** a preview's values name an unsigned built image of the app's package
- **THEN** its pod is denied by the app's admission policy, as in the app's namespace
