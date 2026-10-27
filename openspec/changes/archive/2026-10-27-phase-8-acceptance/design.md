# Design

## Context

See proposal.md, "Why". When the runs started on 2026-10-27, the dev cluster had no host suffix
and served one app with a domain of its own, `greeter`, through the Cloudflare connection of the
maintainer's account, with a tunnel and a record without `-dev` in their names. Task 1.1 asks for
the suffix `-dev`, which `shelf init cluster` refuses to set while such an app exists unless
`--move-hosts` is passed. The Cloudflare account has a single zone, and the maintainer has no
token for a second account. The repository is public.

## Goals / Non-Goals

**Goals:**
- Run every acceptance check that the dev cluster and the real Cloudflare account allow, and
  record what could not be run and what covers it instead.
- Leave nothing behind in Cloudflare that does not belong to an app of the dev cluster.

**Non-Goals:**
- Changing shelf's behaviour. A defect found here would get a change of its own; none was found.
  The one failure, in `hack/smoke/quick.sh`, was a defect of the test script.

## Decisions

- **`greeter` is taken off the internet before the suffix changes, then exposed again.** With no
  suffix, `shelf app credentials greeter --private` deleted its record and its tunnel
  `shelf-greeter`; `init cluster --host-suffix -dev --move-hosts` then moved an app that had
  nothing public, and exposing it again created `shelf-dev-greeter` and `greeter-dev.<domain>`.
  Alternatives: `--move-hosts` while `greeter` was still public (strands the old record and
  tunnel, which the maintainer would have had to delete by hand); keeping the cluster without a
  suffix (breaks the safety rule that tunnels of the dev cluster are named `shelf-dev-<app>`).
  The chosen way also exercises "taken off the internet, nothing left behind" once more for real.

- **Checks the account cannot run are accepted on level 1.** Moving an app to another domain
  (one zone only) and moving a used Cloudflare connection to another account (no token for a
  second account) are covered by `internal/cli/access_test.go` only. Alternatives: leave 1.4 and
  1.5 open until a second zone or account exists, which would hold back the release for checks
  whose code paths the fakes already cover.

- **The test app for 1.3 and 1.4 is `acc-private`, created for this change.** `shelf app rm`
  and exposing are only run against apps created in the session, so the maintainer's `greeter`
  was not used for the smoke tests; `acc-private` was removed at the end.

- **The release is v0.6.0, from `main` after the merge.** v0.5.0 had been published on
  2026-09-29, before the form choices and 8d; a published tag is never moved. Alternatives:
  tagging v0.6.0 on the branch without a merge (the release would not be on `main`); no release
  (the accepted state would stay unpublished).

- **No account identifiers or real names in the public repository.** A note had carried the
  Cloudflare account ID; the unmerged commit was amended and force-pushed rather than fixed by a
  follow-up commit, which would have kept the ID in `main`'s history once merged. The
  maintainer's connection name and domain were replaced in the current tree by `home` and
  `example.org` (tests, golden files, the spec `app-delivery`, the placeholder on
  `/connections`) and by `<connection>` and `<domain>` in these notes. `home` keeps the sort
  order the tests expect after the second connection `club`; `acme` was rejected because it
  sorts first and already names a registry connection in the spec. Git history and the archived
  changes keep their wording.

## Risks / Trade-offs

- [The domain and account moves have only met fakes] → Run them when a second zone or account
  is available; the smoke script `hack/smoke/expose.sh` could then grow a move step.
- [A new record answered with the zone's old host for about two minutes before HTTPS reached
  the tunnel] → `smoke-expose` already retries for three minutes; no change needed.
