// Command apk-sign re-signs melange packages and their APKINDEX.tar.gz files
// with a Cloud KMS key, so that the private key never leaves KMS.
//
// melange builds and signs with a throwaway key; apk-sign then replaces each
// file's signature with one by the KMS key version, named after the public
// key file that apk's keyring holds. It refuses to sign unless KMS's public
// key is that file's, and verifies every signature before writing it.
//
// Usage: apk-sign FILE...
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/pdeslaur/fbo/tools/internal/apksign"
	"github.com/sethvargo/go-envconfig"
)

type config struct {
	// KeyVersion is the KMS key version to sign with, e.g.
	// projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoKeyVersions/1.
	KeyVersion string `env:"KMS_KEY_VERSION,required"`
	// PublicKey is the PEM file apk's keyring holds. Its name names the
	// signatures.
	PublicKey string `env:"PUBLIC_KEY,required"`
}

func main() {
	ctx := context.Background()
	cfg := envconfig.MustProcess(ctx, &config{})
	files := os.Args[1:]
	if len(files) == 0 {
		clog.FatalContext(ctx, "usage: apk-sign FILE...")
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

	keyName := filepath.Base(cfg.PublicKey)
	for _, f := range files {
		if err := apksign.ResignFile(ctx, f, signer, keyName, publicKey); err != nil {
			clog.FatalContextf(ctx, "%v", err)
		}
		clog.InfoContext(ctx, "signed", "file", f, "key", keyName)
	}
}
