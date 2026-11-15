# fbo

Container images built with [melange](https://github.com/chainguard-dev/melange)
and [apko](https://github.com/chainguard-dev/apko) on [Wolfi](https://wolfi.dev),
published to `ghcr.io/pdeslaur/<image>`.

| Image | |
|---|---|
| [anki-sync](anki-sync) | The Anki sync server. |
| [caddy-sso](caddy-sso) | Caddy with the ACME proxy DNS provider and Pocket ID single sign-on. |
| [dns01proxy](dns01proxy) | Solves ACME DNS-01 challenges for clients, with every caddy-dns provider. |
| [factorio](factorio) | The Factorio headless server. |
| [ghostunnel](ghostunnel) | A TLS proxy with mutual authentication, including SPIFFE. |
| [minecraft](minecraft) | The Minecraft: Java Edition server. |
| [spliit](spliit) | Spliit Cloud, as `spliit`, `spliit-api`, `spliit-migrate` and `spliit-worker`. |

Each directory holds a `melange.yaml`, building one package and its
subpackages, and the `apko*.yaml` files of the images installing them, each
documented in the directory's README. `tools/` is the Go module of the commands
the workflows run, under `tools/cmd/`.

## Images

The [build workflow](.github/workflows/build.yaml) builds each directory's
package for `x86_64` and `aarch64` and runs its tests. Then it publishes each
`apko.yaml` as `ghcr.io/pdeslaur/<directory>` and each `apko-<suffix>.yaml` as
`ghcr.io/pdeslaur/<directory>-<suffix>`, for `linux/amd64` and `linux/arm64`,
tagged `latest` and with the package version, and signs them with
[cosign](https://github.com/sigstore/cosign) keyless signing. Verify one with:

```sh
cosign verify ghcr.io/pdeslaur/<image>:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/fbo/.github/workflows/build.yaml@refs/heads/main
```

Pull requests and pushes build only the directories they change, or every
directory when they change the build workflow. It rebuilds every image daily,
without its package cache, to pick up Wolfi package and toolchain fixes. melange
runs in the `cgr.dev/chainguard/melange:latest-dev` container, pinned by digest,
so builds don't depend on the runner's apt mirrors.

## Package repository

melange signs packages with a throwaway key, which their tests use. On `main`,
[apk-sign](tools/cmd/apk-sign) then replaces their signatures with ones by the
`apk/apk-signing` Cloud KMS key in the `philde-net` project, whose public half
is [apk.philde.net.rsa.pub](apk.philde.net.rsa.pub), and
[apk-repo](tools/cmd/apk-repo) adds them to the apk repository at
`https://apk.philde.net/os`, an R2 bucket, and lists them in each arch's
signed `APKINDEX.tar.gz`. The images are built from that repository, trusting
the KMS key alone.

A package's version-epoch is immutable: the repository keeps the first one it
gets, and never replaces it.

The workflow holds no long-lived credential. It reaches the private key, which
never leaves KMS, through workload identity federation with its GitHub OIDC
token, and gets temporary credentials for the bucket from
[cloudflare-sts](https://github.com/pdeslaur/cloudflare-sts) with the same token. Only
this workflow, on `main`, may do either. home-infra manages the key, the bucket
and the broker.

## Updates

The [update workflow](.github/workflows/update.yaml) runs
[melange-update](tools/cmd/melange-update) daily on each directory's `melange.yaml`,
bumping the package and the Go modules it pins to their latest releases. Each
package with an update gets a pull request on `update/<package>`, with
auto-merge enabled, so it merges once the build passes. While that pull request
is open, later runs update it in place.

The pull requests are opened with an [octo-sts](https://github.com/octo-sts/app)
token, as allowed by [`.github/chainguard/update.sts.yaml`](.github/chainguard/update.sts.yaml),
so that they trigger the build.

[Dependabot](.github/dependabot.yml) checks for GitHub Actions and Go module
updates weekly. Its pull requests get auto-merge from the
[Dependabot workflow](.github/workflows/dependabot.yaml), through octo-sts like
the update workflow's, so they merge once the checks pass.
