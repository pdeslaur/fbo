# caddy-sso

[Caddy](https://caddyserver.com) built with two extra modules:

- [caddy-dns/acmeproxy](https://github.com/caddy-dns/acmeproxy), to solve ACME
  DNS-01 challenges through a proxy such as [dns01proxy](../dns01proxy)
  rather than holding DNS provider credentials.
- [caddy-pocketid](https://github.com/pdeslaur/caddy-pocketid), which adds the
  `pocketid_auth` directive for single sign-on with
  [Pocket ID](https://pocket-id.org).

## Image

`ghcr.io/pdeslaur/caddy-sso`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the Caddy version. See [Images](../README.md#images) for how
it's built and how to verify its signature.

## Usage

The entrypoint is `caddy run`, so pass the config with `--config`. Caddy keeps
certificates under `$XDG_DATA_HOME/caddy`, so point that at a volume:

```sh
podman run -d \
    -e XDG_DATA_HOME=/data \
    -v ./Caddyfile:/etc/caddy/Caddyfile:ro \
    -v caddy:/data \
    -p 443:443 \
    ghcr.io/pdeslaur/caddy-sso:latest --config /etc/caddy/Caddyfile
```

For other subcommands, replace the entrypoint:

```sh
podman run --rm --entrypoint caddy ghcr.io/pdeslaur/caddy-sso:latest list-modules
```

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps Caddy and the
two modules to their latest releases and opens a pull request that merges once
the build passes. See [melange-update](../tools/cmd/melange-update) for how.
