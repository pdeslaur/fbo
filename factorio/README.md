# oci-factorio

The [Factorio](https://factorio.com) headless server. It packages Wube's
prebuilt server binaries, which are proprietary.

## Image

`ghcr.io/pdeslaur/oci-factorio`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Factorio version. It's built from
[`melange.yaml`](melange.yaml) and [`apko.yaml`](apko.yaml) on
[Wolfi](https://wolfi.dev) by [oci-common](https://github.com/pdeslaur/oci-common)'s
shared workflow, and rebuilt daily to pick up Wolfi fixes. Verify its signature
with:

```sh
cosign verify ghcr.io/pdeslaur/oci-factorio:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/oci-common/.github/workflows/build.yaml@refs/heads/main \
    --certificate-github-workflow-repository pdeslaur/oci-factorio
```

## Usage

The server reads its settings from `/factorio/config/server-settings.json`.
Start from the example that ships with Factorio:

```sh
mkdir -p config saves mods
podman run --rm --entrypoint cat ghcr.io/pdeslaur/oci-factorio:latest \
    /factorio/data/server-settings.example.json > config/server-settings.json
```

Then run:

```sh
podman run -d -i \
    -v ./saves:/factorio/saves \
    -v ./config:/factorio/config \
    -v ./mods:/factorio/mods \
    -p 34197:34197/udp \
    ghcr.io/pdeslaur/oci-factorio:latest
```

- The server creates a new map at `/factorio/saves/save.zip` if there isn't one.
- The example settings list the game publicly, which needs your factorio.com
  `username` and `token`. Without them, set `visibility.public` to `false`.
- Keep stdin open with `-i`: the server shuts down when stdin closes.
- `SAVE_FILE` and `CONFIG_FILE` override the save and settings paths.

## Updates

A daily [update workflow](.github/workflows/update.yaml) bumps Factorio to the
latest stable release and opens a pull request that merges once the build
passes. See [oci-common](https://github.com/pdeslaur/oci-common#melange-update)
for how.
