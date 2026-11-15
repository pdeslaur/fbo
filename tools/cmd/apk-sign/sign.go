package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"chainguard.dev/apko/pkg/apk/expandapk"
	"chainguard.dev/apko/pkg/apk/signature"
)

// signer signs a SHA-256 digest with RSA PKCS #1 v1.5, as apk signatures are.
type signer interface {
	Sign(ctx context.Context, digest []byte) ([]byte, error)
}

// resign replaces the signature of an apk package or APKINDEX.tar.gz with
// one by s, named after the public key file keyName: apk-tools looks the key
// up in its keyring by that name, though apko tries every key it has. Both
// are gzip streams: an optional
// signature, then the signed stream (an apk's control section, or the
// index), then for a package its data section. Only the signature changes,
// so an index's checksums of its packages' control sections still hold.
// Each signature is verified with publicKey, a PEM public key, before the
// file is replaced.
func resign(ctx context.Context, path string, s signer, keyName string, publicKey []byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	parts, err := expandapk.Split(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s: splitting: %w", path, err)
	}
	// Split returns the signature only when there is one.
	signed, err := io.ReadAll(parts[len(parts)-2])
	if err != nil {
		return err
	}
	rest, err := io.ReadAll(parts[len(parts)-1])
	if err != nil {
		return err
	}

	digest := sha256.Sum256(signed)
	sig, err := s.Sign(ctx, digest[:])
	if err != nil {
		return fmt.Errorf("%s: signing: %w", path, err)
	}
	if err := signature.RSAVerifyDigest(digest[:], crypto.SHA256, sig, publicKey); err != nil {
		return fmt.Errorf("%s: the signature doesn't verify with %s: %w", path, keyName, err)
	}

	modTime, err := firstModTime(signed)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	section, err := signatureSection(".SIGN.RSA256."+keyName, sig, modTime)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".apk-sign-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	for _, b := range [][]byte{section, signed, rest} {
		if _, err := tmp.Write(b); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// firstModTime returns the modification time of the first file in a gzipped
// tar stream: an apk's .PKGINFO, or an index's first file. The signature
// takes it rather than the time it's made, so that signing is reproducible.
func firstModTime(stream []byte) (time.Time, error) {
	zr, err := gzip.NewReader(bytes.NewReader(stream))
	if err != nil {
		return time.Time{}, err
	}
	hdr, err := tar.NewReader(zr).Next()
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the signed stream: %w", err)
	}
	return hdr.ModTime, nil
}

// signatureSection is a gzipped tar stream holding one file, the signature,
// without an end-of-archive marker, since the next stream continues the
// archive.
func signatureSection(name string, sig []byte, modTime time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     int64(len(sig)),
		Mode:     0o644,
		Uname:    "root",
		Gname:    "root",
		ModTime:  modTime,
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(sig); err != nil {
		return nil, err
	}
	if err := tw.Flush(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
