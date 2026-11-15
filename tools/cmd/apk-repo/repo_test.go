package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"chainguard.dev/apko/pkg/apk/apk"
	"github.com/pdeslaur/fbo/tools/internal/apksign"
	"github.com/pdeslaur/fbo/tools/internal/r2"
)

// memStore is a bucket in memory.
type memStore struct {
	objects      map[string][]byte
	cacheControl map[string]string
	puts         int
}

func newMemStore() *memStore {
	return &memStore{objects: map[string][]byte{}, cacheControl: map[string]string{}}
}

func (m *memStore) Get(_ context.Context, key string) ([]byte, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("%s: %w", key, r2.ErrNotFound)
	}
	return data, nil
}

func (m *memStore) Put(_ context.Context, key string, data []byte, _, cacheControl string) error {
	m.objects[key] = data
	m.cacheControl[key] = cacheControl
	m.puts++
	return nil
}

func (m *memStore) PutIfAbsent(ctx context.Context, key string, data []byte, contentType, cacheControl string) (bool, error) {
	if _, ok := m.objects[key]; ok {
		return false, nil
	}
	return true, m.Put(ctx, key, data, contentType, cacheControl)
}

func (m *memStore) List(_ context.Context, prefix string) ([]r2.Object, error) {
	var objects []r2.Object
	for k, v := range m.objects {
		if strings.HasPrefix(k, prefix) {
			objects = append(objects, r2.Object{Key: k, Size: int64(len(v))})
		}
	}
	return objects, nil
}

type keySigner struct {
	key *rsa.PrivateKey
}

func (s keySigner) Sign(_ context.Context, digest []byte) ([]byte, error) {
	return rsa.SignPKCS1v15(nil, s.key, crypto.SHA256, digest)
}

func newKey(t *testing.T) (keySigner, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return keySigner{key}, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func gzipTar(t *testing.T, last bool, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), ModTime: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
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

// pkg is an apk package of name and version for arch, signed by s.
func pkg(t *testing.T, s keySigner, publicKey []byte, name, version, arch string) []byte {
	t.Helper()
	pkginfo := fmt.Sprintf("pkgname = %s\npkgver = %s\narch = %s\nsize = 10\norigin = %s\nbuilddate = 1794700000\nlicense = MIT\n", name, version, arch, name)
	control := gzipTar(t, false, ".PKGINFO", pkginfo)
	data := gzipTar(t, true, "usr/bin/"+name, "#!/bin/sh\n")
	signed, err := apksign.Resign(context.Background(), append(control, data...), s, "test.rsa.pub", publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func writePackage(t *testing.T, dir, arch, file string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, arch, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUpload(t *testing.T) {
	ctx := context.Background()
	s, publicKey := newKey(t)
	st := newMemStore()
	dir := t.TempDir()
	first := pkg(t, s, publicKey, "foo", "1.0-r0", "x86_64")
	files := []string{
		writePackage(t, dir, "x86_64", "foo-1.0-r0.apk", first),
		writePackage(t, dir, "aarch64", "foo-1.0-r0.apk", pkg(t, s, publicKey, "foo", "1.0-r0", "aarch64")),
	}
	if err := upload(ctx, st, files); err != nil {
		t.Fatalf("upload() = %v", err)
	}
	for _, key := range []string{"os/x86_64/foo-1.0-r0.apk", "os/aarch64/foo-1.0-r0.apk"} {
		if _, ok := st.objects[key]; !ok {
			t.Errorf("upload() didn't write %s", key)
		}
		if got := st.cacheControl[key]; got != packageCacheControl {
			t.Errorf("%s Cache-Control = %q, want %q", key, got, packageCacheControl)
		}
	}

	// A rebuild of the same version-epoch doesn't replace it.
	rebuilt := writePackage(t, t.TempDir(), "x86_64", "foo-1.0-r0.apk", []byte("rebuilt"))
	if err := upload(ctx, st, []string{rebuilt}); err != nil {
		t.Fatalf("upload() = %v", err)
	}
	if !bytes.Equal(st.objects["os/x86_64/foo-1.0-r0.apk"], first) {
		t.Error("upload() replaced a version-epoch the repository had")
	}

	for _, bad := range []string{
		writePackage(t, t.TempDir(), "riscv64", "foo-1.0-r0.apk", first),
		writePackage(t, t.TempDir(), "x86_64", "APKINDEX.tar.gz", first),
	} {
		if err := upload(ctx, st, []string{bad}); err == nil {
			t.Errorf("upload(%s) succeeded, want error", bad)
		}
	}
}

// indexed returns the packages an arch's index lists, after checking that
// it's signed by publicKey.
func indexed(t *testing.T, st *memStore, publicKey []byte, arch string) []string {
	t.Helper()
	data := st.objects[indexKey(arch)]
	if err := apksign.Verify(data, publicKey); err != nil {
		t.Fatalf("index for %s: %v", arch, err)
	}
	idx, err := apk.IndexFromArchive(io.NopCloser(bytes.NewReader(data)))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, p := range idx.Packages {
		files = append(files, p.Filename())
	}
	return files
}

func TestIndex(t *testing.T) {
	ctx := context.Background()
	s, publicKey := newKey(t)
	st := newMemStore()
	st.objects["os/x86_64/foo-1.0-r0.apk"] = pkg(t, s, publicKey, "foo", "1.0-r0", "x86_64")
	st.objects["os/aarch64/foo-1.0-r0.apk"] = pkg(t, s, publicKey, "foo", "1.0-r0", "aarch64")

	for _, arch := range arches {
		if err := index(ctx, st, s, "test.rsa.pub", publicKey, arch); err != nil {
			t.Fatalf("index(%s) = %v", arch, err)
		}
		if got := indexed(t, st, publicKey, arch); !slices.Equal(got, []string{"foo-1.0-r0.apk"}) {
			t.Errorf("index for %s lists %v", arch, got)
		}
		if got := st.cacheControl[indexKey(arch)]; got != indexCacheControl {
			t.Errorf("index Cache-Control = %q, want %q", got, indexCacheControl)
		}
	}

	// A new package is added to what the index lists.
	st.objects["os/x86_64/bar-2.0-r1.apk"] = pkg(t, s, publicKey, "bar", "2.0-r1", "x86_64")
	if err := index(ctx, st, s, "test.rsa.pub", publicKey, "x86_64"); err != nil {
		t.Fatalf("index() = %v", err)
	}
	if got := indexed(t, st, publicKey, "x86_64"); !slices.Equal(got, []string{"bar-2.0-r1.apk", "foo-1.0-r0.apk"}) {
		t.Errorf("index lists %v, want bar and foo", got)
	}

	// Nothing new, nothing written.
	puts := st.puts
	if err := index(ctx, st, s, "test.rsa.pub", publicKey, "x86_64"); err != nil {
		t.Fatalf("index() = %v", err)
	}
	if st.puts != puts {
		t.Error("index() rewrote an index that was up to date")
	}
}

func TestIndexRefusesUnverifiedIndex(t *testing.T) {
	ctx := context.Background()
	s, publicKey := newKey(t)
	other, otherPublicKey := newKey(t)
	st := newMemStore()
	st.objects["os/x86_64/foo-1.0-r0.apk"] = pkg(t, s, publicKey, "foo", "1.0-r0", "x86_64")
	if err := index(ctx, st, other, "other.rsa.pub", otherPublicKey, "x86_64"); err != nil {
		t.Fatal(err)
	}

	st.objects["os/x86_64/bar-2.0-r1.apk"] = pkg(t, s, publicKey, "bar", "2.0-r1", "x86_64")
	if err := index(ctx, st, s, "test.rsa.pub", publicKey, "x86_64"); err == nil {
		t.Error("index() built on an index signed by another key, want error")
	}
}

func TestIndexRefusesMisplacedPackage(t *testing.T) {
	s, publicKey := newKey(t)
	st := newMemStore()
	st.objects["os/x86_64/foo-1.0-r0.apk"] = pkg(t, s, publicKey, "foo", "1.0-r0", "aarch64")
	if err := index(context.Background(), st, s, "test.rsa.pub", publicKey, "x86_64"); err == nil {
		t.Error("index() listed an aarch64 package for x86_64, want error")
	}
}

// TestRepositoryForApko checks that apko reads the repository, laid out on
// disk as the bucket is, trusting only the signing key.
func TestRepositoryForApko(t *testing.T) {
	ctx := context.Background()
	s, publicKey := newKey(t)
	st := newMemStore()
	st.objects["os/x86_64/foo-1.0-r0.apk"] = pkg(t, s, publicKey, "foo", "1.0-r0", "x86_64")
	if err := index(ctx, st, s, "test.rsa.pub", publicKey, "x86_64"); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	for key, data := range st.objects {
		p := filepath.Join(root, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	indexes, err := apk.GetRepositoryIndexes(ctx, []string{filepath.Join(root, "os")}, map[string][]byte{"test.rsa.pub": publicKey}, "x86_64")
	if err != nil {
		t.Fatalf("GetRepositoryIndexes() = %v", err)
	}
	pkgs, err := apk.NewPkgResolver(ctx, indexes).ResolvePackage("foo", nil)
	if err != nil {
		t.Fatalf("ResolvePackage(foo) = %v", err)
	}
	if pkgs[0].Version != "1.0-r0" {
		t.Errorf("ResolvePackage(foo) = %s, want 1.0-r0", pkgs[0].Version)
	}
}
