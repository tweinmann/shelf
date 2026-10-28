# Design

## Context

What a preview can do today:

- **Namespaces.** Every app and every preview has a namespace of its own, created by the
  ResourceSets `apps` and `previews`. The namespaces have the labels `shelf.dev/*` and
  `policy.sigstore.dev/include`, and nothing that limits which pods they admit.
- **Network.** There is no NetworkPolicy anywhere. Any pod reaches any other pod and service in
  the cluster, the Kubernetes API, the host's network and the internet. Traefik, in the namespace
  `traefik`, routes by Host header, so a pod that sends `Host: greeter.shelf.internal` to Traefik
  reaches `greeter` even across namespaces.
- **Green.** `shelf app status --wait --commit` says Ready when the release runs the commit's
  values and every pod is ready. Nothing of the app's own is run against it.
- **Releases.** The HelmRelease of an app and of a preview have install and upgrade remediation
  with three retries; upgrade remediation rolls back after the last one
  (`remediateLastFailure: true`).
- **Cluster.** k3s, also in k3d, ships kube-router's NetworkPolicy controller and enables it by
  default. The spike below shows that it enforces what this design needs.

## Goals / Non-Goals

**Goals:**

- **Fenced in.** A preview reaches neither another app, nor the Kubernetes API, nor the LAN, nor a
  cloud metadata address. It still reaches the internet, and the app's own components reach each
  other.
- **Green means tested.** An app's own tests decide whether a rollout counts, for the app and for
  its previews, in the wait verdict too.
- **One report.** An agent gets one document per branch to put into its pull request.
- **Ejectable.** Everything is plain Kubernetes (namespace labels, NetworkPolicy) and plain Helm
  (test hooks): an ejected app keeps it without shelf.

**Non-Goals:**

- No `restricted` opt-in yet (see decision 1).
- No egress allow-list for the internet: an app that calls an API keeps working. Exfiltration
  through the internet is not prevented, only the way into the home network.
- No identity on preview traffic (Cloudflare Access stays in "Later").
- No test history beyond the running revision, and no test retries of shelf's own.
- No CNI-specific policies (Cilium, Calico): the policy has to work on any CNI that implements
  NetworkPolicy.

## Decisions

### 1. Pod Security: `baseline` enforced, `restricted` warned and audited

*Asked and decided by the maintainer.* Namespace labels:
- `pod-security.kubernetes.io/enforce: baseline`;
- `warn: restricted` and `audit: restricted`;
- every `*-version` label is `latest`.

`baseline` refuses what breaks out of a container: privileged containers, host namespaces,
`hostPath`, added capabilities beyond the default set. It admits images that start as root,
which `postgres:16` and most stock database images do.

`latest` follows the cluster's Kubernetes version. Pinning a version would keep the rules
stable across a cluster upgrade, but nobody would move the pin.

Alternatives:
- **`restricted` enforced:** breaks stock images that start as root.
- **An opt-in `security: restricted` in app.yaml:** safe for a tenant to choose, since it can only
  tighten. It was left for later, because nobody needs it yet.

### 1b. Two security defaults in the chart (found during apply)

*Asked and decided by the maintainer.* The chart set no `securityContext`, so every pod violated
`restricted` the same four ways (`allowPrivilegeEscalation != false, unrestricted capabilities,
runAsNonRoot != true, seccompProfile`), and the list in the status would only repeat them. The
chart now sets what no image depends on:
- `seccompProfile: RuntimeDefault` on every pod;
- `allowPrivilegeEscalation: false` on every container.

An entrypoint that drops from root to a user with `gosu` or `su-exec`, as Postgres does, still
works: changing the user is a syscall, not a setuid binary. What is left in the list is what the
image decides, root and capabilities, and that is what the operator wants to see.

Alternatives:
- **Leave the chart alone:** no change of behaviour, but the list is noise.
- **Also drop all capabilities and require a non-root user:** that would be `restricted`
  enforced through the back door, breaking the images decision 1 keeps working.

### 2. `restricted` violations are read with a server-side dry run

The `warn` label sends its warnings to whoever creates the pod. That is helm-controller, so
nobody sees them. shelf reads them itself instead. A server-side dry-run patch of the namespace
label `enforce: restricted` makes the API server answer with one warning per violating pod. A
client-go `WarningHandler` collects them, and nothing is changed: a dry run is not persisted.
`cluster` parses the warning lines (`<pod>: <reason>`) into `restrictedViolations`.

Alternatives:
- **The Pod Security admission library (`k8s.io/pod-security-admission`) in shelf:** a large
  dependency whose version would have to follow the cluster's, and the API server already does
  the same evaluation.
- **Reading the audit log:** k3s does not write it by default.

### 3. One NetworkPolicy per namespace: `shelf-isolation`

Rendered by the ResourceSets, for the app namespace and for each preview namespace:

```yaml
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
  ingress:
    - from:
        - podSelector: {}
        - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: traefik}}
  egress:
    - to: [{podSelector: {}}]
    - to: [{namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: kube-system}},
            podSelector: {matchLabels: {k8s-app: kube-dns}}}]
      ports: [{port: 53, protocol: UDP}, {port: 53, protocol: TCP}]
    - to:
        - ipBlock: {cidr: 0.0.0.0/0, except: [10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16,
                                               100.64.0.0/10, 169.254.0.0/16]}
        - ipBlock: {cidr: ::/0, except: [fc00::/7, fe80::/10]}
    # an app's LAN exceptions, one ipBlock each (decision 4)
```

Why each part:
- **Pod IPs are excluded.** Pod and service IPs of any usual cluster lie in the excluded ranges,
  so the internet rule does not open other namespaces. Policies match after the service address
  is translated, so a ClusterIP counts as the pod behind it.
- **The Kubernetes API is excluded.** It is a private node address behind `10.43.0.1`.
- **DNS is the only thing allowed outside the namespace inside the cluster.**
- **`100.64.0.0/10`** is where Tailscale and carrier-grade NAT put home devices.
- **`169.254.0.0/16`** holds the cloud metadata address, which matters on an ejected or Hetzner
  host.

**Spike on the dev cluster (2026-10-28)**, two throwaway namespaces with this policy in one:

| From the isolated namespace to | Without policy | With policy |
|---|---|---|
| a service in its own namespace | open | open |
| a service in another namespace | open | blocked |
| the registry container `172.19.0.3:5000`, standing in for the LAN | open | blocked |
| `https://10.43.0.1` (Kubernetes API) | — | blocked |
| `http://example.com` (DNS and internet) | open | open |
| from the other namespace into the isolated one | open | blocked |
| API server pod proxy into the isolated namespace (how shelf reads a quick tunnel's name) | open | open |

So level 2 proves the LAN rule on k3d as it holds on k3s on a host.

**cloudflared (found during apply).** cloudflared runs in the app namespace and forwards to
`traefik.traefik.svc`, which the isolation would block, taking every exposed app off the
internet. A second policy `shelf-cloudflared` therefore lets only the pods labelled
`app.kubernetes.io/name: cloudflared` reach the Traefik namespace. It is rendered only for an app
with a tunnel or a quick tunnel, never for a preview. App pods cannot carry that label, since the
chart sets their labels, so app code still cannot reach Traefik. Policies add up, so this needs
no change to `shelf-isolation`.

Alternatives:
- **Egress to Traefik:** would let tests use routes. Rejected: any pod could then reach any app
  through Traefik by its Host header. Tests call components directly (decision 6).
- **Denying the internet too:** breaks every app that calls an API, and package downloads at
  start.
- **CNI-specific policies with FQDN rules:** not generic, and a CRD in the runtime output.

### 4. LAN exceptions: CIDRs set by the operator

*Asked and decided by the maintainer: isolation for every app, with an exception per app.*

The exception is a list of ranges, not "LAN open":
- The excluded private ranges also hold the cluster's own pods, `10.42.0.0/16` on k3s. Opening
  them would reopen every other app.
- A generic platform does not know a cluster's ranges.

The operator names what the app needs:
- `--allow-lan 192.168.1.10/32`, repeatable; the flags replace the list, and `--deny-lan` clears
  it;
- neither flag keeps the list, as `--domain` and the connections are kept.

The list is the provider input `lan` (a list of strings), and the `apps` ResourceSet adds one
`ipBlock` per entry. The `previews` ResourceSet does not read it.

`ops` checks every range:
- it must parse as a CIDR;
- it must lie inside one of the excluded private ranges (anything else is already allowed);
- it must not overlap a node's pod range (`spec.podCIDRs` of the nodes, readable through the API
  on any cluster).

The service range cannot be read generically. A range covering it is not refused, but it opens
nothing: services are reached through their pods.

The UI's access form gets a text field, ranges separated by spaces or commas, and calls the same
`ops` function.

Alternatives:
- **A boolean `--allow-lan`:** reopens the cluster's pods, as above.
- **An exception in app.yaml:** a tenant could open the LAN, against the guardrail that the
  operator decides what reaches beyond the app.

### 5. Tests are Helm test hooks run by Flux

*Asked and decided by the maintainer.*

Rendering:
- `tests:` in app.yaml becomes one Pod per test in the chart, with `helm.sh/hook: test`;
- `before-hook-creation` keeps the last pod, and its log, until the next run;
- the label `shelf.dev/test: <name>` marks it;
- the pods stage leaves it out.

Running: the HelmRelease gets `test.enable: true`, so helm-controller runs the tests after every
install and upgrade, within the release timeout (5 minutes).

What a failure does:
- **App:** `test.ignoreFailures: false`. A failed test fails the upgrade, the remediation retries
  it, and after the last retry it rolls back, so the previous version runs.
- **First install:** install remediation has no rollback, so the failing version keeps running,
  as the spec says.
- **Preview:** `test.ignoreFailures: true`. The release stays Ready and the version that failed
  keeps running. The result is in `status.history[0].testHooks`.

`cluster` reads the tests stage:
- from the latest history entry's `testHooks`, by revision;
- with the `TestSuccess` condition;
- with the last 20 lines of the failed pod's log, through the pod log API.

A test with `build:` is built, signed and scanned like a component:
- `shelf build-plan` lists it;
- `render --image` pins it;
- the workflow scans every digest in the values;
- the admission policy covers `<package>/**`;
- `cluster.verification` includes test images in "the scan covers every image".

Alternatives:
- **A Job started by shelf:** shelf would become a second rollout engine, and the tests would not
  run when nobody runs shelf.
- **Flux Kustomization health checks:** they check readiness, not the app's behaviour.
- **Tests only for previews:** "green" would mean two things.

### 6. Tests reach the app inside its namespace

A test calls components by their service names, through the references it already has:
`http://${web.host}:${web.port}/`. The isolation admits that. Traefik is not reachable
(decision 3), so a route's `stripPrefix` is not part of what a test sees. The test containers get
the app's secrets in the same way, so a test can log in.

### 7. The wait counts tests

The tests stage comes after the pods. `Verdict` says Ready only when the tests of the awaited
commit's revision passed, or there are none. A failed test is `Failed` at once: it does not wait
for the three upgrade retries, so an agent hears it as soon as it happened.

### 8. `shelf app report`

*Asked and decided by the maintainer: a command of its own.*

`ops.Report` puts together, read-only:
- the `AppStatus` parts (state, commit, tests, verification, isolation);
- for a preview, the comparison with its app: component digests from both values ConfigMaps, and
  the CRITICAL and HIGH counts from both scan summaries.

`internal/cli` prints Markdown by default, meant for `gh pr comment --body-file -`, and with
`--json` a `Report` document.

"What changed" means the running images, not a Git diff:
- the platform never talks to Git;
- the agent has the diff already.

Alternatives:
- **Only in `app status`:** no text an agent can paste.
- **A job summary of the workflow:** the workflow cannot see the cluster.

### 9. Turnaround and RAM

There is no new controller: kube-router is part of k3s, and helm-controller runs the tests.

A test adds its own run time to a rollout. On a failed upgrade it adds the three retries before
the rollback, but not to the verdict (decision 7).

`smoke-hardening` prints the time from push to Ready for an app with one curl test, against the
same app without it.

### 10. `smoke-hardening`

New level-2 smoke test, with `examples/hello` plus a curl test. It checks:
- the isolation table of the spike, run from the app and from a preview;
- a LAN exception reaches the registry container, while the preview still cannot;
- a privileged pod is refused;
- `postgres:16` runs and is listed as violating `restricted`;
- a passing test gives Ready;
- a failing test on main keeps the previous version and ends `--wait` with Failed at "the tests";
- a failing test on a preview keeps the preview running and red;
- `shelf app report` for that preview names the changed component.

`select-smokes.sh` lists the paths it covers.

## Risks / Trade-offs

- **[Risk] Kubelet health probes come from the node, which a policy might block** → kube-router
  admits traffic from the node to its own pods. `smoke-chart` and `smoke-hardening` run apps
  with probes under the policy. If a probe fails there, the policy gets an ingress rule for the
  node addresses.
- **[Risk, seen during apply] A new pod's first connection inside its namespace is refused** →
  kube-router lets a pod in through the ingress rule `podSelector: {}` only once its IP is in the
  rule's ipset, which happens a moment after the pod starts. A curl test that connects at once
  got `Connection refused` after 1 ms; one retry 100 ms later got through (dev cluster,
  2026-10-28). Tests retry their first connection (`curl --retry 5 --retry-connrefused` in the
  example and the README), as against any service that just started; the same holds for app
  components that connect at start, which retry anyway. Alternatives: an init container that
  sleeps (an extra image in every app, and a guessed duration), or a rule by address range
  (not generic: a cluster's pod range is not known).
- **[Risk] An existing app that reaches the LAN or another app breaks at the platform
  upgrade** → named in the release notes. The operator adds `--allow-lan`; apps were never meant
  to reach each other.
- **[Risk] A pod that violates `baseline` is refused at its next start after the upgrade** → the
  running pods stay, since labels do not evict. The pods stage names the refusal with a Pod
  Security hint. Before upgrading, `shelf app status` shows the `restricted` violations, a
  superset.
- **[Risk] A failed test on main retries the upgrade three times before the rollback** → about
  three times the test run until the old version is back; the old pods keep serving meanwhile,
  as an upgrade replaces pods only when the new ones are ready.
- **[Trade-off] The internet stays open** → an agent's code can still send data out. Closing it
  needs an allow-list per app, which no app declares today.
- **[Trade-off] IPv6 is covered by the policy but untested** → k3d is IPv4 only. The rule is
  written for dual-stack hosts and checked at level 1 by rendering.
- **[Risk] A dry run of the namespace label needs patch rights on namespaces** → shelf already
  has them. The dry run is never persisted, so `--wait` and status stay read-only in effect.

## Migration Plan

1. Release with the new platform and chart. `shelf init cluster` upgrades existing clusters; the
   ResourceSets add the labels, the policy and `test.enable` to every app and preview.
2. Apps without tests: the tests stage is Ready with nothing to run, and nothing else changes,
   apart from the isolation.
3. Apps that need the LAN: `shelf app credentials <app> --allow-lan <cidr>`.
4. Rollback: an older platform removes the policies and labels again, since the ResourceSets
  prune what they no longer render.

## Open Questions

None.

## Results

### Level 2 (dev cluster, 2026-10-28)

Every smoke test passed against a fresh dev cluster, with `SMOKE_FULL=1`:

| Smoke test | Time |
|---|---|
| `smoke-init` | 208 s |
| `smoke-secrets` | 7 s |
| `smoke-chart` (health probes under the policy) | 128 s |
| `smoke-apps` (apps without tests are Ready) | 261 s |
| `smoke-agent` (`shelf app open` through Traefik) | 392 s |
| `smoke-previews` | 344 s |
| `smoke-verify` | 218 s |
| `smoke-hardening` | 366 s |

`smoke-quick` was not run: it opens a public tunnel, and is left to its own acceptance.

Measured by `smoke-hardening` and `smoke-previews`:

| Check | Result |
|---|---|
| Push to Ready without a test | 52 s |
| Push to Ready with one curl test | 63 s |
| First push of a branch to Ready | 27 s (Phase 10: 23 s) |
| Later commit on a branch to Ready | 13 s (Phase 10: 11 s) |
| RAM of a preview / of the app | 29 Mi / 32 Mi |
| Deleted branch tag to preview gone | 29 s |

Every isolation check has a control: from a namespace without the policy, the Kubernetes API
and the registry container are reachable, from the app's and the preview's not.

What apply found:
- **cloudflared and Traefik.** See decision 3; without `shelf-cloudflared`, every exposed app
  would have gone offline at the upgrade.
- **A new pod's first connection.** See the risk above; the tests retry it.
- **The `restricted` list.** See decision 1b; without the two defaults, it named every pod alike.
- **Leaked forwarders.** `smoke-agent`, `smoke-previews` and the new `smoke-hardening` started
  `shelf app open` through a shell function, so `kill $!` stopped the subshell and left the
  forwarder running, holding its port into the next run. They start the binary now.

Also confirmed: `shelf serve` kept working through two `cluster-reset`s without a restart, the
follow-up of Phase 11.

### Level 3 (`tweinmann/shelf-hello` as `gretter`, release v0.9.0, 2026-10-28)

The dev cluster was installed again from the release, keyless, and `gretter` added privately with
previews. Both release artifacts carried keyless signatures of `release.yml`.

| Check | Result |
|---|---|
| `gretter` on the release platform | verified; Pod Security baseline; for `restricted`, only "unrestricted capabilities, runAsNonRoot" are left per pod (decision 1b), Postgres and Mongo run |
| Push of a curl test to `main` (`33f8756`) | CI 1 min 58 s (Phase 11: 2 min 36 s, cold cache); Ready, "1 passed: smoke"; the scan covers the test's image |
| A branch with a test that fails (`3a40f54`) | CI 1 min 48 s; `--wait --commit` ends `Failed` at "the tests" with exit code 7 and the log; the preview keeps running that commit; `shelf app report --preview` shows it red, with the log, the scan and the comparison with `main` |
| The same commit merged to `main` | CI 1 min 35 s; `--wait` ends `Failed` after 22 s; Helm rolled back 10 s later; after four attempts (about 2 min) `gretter` runs the values of `33f8756` again, `/` answered 200 throughout; "the app" Ready, "the tests" Failed |

Two things the acceptance taught, both now in the README and the tenant skill:
- **A test has to be able to fail.** The web component of `shelf-hello` answers every path with
  200, so `curl -f …/nope` passed. Breaking the port did fail.
- **curl with several URLs** exits with the status of the last one; a failure before it passes
  unnoticed without `--fail-early`.

As foreseen in the risks, a failing test on `main` makes helm-controller retry the upgrade three
times before it stalls, replacing the pods each time; the verdict does not wait for that.

After the acceptance, the test was fixed on `main` (`801843a`): Ready, verified, and the release
left its stalled state with the new values. `gretter` was then exposed again with
`--cloudflare tobile --domain tobile.ch`: deployed privately, verified, then tunnel and record
created; `https://gretter.tobile.ch/` answered 200, so cloudflared reaches Traefik under
`shelf-cloudflared` on the released platform.

**Found after the archive, and fixed with it:** an admin UI started before Phase 12 failed on the
app page of `gretter` once its values held `tests`: shelf read the resolved app.yaml from the
cluster as strictly as one a person writes. `render.ParseApp` now uses `schema.ParseLenient`,
which leaves out fields it does not know (also in a long-form `route`), so an older shelf can
still show an app built by a newer workflow; `shelf validate` and `render` stay strict. The main
spec `app-manifest` has the requirement "Reading a resolved app.yaml from the cluster".
