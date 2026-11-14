# factorio

The [Factorio](https://factorio.com) headless server. It packages Wube's
prebuilt server binaries, which are proprietary.

## Image

`ghcr.io/pdeslaur/factorio`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Factorio version. See [Images](../README.md#images) for
how it's built and how to verify its signature.

## Usage

The server reads its settings from `/factorio/config/server-settings.json`.
Start from the example that ships with Factorio:

```sh
mkdir -p config saves mods
podman run --rm --entrypoint cat ghcr.io/pdeslaur/factorio:latest \
    /factorio/data/server-settings.example.json > config/server-settings.json
```

Then run:

```sh
podman run -d -i \
    -v ./saves:/factorio/saves \
    -v ./config:/factorio/config \
    -v ./mods:/factorio/mods \
    -p 34197:34197/udp \
    ghcr.io/pdeslaur/factorio:latest
```

- The server creates a new map at `/factorio/saves/save.zip` if there isn't one.
- The example settings list the game publicly, which needs your factorio.com
  `username` and `token`. Without them, set `visibility.public` to `false`.
- Keep stdin open with `-i`: the server shuts down when stdin closes.
- `SAVE_FILE` and `CONFIG_FILE` override the save and settings paths.

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps Factorio to
the latest stable release and opens a pull request that merges once the build
passes. See [melange-update](../cmd/melange-update) for how.
