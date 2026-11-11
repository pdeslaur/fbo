# oci-common

Shared GitHub Actions workflows and tooling for the melange + apko image
repositories:

- [oci-caddy](https://github.com/pdeslaur/oci-caddy)
- [oci-anki-sync](https://github.com/pdeslaur/oci-anki-sync)
- [oci-dns01proxy](https://github.com/pdeslaur/oci-dns01proxy)
- [oci-factorio](https://github.com/pdeslaur/oci-factorio)
- [oci-minecraft](https://github.com/pdeslaur/oci-minecraft)

## Workflows

### `build.yaml`

Builds `melange.yaml` for `x86_64` and `aarch64`, runs the package's `test:`
pipeline, and, when `publish: true`, publishes `apko.yaml` to
`ghcr.io/<repository>` tagged `latest` and `<package version>` and signs it
with [cosign](https://github.com/sigstore/cosign) keyless signing. Scheduled
runs bypass the package cache so that Wolfi toolchain fixes are picked up.
melange runs in the `cgr.dev/chainguard/melange:latest-dev` container, pinned
by digest, so builds don't depend on the runner's apt mirrors.

```yaml
jobs:
    build:
        uses: pdeslaur/oci-common/.github/workflows/build.yaml@main
        with:
            publish: ${{ github.event_name != 'pull_request' }}
```

The caller must grant `contents: read`, `id-token: write` and `packages: write`.

Images are signed by this reusable workflow, so verify them with:

```sh
cosign verify ghcr.io/pdeslaur/oci-caddy:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/oci-common/.github/workflows/build.yaml@refs/heads/main \
    --certificate-github-workflow-repository pdeslaur/oci-caddy
```

### `update.yaml`

Runs [`melange-update`](./cmd/melange-update) to bump `melange.yaml` and the Go
modules it pins to their latest releases, then opens a pull request on
`update/<package>` with auto-merge enabled. While that pull request is open,
later runs update it in place.

```yaml
jobs:
    update:
        uses: pdeslaur/oci-common/.github/workflows/update.yaml@main
```

The caller must grant `contents: read` and `id-token: write`, and the repository
needs:

- The [octo-sts](https://github.com/apps/octo-sts) app installed.
- A `.github/chainguard/update.sts.yaml` trust policy granting
  `contents: write` and `pull_requests: write` to this workflow.
- Auto-merge enabled and a ruleset requiring the build checks, so that
  auto-merge waits for them.

## `melange-update`

Reads the upstream repository and tag format from the `git-checkout` step of a
melange config, and if the latest GitHub release is newer, updates
`package.version` and `expected-commit`.

Packages that download upstream artifacts with `fetch` steps instead of using
`git-checkout` describe how to find new releases with `update-*` vars. melange
rejects unknown config fields, so these live in `vars:`. Each one except
`update-url` is a [jq](https://jqlang.org) query over the JSON document at
`update-url`:

| Var | Query result |
| --- | --- |
| `update-url` | (a URL, not a query) JSON document describing releases. |
| `update-version` | The latest version. |
| `update-var-NAME` | The value of var `NAME` for the latest version. Optional. |
| `update-release-notes` | A release notes URL for the latest version. Optional. |

All queries except `update-version` see the latest version as `$version` and can
call `fetch_json` to follow a URL to another JSON document. Each query must
produce exactly one non-empty string. Since melange substitutes `${{...}}` in
var values, build URLs in jq, e.g. `"https://example.com/" + $version`.

When a newer version exists, `package.version` and the `update-var-*` vars are
updated, and each fetch step's `expected-sha256` is recomputed by downloading
its `uri` with `${{package.version}}` and `${{vars.NAME}}` substituted. For
example, the Minecraft server's download URL embeds a hash rather than the
version, so it's resolved through Mojang's version manifest:

```yaml
vars:
    server-jar: https://piston-data.mojang.com/v1/objects/33680f.../server.jar
    update-url: https://piston-meta.mojang.com/mc/game/version_manifest_v2.json
    update-version: .latest.release
    update-var-server-jar: '.versions[] | select(.id == $version) | .url | fetch_json | .downloads.server.url'
    update-release-notes: '"https://minecraft.wiki/w/Java_Edition_" + $version'

pipeline:
    - uses: fetch
      with:
          uri: ${{vars.server-jar}}
          expected-sha256: d052f1...
```

It also bumps Go modules pinned in pipeline steps with `go get MODULE@VERSION`,
such as Caddy plugins, to the latest release reported by the Go module proxy.
This stays within the module's major version and skips prereleases.

`package.epoch` is reset to 0 when the package version changes, and incremented
when only Go modules change.

```sh
GH_TOKEN=$(gh auth token) MELANGE_CONFIG=path/to/melange.yaml go run ./cmd/melange-update
```
