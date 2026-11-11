# oci-anki-sync

The [Anki](https://apps.ankiweb.net) sync server, for syncing Anki and AnkiDroid
through your own server instead of AnkiWeb. It's built from the
[Anki source](https://github.com/ankitects/anki).

## Image

`ghcr.io/pdeslaur/oci-anki-sync`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Anki version. It's built from [`melange.yaml`](melange.yaml)
and [`apko.yaml`](apko.yaml) on [Wolfi](https://wolfi.dev) by
[oci-common](https://github.com/pdeslaur/oci-common)'s shared workflow, and
rebuilt daily to pick up Wolfi fixes. Verify its signature with:

```sh
cosign verify ghcr.io/pdeslaur/oci-anki-sync:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/oci-common/.github/workflows/build.yaml@refs/heads/main \
    --certificate-github-workflow-repository pdeslaur/oci-anki-sync
```

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
    ghcr.io/pdeslaur/oci-anki-sync:latest
```

Then point Anki's sync server setting at `http://<host>:8080/`. See Anki's
[sync server documentation](https://docs.ankiweb.net/sync-server.html) for the
other options.

## Updates

A daily [update workflow](.github/workflows/update.yaml) bumps Anki to its latest
release and opens a pull request that merges once the build passes. See
[oci-common](https://github.com/pdeslaur/oci-common#melange-update) for how.
