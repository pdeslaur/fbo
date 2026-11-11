# oci-caddy

[Caddy](https://caddyserver.com) built with two extra modules:

- [caddy-dns/acmeproxy](https://github.com/caddy-dns/acmeproxy), to solve ACME
  DNS-01 challenges through a proxy such as
  [oci-dns01proxy](https://github.com/pdeslaur/oci-dns01proxy) rather than
  holding DNS provider credentials.
- [caddy-pocketid](https://github.com/pdeslaur/caddy-pocketid), which adds the
  `pocketid_auth` directive for single sign-on with
  [Pocket ID](https://pocket-id.org).

## Image

`ghcr.io/pdeslaur/oci-caddy`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Caddy version. It's built from [`melange.yaml`](melange.yaml)
and [`apko.yaml`](apko.yaml) on [Wolfi](https://wolfi.dev) by
[oci-common](https://github.com/pdeslaur/oci-common)'s shared workflow, and
rebuilt daily to pick up Wolfi fixes. Verify its signature with:

```sh
cosign verify ghcr.io/pdeslaur/oci-caddy:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/oci-common/.github/workflows/build.yaml@refs/heads/main \
    --certificate-github-workflow-repository pdeslaur/oci-caddy
```

## Usage

The entrypoint is `caddy run`, so pass the config with `--config`. Caddy keeps
certificates under `$XDG_DATA_HOME/caddy`, so point that at a volume:

```sh
podman run -d \
    -e XDG_DATA_HOME=/data \
    -v ./Caddyfile:/etc/caddy/Caddyfile:ro \
    -v caddy:/data \
    -p 443:443 \
    ghcr.io/pdeslaur/oci-caddy:latest --config /etc/caddy/Caddyfile
```

For other subcommands, replace the entrypoint:

```sh
podman run --rm --entrypoint caddy ghcr.io/pdeslaur/oci-caddy:latest list-modules
```

## Updates

A daily [update workflow](.github/workflows/update.yaml) bumps Caddy and the two
modules to their latest releases and opens a pull request that merges once the
build passes. See [oci-common](https://github.com/pdeslaur/oci-common#melange-update)
for how.
