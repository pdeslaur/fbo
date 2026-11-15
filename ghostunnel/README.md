# ghostunnel

[ghostunnel](https://github.com/ghostunnel/ghostunnel), a TLS proxy with
mutual authentication. It can take its certificates from a
[SPIFFE](https://spiffe.io) Workload API and authorize peers by SPIFFE ID,
which makes it a sidecar for mTLS between services. This build leaves out
PKCS#11 and the macOS and Windows keychains, which need cgo.

## Image

`ghcr.io/pdeslaur/ghostunnel`, for `linux/amd64` and `linux/arm64`, tagged
`latest` and with the ghostunnel version. It runs as `nonroot` (uid 65532). See
[Images](../README.md#images) for how it's built and how to verify its
signature.

## Usage

The entrypoint is `ghostunnel`. With a SPIFFE agent's Workload API socket in
`/run/spire`, a server accepting only `spiffe://example.org/client` and
forwarding to a local app:

```sh
podman run -d \
    -v /run/spire:/run/spire:ro \
    -p 8443:8443 \
    ghcr.io/pdeslaur/ghostunnel:latest server \
    --use-workload-api-addr unix:///run/spire/api.sock \
    --listen 0.0.0.0:8443 \
    --target 127.0.0.1:8080 \
    --allow-uri spiffe://example.org/client
```

and a client, listening locally, that only connects to that server:

```sh
podman run -d \
    -v /run/spire:/run/spire:ro \
    ghcr.io/pdeslaur/ghostunnel:latest client \
    --use-workload-api-addr unix:///run/spire/api.sock \
    --listen 127.0.0.1:8080 \
    --target server.example.org:8443 \
    --verify-uri spiffe://example.org/server
```

## Updates

A daily [update workflow](../.github/workflows/update.yaml) bumps ghostunnel to
its latest release and opens a pull request that merges once the build passes.
See [melange-update](../tools/cmd/melange-update) for how.
