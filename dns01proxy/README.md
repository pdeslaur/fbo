# dns01proxy

[dns01proxy](https://github.com/liujed/dns01proxy), a server that solves ACME
DNS-01 challenges on behalf of clients such as Caddy's
[acmeproxy](https://github.com/caddy-dns/acmeproxy) module, so that only the
proxy holds DNS provider credentials. This build includes every
[caddy-dns](https://github.com/caddy-dns) provider.

## Image

`ghcr.io/pdeslaur/dns01proxy`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the dns01proxy version. See [Images](../README.md#images) for
how it's built and how to verify its signature.

## Usage

The entrypoint is `dns01proxy`. With a `config.toml` such as:

```toml
hostnames = ["dns01proxy.example.com"]
listen = [":443"]

[dns.provider]
name = "cloudflare"
api_token = "{env.CF_API_TOKEN}"

[[accounts]]
user_id = "caddy"
password = "<bcrypt hash>"
allow_domains = ["app.example.com"]
```

run:

```sh
podman run -d \
    -e XDG_DATA_HOME=/data \
    -e CF_API_TOKEN=... \
    -v ./config.toml:/etc/dns01proxy/config.toml:ro \
    -v dns01proxy:/data \
    -p 443:443 \
    ghcr.io/pdeslaur/dns01proxy:latest run --config /etc/dns01proxy/config.toml
```

dns01proxy keeps its own certificate under `$XDG_DATA_HOME`.

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps dns01proxy to
its latest release and opens a pull request that merges once the build passes.
See [melange-update](../cmd/melange-update) for how.
