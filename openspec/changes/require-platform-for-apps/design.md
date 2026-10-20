# Design

## Context

See proposal.md, "Why". The order of an add today:

```
ops.AddApp
  read artifact, plan secrets, write backup
  planExposure -> Cloudflare: create/replace tunnel, DNS     <- already outside the cluster
  Cluster.AddApp
    checkQuickTunnels (only with --quick)                    <- the only platform check today
    apply app-<app>, tunnel-<app>, provider
    rollOut: wait for OCIRepository <app>/deploy             <- hangs without the ResourceSet
```

`checkQuickTunnels` (internal/cluster/tunnel.go) already reads the ResourceSet `apps` in
`shelf-system`. It treats a missing ResourceSet like an old one. It runs inside `cluster.AddApp`,
so it comes after Cloudflare has been changed.

## Goals / Non-Goals

**Goals:**
- One platform check, run first by every operation that waits for a rollout.
- The quick-tunnel check moves to the same place and runs at the same time.

**Non-Goals:**
- Checking that the platform is healthy (Ready, current revision). The rollout wait and the
  five-stage diagnosis report that already. This check only answers whether apps can be deployed
  here at all.
- `shelf app rm`. It works without the platform, because it deletes the provider and the
  namespace directly.
- Installing the platform from `shelf app add`. That needs `init cluster`'s confirmation and its
  choices (host suffix, platform version), and is rejected.
- A default host suffix in `just init-cluster`. The maintainer decided to keep it the caller's
  choice.

## Decisions

- **The check is a `Cluster` method, called by ops.** `cluster.CheckPlatform(ctx, cfg, quick bool)`
  returns an error when the ResourceSet is missing, or when `quick` is set and its template lacks
  the `quick` input. `ops.Cluster` gains `CheckPlatform`. `ops.AddApp` calls it as soon as it knows the quick flag the
  app will end up with, from `opts.Access` and the current config. Only the lines naming the chosen
  connections come before it, so it runs before the artifact is read and before Cloudflare is
  touched. `SetAccess`
  goes through `AddApp`; `ops.Redeploy` calls it with `quick=false`. `cluster.AddApp` stops calling
  `checkQuickTunnels`, and the function becomes the quick branch of `CheckPlatform`.
  *Alternative:* keep the check inside `cluster.AddApp`. Rejected, because the tunnel would still
  be replaced first.
  *Alternative:* have `rollOut` give up early when the ResourceSet is missing. That fixes the hang
  only, and leaves the writes behind.
- **Two errors for two causes.** A missing ResourceSet says "this cluster has no shelf platform;
  install it with `shelf init cluster`". A present but old one keeps its message, "the platform in
  this cluster does not run quick tunnels yet", with the corrected hint "… or add the app without
  --quick". Today both causes print the quick-tunnel message, which is misleading for a cluster
  with no platform at all.
- **Existence, not Ready.** A ResourceSet that exists but fails still has its reason shown by
  `shelf app status`, and a wait on it ends with that reason. Requiring Ready would refuse an add
  during a short platform reconcile.
- **Dev bootstrap through recipe dependencies.** `init-cluster *flags: platform-push chart-push`.
  The smoke tests already push for themselves (`hack/smoke/init.sh` and others) and call `shelf`
  directly, so nothing changes for them. `cluster-up` does not push, so a restart never replaces
  a platform that is being tested.

## Risks / Trade-offs

- [The server's jobs call ops, and the UI renders the job error as text] → No UI change is
  needed. The server's fake `Platform` sits above ops, so its golden files do not change.
- [Every add and redeploy makes one more GET] → It is a single read of one object, and it
  replaces the read the quick check already made.
- [`just init-cluster` now needs a pushable working copy of `platform/`] → `platform-push.sh`
  validates with `kubectl kustomize` first and stops on errors, which is the behaviour wanted.
