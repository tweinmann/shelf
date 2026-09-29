# shelf-hello

An example tenant of [shelf](https://github.com/tweinmann/shelf). Every push to `main` builds
`web` as `ghcr.io/tweinmann/greeter/web`, and the shelf workflow publishes the deploy artifact
`ghcr.io/tweinmann/greeter`.

Add the app to a shelf cluster once:

```sh
shelf app add greeter oci://ghcr.io/tweinmann/greeter:main
```

From then on, the platform rolls out every push by itself, usually within two minutes.
