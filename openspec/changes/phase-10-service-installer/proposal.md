# Proposal

> Draft. This change holds the roadmap text only and is marked `skip_specs: true`.
> `/opsx:propose` turns it into a full change: it writes the delta specs, `design.md` and
> `tasks.md`, revises this proposal, and removes `skip_specs` from `.openspec.yaml`.

## Why

shelf has to run on the mini without a terminal: start at login, survive a power cut, and install with one command.

## What Changes

- `shelf service install|uninstall|status` for the LaunchAgent.
- `install.sh`: binary, checksum, symlink, host name, `pmset`, application firewall, service, URL and setup token.

## Capabilities

### New Capabilities

- `host-service` (planned): the LaunchAgent and what it starts.
- `installer` (planned): `install.sh`.

### Modified Capabilities

To be decided at `/opsx:propose`.

## Impact

CLI, `install.sh`, `internal/host`. Level 3: needs the mini.

## Roadmap text (from the plan before OpenSpec)

`shelf service install|uninstall|status` for the LaunchAgent, and `install.sh`: download the
release binary, verify the checksum, install it to `~/.shelf/bin` with the symlink in
`/usr/local/bin`, offer the host name, set `pmset`, allow the binary through the application
firewall, register the service, print the URL and the setup token. Autostart after a power cut
falls out of the same chain.

**Acceptance:** the one-liner on an untouched user account ends with a URL that opens the claim
page from another device on the LAN; pulling the power and restoring it brings shelf and the
cluster back with no keyboard attached; `shelf service uninstall` leaves no loaded agent and no
plist behind; the `xattr` workaround for a tarball downloaded in a browser is documented.

### Components on the mini (from the architecture of the plan)

Everything shelf itself runs on the host is one binary in two modes. The CLI is one invocation;
`shelf serve` is the long-running one, started by launchd.

| Concern | Building block |
|---|---|
| Admin UI and its API | `shelf serve`, an HTTP server with embedded templates, bound to the LAN |
| Autostart | LaunchAgent `dev.shelf.agent`, `RunAtLoad` + `KeepAlive` |
| VM | Colima, started and stopped by the service |
| Host setup | `shelf init host` (brew, `pmset`, Colima profile, host name) |

The service runs **as the logged-in user, not as root**: Colima is per-user (`~/.colima`,
`~/.lima`), and so are the kubeconfig, the secret backups and the tokens. A root daemon running
`colima start` would silently create a second VM under root's home. No secret ever goes into the
plist, which is world-readable.

`shelf serve` at startup loads its configuration, serves nothing but the claim page while the
instance is unclaimed, and binds its listener. Every page then reads the cluster when it is
asked for, under a timeout of a few seconds, so a cluster that does not answer becomes a page
that says so instead of a request that hangs — which is what keeps the UI useful when the VM is
down. (Phase 7 built this with a timeout rather than the background poller this paragraph
described first: the poller would be a second source of truth, and its staleness would have to
be shown on every page.) With autostart enabled the service also brings Colima up at login,
which is what makes the platform survive a power cut.

State on disk, all under `~/.shelf` (0700), each file 0600:

```
bin/shelf                  the real binary; /usr/local/bin/shelf is a symlink to it
config.yaml                domain, host suffix, listen address, Colima profile, autostart
admin.yaml                 password hash, sessions, and the setup token until the claim
apps/<app>/secrets.yaml    the secret backup, unchanged since Phase 4
connections/cloudflare/    one file per Cloudflare connection: token and account (Phase 8b)
audit.log                  one line per mutating action
```

Plain files rather than the macOS keychain: keychain ACLs are bound to a code-signing identity,
so an unsigned binary replaced on every self-update would re-prompt — a dialog nobody is sitting
in front of on a headless machine. `SHELF_HOME` moves the whole tree, for anyone who wants it on
an encrypted volume.
