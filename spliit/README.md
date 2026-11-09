# oci-spliit

OCI image for [spliit](https://github.com/spliit-app/spliit) — free, open-source alternative to Splitwise. Built from source using melange + apko on Wolfi.

## Deployment

The image is published to `ghcr.io/pdeslaur/oci-spliit`. It requires a PostgreSQL database; spliit runs migrations automatically at container startup via `prisma migrate deploy`.

```
podman run -d --restart=always \
  -e POSTGRES_PRISMA_URL="postgresql://user:pass@host:5432/spliit" \
  -e POSTGRES_URL_NON_POOLING="postgresql://user:pass@host:5432/spliit" \
  -e BASE_URL="http://spliit.local:3000" \
  -p 3000:3000 \
  ghcr.io/pdeslaur/oci-spliit:latest
```

## Configuration

| Variable | Required | Description |
|---|---|---|
| `POSTGRES_PRISMA_URL` | yes | Pooled PostgreSQL connection URL |
| `POSTGRES_URL_NON_POOLING` | yes | Direct PostgreSQL connection URL (used by migrations) |
| `BASE_URL` | yes | Public URL of the app (e.g. `http://spliit.local:3000`) |
| `PORT` | no | HTTP listen port (default: `3000`) |

Optional features are disabled by default (the `NEXT_PUBLIC_ENABLE_*` flags are baked in at build time). Receipt extraction via OpenAI and document storage via S3 are not enabled in this image.

## Updates

The [update workflow](.github/workflows/update.yaml) runs daily and opens an auto-merging pull request when spliit publishes a new release.
