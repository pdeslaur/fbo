package apksign

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"chainguard.dev/apko/pkg/apk/apk"
	"chainguard.dev/apko/pkg/apk/expandapk"
	"chainguard.dev/apko/pkg/apk/signature"
)

type keySigner struct {
	key *rsa.PrivateKey
}

func (s keySigner) Sign(_ context.Context, digest []byte) ([]byte, error) {
	return rsa.SignPKCS1v15(nil, s.key, crypto.SHA256, digest)
}

func newKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

type file struct {
	name    string
	content string
}

// tarStream is a gzipped tar stream of files, with an end-of-archive marker
// when it ends the archive.
func tarStream(t *testing.T, modTime time.Time, last bool, files ...file) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.content)), ModTime: modTime}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			t.Fatal(err)
		}
	}
	finish := tw.Flush
	if last {
		finish = tw.Close
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var buildDate = time.Date(2026, 11, 14, 17, 50, 0, 0, time.UTC)

// throwawaySignature is a signature section, as melange writes it, by a key
// that Resign replaces.
func throwawaySignature(t *testing.T, signed []byte) []byte {
	t.Helper()
	key, _ := newKey(t)
	digest := sha256.Sum256(signed)
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	section, err := signatureSection(".SIGN.RSA256.melange-x86_64.rsa.pub", sig, buildDate)
	if err != nil {
		t.Fatal(err)
	}
	return section
}

func writeFile(t *testing.T, name string, parts ...[]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, bytes.Join(parts, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// checkSigned checks that the file at path is signed by publicKey under
// keyName, followed by signed and rest unchanged.
func checkSigned(t *testing.T, path, keyName string, publicKey, signed, rest []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := expandapk.Split(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Split() = %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("Split() = %d parts, want a signature, the signed stream and the rest", len(parts))
	}
	var got [3][]byte
	for i, p := range parts {
		if got[i], err = io.ReadAll(p); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got[1], signed) {
		t.Error("signed stream changed")
	}
	if !bytes.Equal(got[2], rest) {
		t.Error("rest changed")
	}

	zr, err := gzip.NewReader(bytes.NewReader(got[0]))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if want := ".SIGN.RSA256." + keyName; hdr.Name != want {
		t.Errorf("signature name = %q, want %q", hdr.Name, want)
	}
	if !hdr.ModTime.Equal(buildDate) {
		t.Errorf("signature time = %v, want the build date %v", hdr.ModTime, buildDate)
	}
	sig, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(signed)
	if err := signature.RSAVerifyDigest(digest[:], crypto.SHA256, sig, publicKey); err != nil {
		t.Errorf("signature doesn't verify: %v", err)
	}
}

func TestResignPackage(t *testing.T) {
	key, publicKey := newKey(t)
	control := tarStream(t, buildDate, false, file{".PKGINFO", "pkgname = foo\npkgver = 1.0-r0\n"})
	data := tarStream(t, buildDate, true, file{"usr/bin/foo", "#!/bin/sh\n"})

	for _, tc := range []struct {
		desc  string
		parts [][]byte
	}{
		{"signed by a throwaway key", [][]byte{throwawaySignature(t, control), control, data}},
		{"unsigned", [][]byte{control, data}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			path := writeFile(t, "foo-1.0-r0.apk", tc.parts...)
			if err := ResignFile(context.Background(), path, keySigner{key}, "test.rsa.pub", publicKey); err != nil {
				t.Fatalf("ResignFile() = %v", err)
			}
			checkSigned(t, path, "test.rsa.pub", publicKey, control, data)
		})
	}
}

func TestResignIsReproducible(t *testing.T) {
	key, publicKey := newKey(t)
	control := tarStream(t, buildDate, false, file{".PKGINFO", "pkgname = foo\n"})
	data := tarStream(t, buildDate, true, file{"usr/bin/foo", "foo"})

	var signed [][]byte
	for range 2 {
		path := writeFile(t, "foo-1.0-r0.apk", throwawaySignature(t, control), control, data)
		if err := ResignFile(context.Background(), path, keySigner{key}, "test.rsa.pub", publicKey); err != nil {
			t.Fatalf("ResignFile() = %v", err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		signed = append(signed, b)
	}
	if !bytes.Equal(signed[0], signed[1]) {
		t.Error("signing the same package twice gave different files")
	}
}

func TestResignWrongKey(t *testing.T) {
	key, _ := newKey(t)
	_, otherPublicKey := newKey(t)
	control := tarStream(t, buildDate, false, file{".PKGINFO", "pkgname = foo\n"})
	data := tarStream(t, buildDate, true, file{"usr/bin/foo", "foo"})
	original := bytes.Join([][]byte{throwawaySignature(t, control), control, data}, nil)
	path := writeFile(t, "foo-1.0-r0.apk", original)

	if err := ResignFile(context.Background(), path, keySigner{key}, "test.rsa.pub", otherPublicKey); err == nil {
		t.Fatal("ResignFile() with a signer that doesn't match the public key succeeded, want error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Error("ResignFile() changed the file although the signature didn't verify")
	}
}

// TestResignIndex checks that apko accepts a re-signed index with only the
// signing key in its keyring, as apko-publish reads the packages.
func TestResignIndex(t *testing.T) {
	key, publicKey := newKey(t)
	index := tarStream(t, buildDate, true,
		file{"DESCRIPTION", ""},
		file{"APKINDEX", "C:Q1abcdefghijklmnopqrstuvwxyzA=\nP:foo\nV:1.0-r0\nA:x86_64\nS:100\nI:200\nT:foo\nL:MIT\no:foo\n\n"},
	)

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "x86_64"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "x86_64", "APKINDEX.tar.gz")
	if err := os.WriteFile(path, bytes.Join([][]byte{throwawaySignature(t, index), index}, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ResignFile(context.Background(), path, keySigner{key}, "test.rsa.pub", publicKey); err != nil {
		t.Fatalf("ResignFile() = %v", err)
	}
	checkSigned(t, path, "test.rsa.pub", publicKey, index, nil)

	indexes, err := apk.GetRepositoryIndexes(context.Background(), []string{repo}, map[string][]byte{"test.rsa.pub": publicKey}, "x86_64")
	if err != nil {
		t.Fatalf("GetRepositoryIndexes() = %v", err)
	}
	if len(indexes) != 1 || len(indexes[0].Packages()) != 1 || indexes[0].Packages()[0].Name != "foo" {
		t.Errorf("GetRepositoryIndexes() = %v, want the one index with foo", indexes)
	}

	// apko caches an index by its path, whatever the keyring, so another key
	// is tried on a copy.
	signed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copied := t.TempDir()
	if err := os.Mkdir(filepath.Join(copied, "x86_64"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, "x86_64", "APKINDEX.tar.gz"), signed, 0o644); err != nil {
		t.Fatal(err)
	}
	_, otherPublicKey := newKey(t)
	if _, err := apk.GetRepositoryIndexes(context.Background(), []string{copied}, map[string][]byte{"test.rsa.pub": otherPublicKey}, "x86_64"); err == nil {
		t.Error("GetRepositoryIndexes() with another key succeeded, want error")
	}
}

func TestParsePublicKey(t *testing.T) {
	_, a := newKey(t)
	_, b := newKey(t)
	ka, err := parsePublicKey(a)
	if err != nil {
		t.Fatalf("parsePublicKey() = %v", err)
	}
	kb, err := parsePublicKey(b)
	if err != nil {
		t.Fatalf("parsePublicKey() = %v", err)
	}
	if ka2, _ := parsePublicKey(a); !ka.Equal(ka2) {
		t.Error("a key isn't Equal to itself")
	}
	if ka.Equal(kb) {
		t.Error("two different keys are Equal")
	}
	if _, err := parsePublicKey([]byte("not PEM")); err == nil {
		t.Error("parsePublicKey() of non-PEM succeeded, want error")
	}
}

func TestVerify(t *testing.T) {
	key, publicKey := newKey(t)
	_, otherPublicKey := newKey(t)
	control := tarStream(t, buildDate, false, file{".PKGINFO", "pkgname = foo\n"})
	data := tarStream(t, buildDate, true, file{"usr/bin/foo", "foo"})
	unsigned := bytes.Join([][]byte{control, data}, nil)

	signed, err := Resign(context.Background(), unsigned, keySigner{key}, "test.rsa.pub", publicKey)
	if err != nil {
		t.Fatalf("Resign() = %v", err)
	}
	if err := Verify(signed, publicKey); err != nil {
		t.Errorf("Verify() = %v", err)
	}
	if err := Verify(signed, otherPublicKey); err == nil {
		t.Error("Verify() with another key succeeded, want error")
	}
	if err := Verify(unsigned, publicKey); err == nil {
		t.Error("Verify() of an unsigned package succeeded, want error")
	}
}
