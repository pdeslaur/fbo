# oci-spliit

OCI images for [Spliit Cloud](https://github.com/antonio-ivanovski/spliit-cloud) — an open-source, self-hosted expense splitter with accounts, sync, and more. Built from source using melange + apko on Wolfi.

## Images

| Image | Description |
|---|---|
| `ghcr.io/pdeslaur/oci-spliit` | Caddy proxy + web SPA |
| `ghcr.io/pdeslaur/oci-spliit-api` | Hono/tRPC API server (port 3001) |
| `ghcr.io/pdeslaur/oci-spliit-worker` | Background job worker |
| `ghcr.io/pdeslaur/oci-spliit-migrate` | One-shot Prisma migration runner |

Each is published for `linux/amd64` and `linux/arm64`, tagged `latest` and with
the Spliit Cloud version. They're built from [`melange.yaml`](melange.yaml) and
the `apko*.yaml` files by this repo's [build workflow](.github/workflows/build.yaml),
and rebuilt daily to pick up Wolfi fixes. Verify an image's signature with:

```sh
cosign verify ghcr.io/pdeslaur/oci-spliit:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/oci-spliit/.github/workflows/build.yaml@refs/heads/main
```

## Usage

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

## Network security

The API server enables `TRUST_PROXY`, which means it trusts client IP headers for rate limiting. To prevent IP spoofing:

**Strip incoming IP headers at the proxy (always recommended).** The Caddy config in this image already strips any client-supplied `X-Forwarded-For` and `X-Real-IP` before setting its own, so traffic routed through Caddy is safe.

**Isolate the API from untrusted networks.** The API binds to `0.0.0.0:3001` by default, so it is reachable on every interface — including any DMZ or public VLAN the host is on. Do not expose port 3001 outside the Docker network. Use an internal Docker network to ensure only the Caddy sidecar can reach the API:

```yaml
networks:
  internal:
    internal: true   # no external routing

services:
  api:
    networks: [internal]
  worker:
    networks: [internal]
  web:
    networks: [internal]
    ports:
      - "127.0.0.1:3000:80"   # only Caddy faces the outside
  db:
    networks: [internal]
```

**Single-host / Kubernetes pod deployments.** If Caddy and the API run on the same host or in the same pod (sharing a loopback), set `BIND_HOST=127.0.0.1` on the API container so it only listens on the loopback interface. Do not set this in a multi-container Docker Compose setup where Caddy and the API are separate containers — they communicate over the Docker bridge, not loopback.

## Configuration

The API reads all feature flags and secrets at runtime — nothing is baked into the image. See [env.ts](https://github.com/antonio-ivanovski/spliit-cloud/blob/main/apps/api/src/lib/env.ts) for the full schema.

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | PostgreSQL connection URL |
| `BETTER_AUTH_SECRET` | yes (production) | Auth secret, min 32 bytes |
| `APP_URL` | yes | Public HTTPS origin (sets `WEB_ORIGINS` and `BETTER_AUTH_URL`) |
| `SMTP_HOST` + `EMAIL_FROM` | yes (email auth) | SMTP config for sending emails |
| `EMAIL_UNSUBSCRIBE_SECRET` | yes (email auth) | Unsubscribe HMAC secret, min 32 bytes |
| `BIND_HOST` | no | Interface the API listens on (default `0.0.0.0`). Set to `127.0.0.1` on single-host / pod deployments; leave unset for Docker Compose (see [Network security](#network-security)) |

The web SPA bakes `VITE_DEFAULT_CURRENCY_CODE=USD` at build time. All `PUBLIC_ENABLE_*` feature flags are runtime API variables.

## Updates

A daily [update workflow](.github/workflows/update.yaml) bumps Spliit Cloud to its latest release and opens a pull request that merges once the build passes. See [oci-common](https://github.com/pdeslaur/oci-common#melange-update) for how.
