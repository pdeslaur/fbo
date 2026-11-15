# melange-update

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

Build dependencies pinned to an exact version in the build environment are
bumped to the latest version in its repositories, so that a package is rebuilt
when its toolchain changes:

```yaml
environment:
    contents:
        packages:
            - busybox
            - go-1.26=1.26.11-r0
```

Only `=` pins are bumped; unpinned packages and other constraints are left
alone. A pin moves only to a version that every architecture's APKINDEX has,
and it may name a package's provides, such as `rust`, which several
`rust-1.X` packages provide.

`package.epoch` is reset to 0 when the package version changes, and incremented
when only Go modules or pinned build dependencies change. A package's version
and epoch therefore identify everything it is built from.

The [update workflow](../../.github/workflows/update.yaml) runs it on each
image's `melange.yaml` daily. To run it by hand, from the repository's root:

```sh
GH_TOKEN=$(gh auth token) MELANGE_CONFIG=caddy-sso/melange.yaml go run ./cmd/melange-update
```
