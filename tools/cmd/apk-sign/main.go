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
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/chainguard-dev/clog"
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
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		clog.FatalContextf(ctx, "creating KMS client: %v", err)
	}
	defer client.Close()

	s := kmsSigner{client: client, keyVersion: cfg.KeyVersion}
	if err := s.checkPublicKey(ctx, publicKey); err != nil {
		clog.FatalContextf(ctx, "%s: %v", cfg.PublicKey, err)
	}

	keyName := filepath.Base(cfg.PublicKey)
	for _, f := range files {
		if err := resign(ctx, f, s, keyName, publicKey); err != nil {
			clog.FatalContextf(ctx, "%v", err)
		}
		clog.InfoContext(ctx, "signed", "file", f, "key", keyName)
	}
}

// kmsSigner signs with an RSA_SIGN_PKCS1_*_SHA256 KMS key version.
type kmsSigner struct {
	client     *kms.KeyManagementClient
	keyVersion string
}

// Sign doesn't check the response's CRC32C checksums: resign verifies the
// signature itself, which catches any corruption they would.
func (s kmsSigner) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	resp, err := s.client.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{
		Name:   s.keyVersion,
		Digest: &kmspb.Digest{Digest: &kmspb.Digest_Sha256{Sha256: digest}},
	})
	if err != nil {
		return nil, err
	}
	return resp.Signature, nil
}

// checkPublicKey fails unless the KMS key version's public key is the one
// in want, a PEM public key.
func (s kmsSigner) checkPublicKey(ctx context.Context, want []byte) error {
	resp, err := s.client.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: s.keyVersion})
	if err != nil {
		return fmt.Errorf("fetching KMS public key: %w", err)
	}
	got, err := parsePublicKey([]byte(resp.Pem))
	if err != nil {
		return fmt.Errorf("KMS public key: %w", err)
	}
	w, err := parsePublicKey(want)
	if err != nil {
		return err
	}
	if !got.Equal(w) {
		return fmt.Errorf("isn't the public key of %s", s.keyVersion)
	}
	return nil
}

type publicKey interface {
	Equal(x crypto.PublicKey) bool
}

func parsePublicKey(data []byte) (publicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	k, ok := key.(publicKey)
	if !ok {
		return nil, fmt.Errorf("unsupported public key type %T", key)
	}
	return k, nil
}
