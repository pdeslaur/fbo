package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// apkIndexArchive builds an APKINDEX.tar.gz as apk repositories serve it: a
// gzipped signature archive without an end-of-archive marker, followed by
// the gzipped index archive.
func apkIndexArchive(t *testing.T, index string) []byte {
	t.Helper()
	var buf bytes.Buffer
	segment := func(name, content string, closeTar bool) {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		finish := tw.Flush
		if closeTar {
			finish = tw.Close
		}
		if err := finish(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	segment(".SIGN.RSA256.wolfi-signing.rsa.pub", "signature", false)
	segment("APKINDEX", index, true)
	return buf.Bytes()
}

const x86Index = `C:Q1abc=
P:go-1.26
V:1.26.10-r0
A:x86_64
o:go-1.26
t:1

P:go-1.26
V:1.26.11-r0
A:x86_64
o:go-1.26
t:2

P:go-1.26
V:1.26.12-r0
A:x86_64
o:go-1.26
t:3

P:rust-1.100
V:1.100.0-r0
o:rust-1.100
p:cmd:cargo=1.100.0-r0 rust=1.100.0-r0
t:4

P:rust-1.99
V:1.99.1-r3
o:rust-1.99
p:cmd:cargo=1.99.1-r3 rust=1.99.1-r3 so:librustc.so
t:5
`

// The aarch64 index doesn't have go-1.26 1.26.12-r0 yet.
const aarch64Index = `P:go-1.26
V:1.26.11-r0
o:go-1.26

P:go-1.26
V:1.26.10-r0
o:go-1.26

P:rust-1.100
V:1.100.0-r0
o:rust-1.100
p:rust=1.100.0-r0

P:rust-1.99
V:1.99.1-r3
o:rust-1.99
p:rust=1.99.1-r3
`

func TestParseAPKIndexArchive(t *testing.T) {
	idx, err := parseAPKIndexArchive(bytes.NewReader(apkIndexArchive(t, x86Index)))
	if err != nil {
		t.Fatalf("parseAPKIndexArchive() = %v", err)
	}
	var versions []string
	for _, p := range idx["go-1.26"] {
		versions = append(versions, p.Version)
	}
	if got, want := strings.Join(versions, " "), "1.26.10-r0 1.26.11-r0 1.26.12-r0"; got != want {
		t.Errorf("go-1.26 versions = %q, want %q", got, want)
	}

	// rust is only a provides, of both rust-1.100 and rust-1.99. Unversioned
	// provides, like so:librustc.so, can't satisfy a pin.
	var providers []string
	for _, p := range idx["rust"] {
		providers = append(providers, p.Name+"="+p.Version)
	}
	if got, want := strings.Join(providers, " "), "rust-1.100=1.100.0-r0 rust-1.99=1.99.1-r3"; got != want {
		t.Errorf("rust providers = %q, want %q", got, want)
	}
	if _, ok := idx["so:librustc.so"]; ok {
		t.Error("unversioned provides so:librustc.so is indexed")
	}
}

func TestParseAPKIndexArchiveWithoutIndex(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.Close()
	gz.Close()
	if _, err := parseAPKIndexArchive(&buf); err == nil {
		t.Error("parseAPKIndexArchive() = nil, want error")
	}
}

func parseIndex(t *testing.T, index string) apkIndex {
	t.Helper()
	idx, err := parseAPKIndex(strings.NewReader(index))
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestLatestAPK(t *testing.T) {
	indexes := []apkIndex{parseIndex(t, x86Index), parseIndex(t, aarch64Index)}
	for _, tc := range []struct {
		name, want, wantOrigin string
	}{
		// 1.26.12-r0 is only built for x86_64.
		{"go-1.26", "1.26.11-r0", "go-1.26"},
		// rust-1.99 was rebuilt after rust-1.100, but is the older version.
		{"rust", "1.100.0-r0", "rust-1.100"},
	} {
		got, ok, err := latestAPK(indexes, tc.name)
		if err != nil || !ok {
			t.Fatalf("latestAPK(%q) = %v, %v", tc.name, ok, err)
		}
		if got.Version != tc.want || got.Origin != tc.wantOrigin {
			t.Errorf("latestAPK(%q) = %s from %s, want %s from %s", tc.name, got.Version, got.Origin, tc.want, tc.wantOrigin)
		}
	}

	if _, ok, err := latestAPK(indexes, "bun"); ok || err != nil {
		t.Errorf("latestAPK(bun) = %v, %v, want not found", ok, err)
	}
}

func TestCompareAPKVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.26.11-r0", "1.26.11-r0", 0},
		{"1.26.11-r0", "1.26.11-r1", -1},
		{"1.26.11-r0", "1.26.9-r3", 1},
		{"1.100.0-r0", "1.99.1-r3", 1},
		{"1-r10", "1-r9", 1},
		{"36.2-r1", "3.29.5-r0", 1},
		{"1.2-r0", "1.2.0-r0", 0},
		{"1.2a-r0", "1.2-r0", 1},
		{"1.27.0_rc2-r0", "1.27.0-r0", -1},
		{"1.27.0_rc2-r0", "1.27.0_rc10-r0", -1},
		{"1.27.0_beta1-r0", "1.27.0_rc1-r0", -1},
		{"1.27.0_p1-r0", "1.27.0-r0", 1},
	} {
		got, err := compareAPKVersions(tc.a, tc.b)
		if err != nil {
			t.Fatalf("compareAPKVersions(%q, %q) = %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("compareAPKVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompareAPKVersionsErrors(t *testing.T) {
	for _, v := range []string{"1.2", "1.2-rc", "v1.2-r0", "1.2_foo-r0", ""} {
		if _, err := compareAPKVersions(v, "1.2-r0"); err == nil {
			t.Errorf("compareAPKVersions(%q) = nil, want error", v)
		}
	}
}

func TestUpdatePins(t *testing.T) {
	archives := map[string][]byte{
		"/os/x86_64/APKINDEX.tar.gz":  apkIndexArchive(t, x86Index),
		"/os/aarch64/APKINDEX.tar.gz": apkIndexArchive(t, aarch64Index),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := archives[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)

	in := `package:
    name: foo
    version: "1.0"
    epoch: 0

environment:
    contents:
        repositories:
            - ` + srv.URL + `/os
        packages:
            - busybox
            - go-1.26=1.26.10-r0
            - rust=1.100.0-r0
            - build-base>=1
`
	got, updates, err := updatePins(context.Background(), []byte(in))
	if err != nil {
		t.Fatalf("updatePins() = %v", err)
	}
	want := strings.Replace(in, "go-1.26=1.26.10-r0", "go-1.26=1.26.11-r0", 1)
	if string(got) != want {
		t.Errorf("updatePins() config =\n%s\nwant:\n%s", got, want)
	}
	if len(updates) != 1 {
		t.Fatalf("updatePins() updates = %+v, want one", updates)
	}
	u := updates[0]
	if u.Name != "go-1.26" || u.From != "1.26.10-r0" || u.To != "1.26.11-r0" ||
		u.URL != "https://github.com/wolfi-dev/os/blob/main/go-1.26.yaml" {
		t.Errorf("updatePins() update = %+v", u)
	}
}

func TestUpdatePinsWithoutPins(t *testing.T) {
	in := "package:\n    name: foo\nenvironment:\n    contents:\n        repositories:\n            - http://127.0.0.1:1/os\n        packages:\n            - busybox\n"
	got, updates, err := updatePins(context.Background(), []byte(in))
	if err != nil || len(updates) != 0 || string(got) != in {
		t.Errorf("updatePins() = %q, %v, %v; want no fetch and no change", got, updates, err)
	}
}
