// Package apksign signs apk packages and APKINDEX.tar.gz files, and verifies
// their signatures.
//
// Both are gzip streams: an optional signature, then the signed stream (an
// apk's control section, or the index), then for a package its data section.
// A signature is RSA PKCS #1 v1.5 over the signed stream's SHA-256, in a file
// named .SIGN.RSA256.<the public key file's name>.
package apksign

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"chainguard.dev/apko/pkg/apk/expandapk"
	"chainguard.dev/apko/pkg/apk/signature"
)

const signaturePrefix = ".SIGN.RSA256."

// Signer signs a SHA-256 digest with RSA PKCS #1 v1.5.
type Signer interface {
	Sign(ctx context.Context, digest []byte) ([]byte, error)
}

// Resign returns data, an apk package or APKINDEX.tar.gz, with its signature
// replaced by one by s, named after the public key file keyName: apk-tools
// looks the key up in its keyring by that name, though apko tries every key
// it has. Only the signature changes, so an index's checksums of its
// packages' control sections still hold. The signature is verified with
// publicKey, a PEM public key.
func Resign(ctx context.Context, data []byte, s Signer, keyName string, publicKey []byte) ([]byte, error) {
	_, signed, rest, err := split(data)
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256(signed)
	sig, err := s.Sign(ctx, digest[:])
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}
	if err := signature.RSAVerifyDigest(digest[:], crypto.SHA256, sig, publicKey); err != nil {
		return nil, fmt.Errorf("the signature doesn't verify with %s: %w", keyName, err)
	}

	modTime, err := firstModTime(signed)
	if err != nil {
		return nil, err
	}
	section, err := signatureSection(signaturePrefix+keyName, sig, modTime)
	if err != nil {
		return nil, err
	}
	return bytes.Join([][]byte{section, signed, rest}, nil), nil
}

// ResignFile replaces the file at path with Resign's result.
func ResignFile(ctx context.Context, path string, s Signer, keyName string, publicKey []byte) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	signed, err := Resign(ctx, data, s, keyName, publicKey)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".apksign-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(signed); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Verify checks that data, an apk package or APKINDEX.tar.gz, is signed by
// publicKey, a PEM public key.
func Verify(data, publicKey []byte) error {
	section, signed, _, err := split(data)
	if err != nil {
		return err
	}
	if section == nil {
		return errors.New("not signed")
	}
	zr, err := gzip.NewReader(bytes.NewReader(section))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	if err != nil {
		return fmt.Errorf("reading the signature: %w", err)
	}
	if !strings.HasPrefix(hdr.Name, signaturePrefix) {
		return fmt.Errorf("unsupported signature %s", hdr.Name)
	}
	sig, err := io.ReadAll(tr)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(signed)
	return signature.RSAVerifyDigest(digest[:], crypto.SHA256, sig, publicKey)
}

// split returns data's signature section, nil if it has none, its signed
// stream, and the rest.
func split(data []byte) (section, signed, rest []byte, err error) {
	parts, err := expandapk.Split(bytes.NewReader(data))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("splitting: %w", err)
	}
	var b [3][]byte
	for i, p := range parts {
		if b[i], err = io.ReadAll(p); err != nil {
			return nil, nil, nil, err
		}
	}
	// Split returns the signature only when there is one.
	if len(parts) == 2 {
		return nil, b[0], b[1], nil
	}
	return b[0], b[1], b[2], nil
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
