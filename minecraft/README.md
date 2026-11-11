# oci-minecraft

The [Minecraft: Java Edition](https://www.minecraft.net) server, running on
OpenJDK. It packages Mojang's server jar, which is proprietary and subject to
the [Minecraft EULA](https://www.minecraft.net/eula).

## Image

`ghcr.io/pdeslaur/oci-minecraft`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Minecraft version. It's built from
[`melange.yaml`](melange.yaml) and [`apko.yaml`](apko.yaml) on
[Wolfi](https://wolfi.dev) by [oci-common](https://github.com/pdeslaur/oci-common)'s
shared workflow, and rebuilt daily to pick up Wolfi fixes. Verify its signature
with:

```sh
cosign verify ghcr.io/pdeslaur/oci-minecraft:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/oci-common/.github/workflows/build.yaml@refs/heads/main \
    --certificate-github-workflow-repository pdeslaur/oci-minecraft
```

## Usage

The server keeps the world, `server.properties` and other state in `/data`. It
won't start until you accept the EULA:

```sh
mkdir -p data
echo eula=true > data/eula.txt
podman run -d \
    -e JAVA_TOOL_OPTIONS=-Xmx4G \
    -v ./data:/data \
    -p 25565:25565 \
    ghcr.io/pdeslaur/oci-minecraft:latest
```

Pass JVM options, such as the heap size, with `JAVA_TOOL_OPTIONS`.

## Updates

A daily [update workflow](.github/workflows/update.yaml) bumps Minecraft to the
latest release and opens a pull request that merges once the build passes. See
[oci-common](https://github.com/pdeslaur/oci-common#melange-update) for how.

The Java version is pinned in `melange.yaml`. If a release needs a newer one,
the package test fails and the pull request waits for that to be bumped by hand.
