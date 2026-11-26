# beeper-bridge

Runs one of [Beeper](https://www.beeper.com)'s self-hosted bridges with
[bbctl](https://github.com/beeper/bridge-manager), Beeper's bridge manager. It
replaces the upstream `ghcr.io/beeper/bridge-manager` image for the Go bridges:
it has no Python, so it can't run the Google Chat bridge or Heisenbridge, and
no lottieconverter, so animated stickers aren't converted.

Its entrypoint, [`run-bridge`](run-bridge), writes bbctl's config from the
environment like the upstream image's, then execs bbctl. bbctl is then the
container's PID 1, so it gets the stop signal and passes it on to the bridge,
where the upstream image's shell ignores it until the runtime kills the
container.

## Image

`ghcr.io/pdeslaur/beeper-bridge`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the bbctl version. See [Images](../README.md#images) for how
it's built and how to verify its signature.

## Usage

The argument is the bridge to run, such as `sh-signal`, after any `bbctl run`
flags. With the Beeper account's access token in `MATRIX_ACCESS_TOKEN`:

```sh
podman run -d \
    -e MATRIX_ACCESS_TOKEN=syt_... \
    -v signal:/data \
    ghcr.io/pdeslaur/beeper-bridge:latest sh-signal
```

bbctl downloads the bridge's latest build into `/data` each time it starts. The `mautrix-*` images instead bundle a bridge built from source, released versions only, which bbctl runs as it is.
The bridge keeps its data under `DATA_DIR`, `/data` by default, and its
database under `DB_DIR`, `/data/db` by default. `BEEPER_ENV` picks the Beeper
environment, `prod` by default.

bbctl gives the bridge 3 seconds to exit after the stop signal, then kills it
and exits with status 1.

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps bbctl to its
latest release and opens a pull request that merges once the build passes. See
[melange-update](../tools/cmd/melange-update) for how.
