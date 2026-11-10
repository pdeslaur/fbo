# oci-spliit

OCI images for [Spliit Cloud](https://github.com/antonio-ivanovski/spliit-cloud) — an open-source, self-hosted expense splitter with accounts, sync, and more. Built from source using melange + apko on Wolfi.

Four images are published from this repo:

| Image | Description |
|---|---|
| `ghcr.io/pdeslaur/oci-spliit` | Caddy proxy + web SPA |
| `ghcr.io/pdeslaur/oci-spliit-api` | Hono/tRPC API server (port 3001) |
| `ghcr.io/pdeslaur/oci-spliit-worker` | Background job worker |
| `ghcr.io/pdeslaur/oci-spliit-migrate` | One-shot Prisma migration runner |

## Deployment

The images follow the same service layout as the [upstream compose.yaml](https://github.com/antonio-ivanovski/spliit-cloud/blob/main/compose.yaml). The web container proxies API traffic to a service named `api` on port 3001.

```yaml
services:
  migrate:
    image: ghcr.io/pdeslaur/oci-spliit-migrate:latest
    restart: "no"
    environment:
      DATABASE_URL: postgresql://spliit:pass@db:5432/spliit
    depends_on:
      db:
        condition: service_healthy

  api:
    image: ghcr.io/pdeslaur/oci-spliit-api:latest
    restart: unless-stopped
    environment:
      DATABASE_URL: postgresql://spliit:pass@db:5432/spliit
      BETTER_AUTH_SECRET: <openssl rand -hex 32>
      APP_URL: https://spliit.example.com
      SMTP_HOST: smtp.example.com
      EMAIL_FROM: spliit@example.com
      EMAIL_UNSUBSCRIBE_SECRET: <openssl rand -hex 32>
    depends_on:
      migrate:
        condition: service_completed_successfully

  worker:
    image: ghcr.io/pdeslaur/oci-spliit-worker:latest
    restart: unless-stopped
    environment:
      DATABASE_URL: postgresql://spliit:pass@db:5432/spliit
      BETTER_AUTH_SECRET: <same as api>
    depends_on:
      migrate:
        condition: service_completed_successfully

  web:
    image: ghcr.io/pdeslaur/oci-spliit:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:3000:80"
    depends_on:
      api:
        condition: service_healthy
```

See the [upstream deployment docs](https://github.com/antonio-ivanovski/spliit-cloud/blob/main/docs/deployment.md) for the full configuration reference.

## Configuration

The API reads all feature flags and secrets at runtime — nothing is baked into the image. See [env.ts](https://github.com/antonio-ivanovski/spliit-cloud/blob/main/apps/api/src/lib/env.ts) for the full schema.

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | PostgreSQL connection URL |
| `BETTER_AUTH_SECRET` | yes (production) | Auth secret, min 32 bytes |
| `APP_URL` | yes | Public HTTPS origin (sets `WEB_ORIGINS` and `BETTER_AUTH_URL`) |
| `SMTP_HOST` + `EMAIL_FROM` | yes (email auth) | SMTP config for sending emails |
| `EMAIL_UNSUBSCRIBE_SECRET` | yes (email auth) | Unsubscribe HMAC secret, min 32 bytes |

The web SPA bakes `VITE_DEFAULT_CURRENCY_CODE=USD` at build time. All `PUBLIC_ENABLE_*` feature flags are runtime API variables.

## Updates

The [update workflow](.github/workflows/update.yaml) runs daily and opens an auto-merging pull request when Spliit Cloud publishes a new release.
