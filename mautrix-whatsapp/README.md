# mautrix-whatsapp

[mautrix-whatsapp](https://github.com/mautrix/whatsapp), the Matrix bridge for WhatsApp, built from source and run by [bbctl](https://github.com/beeper/bridge-manager) as one of [Beeper](https://www.beeper.com)'s self-hosted bridges.

## Image

`ghcr.io/pdeslaur/mautrix-whatsapp`, for `linux/amd64` and `linux/arm64`, tagged `latest` and with the bridge's version. See [Images](../README.md#images) for how it's built and how to verify its signature.

The image is [beeper-bridge](../beeper-bridge)'s with the bridge added: bbctl runs it from `BEEPER_BRIDGE_CUSTOM_STARTUP_COMMAND` rather than downloading mautrix's latest build. Usage is the same, with this bridge's name:

```sh
podman run -d \
    -e MATRIX_ACCESS_TOKEN=syt_... \
    -v whatsapp:/data \
    ghcr.io/pdeslaur/mautrix-whatsapp:latest sh-whatsapp
```

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps the bridge to its latest release and opens a pull request that merges once the build passes. See [melange-update](../tools/cmd/melange-update) for how.
