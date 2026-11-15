// Command apk-repo maintains fbo's apk repository, an R2 bucket served at
// https://apk.philde.net, from a GitHub Actions job. It gets the bucket's
// credentials from cloudflare-sts with the job's OIDC token, so the job holds
// no long-lived key.
//
// The repository is os/<arch>/<name>-<version>.apk for each package, and
// os/<arch>/APKINDEX.tar.gz listing them, both signed with the KMS key whose
// public half is at the bucket's root.
//
// Usage:
//
//	apk-repo upload FILE...   add packages signed by apk-sign, from packages/<arch>/
//	apk-repo index            list new packages in each arch's index, and sign it
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/pdeslaur/fbo/tools/internal/apksign"
	"github.com/pdeslaur/fbo/tools/internal/r2"
	"github.com/sethvargo/go-envconfig"
)

type config struct {
	// Broker is cloudflare-sts's URL, which is also the audience of the
	// job's OIDC token.
	Broker string `env:"BROKER_URL,required"`
	// Scope is the broker's profile for the bucket.
	Scope string `env:"BROKER_SCOPE,required"`

	// The job's, with the id-token: write permission.
	TokenRequestURL   string `env:"ACTIONS_ID_TOKEN_REQUEST_URL"`
	TokenRequestToken string `env:"ACTIONS_ID_TOKEN_REQUEST_TOKEN"`

	// For index: the KMS key version to sign the index with, and its
	// public half, the PEM file apk's keyring holds.
	KeyVersion string `env:"KMS_KEY_VERSION"`
	PublicKey  string `env:"PUBLIC_KEY"`
}

func main() {
	ctx := context.Background()
	cfg := envconfig.MustProcess(ctx, &config{})
	if len(os.Args) < 2 {
		clog.FatalContext(ctx, "usage: apk-repo upload FILE... | apk-repo index")
	}

	token, err := r2.GitHubToken(ctx, cfg.TokenRequestURL, cfg.TokenRequestToken, cfg.Broker)
	if err != nil {
		clog.FatalContextf(ctx, "%v", err)
	}
	bucket, err := r2.Exchange(ctx, cfg.Broker, cfg.Scope, token)
	if err != nil {
		clog.FatalContextf(ctx, "%v", err)
	}
	st := r2.NewStore(bucket)

	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "upload":
		if len(args) == 0 {
			clog.FatalContext(ctx, "usage: apk-repo upload FILE...")
		}
		if err := upload(ctx, st, args); err != nil {
			clog.FatalContextf(ctx, "%v", err)
		}

	case "index":
		if cfg.KeyVersion == "" || cfg.PublicKey == "" {
			clog.FatalContext(ctx, "index needs KMS_KEY_VERSION and PUBLIC_KEY")
		}
		publicKey, err := os.ReadFile(cfg.PublicKey)
		if err != nil {
			clog.FatalContextf(ctx, "reading public key: %v", err)
		}
		signer, err := apksign.NewKMS(ctx, cfg.KeyVersion, publicKey)
		if err != nil {
			clog.FatalContextf(ctx, "%s: %v", cfg.PublicKey, err)
		}
		defer signer.Close()

		// Images and builds fetch the key from the bucket's root, as a
		// repository's keyring.
		keyName := filepath.Base(cfg.PublicKey)
		if _, err := st.PutIfAbsent(ctx, keyName, publicKey, "application/x-pem-file", indexCacheControl); err != nil {
			clog.FatalContextf(ctx, "%v", err)
		}
		for _, arch := range arches {
			if err := index(ctx, st, signer, keyName, publicKey, arch); err != nil {
				clog.FatalContextf(ctx, "%v", err)
			}
		}

	default:
		clog.FatalContextf(ctx, "unknown command %q: use upload or index", cmd)
	}
}
