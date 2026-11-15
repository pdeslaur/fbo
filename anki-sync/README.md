# anki-sync

The [Anki](https://apps.ankiweb.net) sync server, for syncing Anki and AnkiDroid
through your own server instead of AnkiWeb. It's built from the
[Anki source](https://github.com/ankitects/anki).

## Image

`ghcr.io/pdeslaur/anki-sync`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Anki version. See [Images](../README.md#images) for how
it's built and how to verify its signature.

## Usage

The server is configured with environment variables. `SYNC_USER1` (and
`SYNC_USER2`, and so on) adds a user as `username:password`:

```sh
podman run -d \
    -e SYNC_USER1=user:password \
    -e SYNC_BASE=/data \
    -e SYNC_PORT=8080 \
    -v anki:/data \
    -p 8080:8080 \
    ghcr.io/pdeslaur/anki-sync:latest
```

Then point Anki's sync server setting at `http://<host>:8080/`. See Anki's
[sync server documentation](https://docs.ankiweb.net/sync-server.html) for the
other options.

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps Anki to its
latest release and opens a pull request that merges once the build passes. See
[melange-update](../tools/cmd/melange-update) for how.
