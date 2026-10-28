# shelf-hello

An example tenant of [shelf](https://github.com/tweinmann/shelf). Every push to `main` builds
`web` as `ghcr.io/tweinmann/greeter/web`, and the shelf workflow publishes the deploy artifact
`ghcr.io/tweinmann/greeter`.

Add the app to a shelf cluster once:

```sh
shelf app add greeter oci://ghcr.io/tweinmann/greeter:main
```

From then on, the platform rolls out every push by itself, usually within two minutes. The
`name` in `app.yaml` only names the packages; the app's name is chosen when it is added, so the
same artifact can run more than once:

```sh
shelf app add greeter-staging oci://ghcr.io/tweinmann/greeter:main
```

## Coding agents

`CLAUDE.md` and the skill in `.claude/skills/shelf/` tell Claude Code how to work with shelf in
this repository: after a push, wait for the verdict on that commit with

```sh
shelf app status greeter --wait --commit "$(git rev-parse HEAD)" --json
```

and fix what it names; on a branch with `--preview <branch>`, if the operator switched previews
on (`shelf app credentials greeter --previews`). Put the name you gave the app in
`shelf app add` into `CLAUDE.md`. The
agent cannot make the app public: exposure is set with `shelf app add` or
`shelf app credentials`, never in this repository.
