# fbo

Container images built with [melange](https://github.com/chainguard-dev/melange)
and [apko](https://github.com/chainguard-dev/apko) on [Wolfi](https://wolfi.dev),
published to `ghcr.io/pdeslaur/<image>`.

| Image | |
|---|---|
| [anki-sync](anki-sync) | The Anki sync server. |
| [beeper-bridge](beeper-bridge) | A Beeper self-hosted bridge, run by bbctl. |
| [caddy-sso](caddy-sso) | Caddy with the cloudflare-sts DNS provider and Pocket ID single sign-on. |
| [dns01proxy](dns01proxy) | Solves ACME DNS-01 challenges for clients, with every caddy-dns provider. |
| [factorio](factorio) | The Factorio headless server. |
| [ghostunnel](ghostunnel) | A TLS proxy with mutual authentication, including SPIFFE. |
| [mautrix-discord](mautrix-discord) | The Matrix-Discord bridge, run by bbctl for Beeper. |
| [mautrix-linkedin](mautrix-linkedin) | The Matrix-LinkedIn bridge, run by bbctl for Beeper. |
| [mautrix-meta](mautrix-meta) | The Matrix bridges for Facebook Messenger and Instagram, run by bbctl for Beeper, as `mautrix-meta` and `mautrix-meta-instagram`. |
| [mautrix-signal](mautrix-signal) | The Matrix-Signal bridge, run by bbctl for Beeper. |
| [mautrix-telegram](mautrix-telegram) | The Matrix-Telegram bridge, run by bbctl for Beeper. |
| [mautrix-whatsapp](mautrix-whatsapp) | The Matrix-WhatsApp bridge, run by bbctl for Beeper. |
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

Pull requests build and test the packages of the directories they change, or of
every directory when they change the build workflow. On `main`, a package is
built only when the repository doesn't have its version-epoch, and a pull
request that changes a package without bumping its epoch fails. Every image is
rebuilt daily, to pick up Wolfi's fixes to the packages it installs. Toolchain
fixes come as pull requests bumping the pinned build dependencies. melange
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
this workflow, on `main`, may do either.

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
