# minecraft

The [Minecraft: Java Edition](https://www.minecraft.net) server, running on
OpenJDK. It packages Mojang's server jar, which is proprietary and subject to
the [Minecraft EULA](https://www.minecraft.net/eula).

## Image

`ghcr.io/pdeslaur/minecraft`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Minecraft version. See [Images](../README.md#images) for
how it's built and how to verify its signature.

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
    ghcr.io/pdeslaur/minecraft:latest
```

Pass JVM options, such as the heap size, with `JAVA_TOOL_OPTIONS`.

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps Minecraft to
the latest release and opens a pull request that merges once the build passes.
See [melange-update](../cmd/melange-update) for how.

The Java version is pinned in `melange.yaml`. If a release needs a newer one,
the package test fails and the pull request waits for that to be bumped by hand.
