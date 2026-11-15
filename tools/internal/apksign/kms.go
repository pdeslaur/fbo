package apksign

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
)

// KMS signs with an RSA_SIGN_PKCS1_*_SHA256 Cloud KMS key version, so that
// the private key never leaves KMS.
type KMS struct {
	client     *kms.KeyManagementClient
	keyVersion string
}

// NewKMS returns a signer for keyVersion, e.g.
// projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoKeyVersions/1, with
// the environment's Google credentials. It fails unless the key version's
// public key is publicKey, a PEM public key.
func NewKMS(ctx context.Context, keyVersion string, publicKey []byte) (*KMS, error) {
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating KMS client: %w", err)
	}
	k := &KMS{client: client, keyVersion: keyVersion}
	if err := k.checkPublicKey(ctx, publicKey); err != nil {
		client.Close()
		return nil, err
	}
	return k, nil
}

func (k *KMS) Close() error {
	return k.client.Close()
}

// Sign doesn't check the response's CRC32C checksums: Resign verifies the
// signature itself, which catches any corruption they would.
func (k *KMS) Sign(ctx context.Context, digest []byte) ([]byte, error) {
	resp, err := k.client.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{
		Name:   k.keyVersion,
		Digest: &kmspb.Digest{Digest: &kmspb.Digest_Sha256{Sha256: digest}},
	})
	if err != nil {
		return nil, err
	}
	return resp.Signature, nil
}

func (k *KMS) checkPublicKey(ctx context.Context, want []byte) error {
	resp, err := k.client.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: k.keyVersion})
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
		return fmt.Errorf("the public key isn't that of %s", k.keyVersion)
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
