package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newUpstream serves a release manifest that, like Mojang's, links each
// version to a document with its download URL, and the downloads themselves.
func newUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
			"latest": {"release": "1.2", "snapshot": "1.3-pre1"},
			"versions": [
				{"id": "1.3-pre1", "url": "%[1]s/1.3-pre1.json"},
				{"id": "1.2", "url": "%[1]s/1.2.json"},
				{"id": "1.1", "url": "%[1]s/1.1.json"}
			]
		}`, srv.URL)
	})
	mux.HandleFunc("/1.2.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"downloads": {"server": {"url": "%s/objects/cafe/server.jar"}}}`, srv.URL)
	})
	mux.HandleFunc("/objects/cafe/server.jar", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "server 1.2")
	})
	mux.HandleFunc("/dist/foo-1.2.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "foo 1.2")
	})
	return srv
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestLatestRelease(t *testing.T) {
	srv := newUpstream(t)
	got, err := latestRelease(context.Background(), &updateRules{
		URL:     srv.URL + "/manifest.json",
		Version: ".latest.release",
		Vars: map[string]string{
			"server-jar": ".versions[] | select(.id == $version) | .url | fetch_json | .downloads.server.url",
		},
	})
	if err != nil {
		t.Fatalf("latestRelease() = %v", err)
	}
	if want := srv.URL + "/objects/cafe/server.jar"; got.Version != "1.2" || got.Vars["server-jar"] != want {
		t.Errorf("latestRelease() = %+v, want version 1.2 and server-jar %s", got, want)
	}
}

func TestEvalQueryErrors(t *testing.T) {
	doc := map[string]any{
		"latest":   "1.2",
		"versions": []any{"1.1", "1.2"},
		"count":    2.0,
		"empty":    "",
	}
	for _, query := range []string{
		".versions[]",                      // two results
		".versions[] | select(. == \"9\")", // no results
		".count",                           // not a string
		".empty",                           // empty string
		".latest |",                        // doesn't parse
		"fetch_json",                       // input isn't a string
		"$missing",                         // undefined variable
	} {
		if got, err := evalQuery(context.Background(), query, doc, "1.2"); err == nil {
			t.Errorf("evalQuery(%q) = %q, want error", query, got)
		}
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]string{"jar": "https://example.com/a.jar", "name": "foo"}
	for _, tt := range []struct {
		in, want string
	}{
		{"${{vars.jar}}", "https://example.com/a.jar"},
		{"https://example.com/${{vars.name}}-${{package.version}}.tar.gz", "https://example.com/foo-1.2.tar.gz"},
		{"https://example.com/static.tar.gz", "https://example.com/static.tar.gz"},
	} {
		got, err := expand(tt.in, "1.2", vars)
		if err != nil || got != tt.want {
			t.Errorf("expand(%q) = %q, %v, want %q", tt.in, got, err, tt.want)
		}
	}

	for _, in := range []string{
		"${{vars.missing}}",
		"https://example.com/foo-${{build.arch}}.tar.gz",
	} {
		if got, err := expand(in, "1.2", vars); err == nil {
			t.Errorf("expand(%q) = %q, want error", in, got)
		}
	}
}

func TestUpdatePackageFetch(t *testing.T) {
	srv := newUpstream(t)
	data := []byte(strings.ReplaceAll(`package:
    name: server
    version: "1.1"
    epoch: 3

vars:
    server-jar: SRV/objects/beef/server.jar
    update-url: SRV/manifest.json
    update-version: .latest.release
    update-var-server-jar: '.versions[] | select(.id == $version) | .url | fetch_json | .downloads.server.url'
    update-release-notes: '"https://example.com/notes/" + $version'

pipeline:
    - uses: fetch
      with:
          uri: ${{vars.server-jar}}
          expected-sha256: 1111
          extract: false
    - uses: fetch
      with:
          uri: SRV/dist/foo-${{package.version}}.tar.gz
          expected-sha256: 2222
`, "SRV", srv.URL))
	src, err := parseSource(data)
	if err != nil {
		t.Fatalf("parseSource() = %v", err)
	}

	got, u, err := updatePackageFetch(context.Background(), data, src)
	if err != nil {
		t.Fatalf("updatePackageFetch() = %v", err)
	}
	want := strings.NewReplacer(
		`version: "1.1"`, `version: "1.2"`,
		"server-jar: "+srv.URL+"/objects/beef/server.jar", "server-jar: "+srv.URL+"/objects/cafe/server.jar",
		"expected-sha256: 1111", "expected-sha256: "+sha256Hex("server 1.2"),
		"expected-sha256: 2222", "expected-sha256: "+sha256Hex("foo 1.2"),
	).Replace(string(data))
	if string(got) != want {
		t.Errorf("updatePackageFetch() =\n%s\nwant:\n%s", got, want)
	}
	wantUpdate := update{Name: "server", From: "1.1", To: "1.2", URL: "https://example.com/notes/1.2"}
	if u == nil || *u != wantUpdate {
		t.Errorf("updatePackageFetch() update = %+v, want %+v", u, wantUpdate)
	}

	// Once updated, there's nothing more to do.
	src, err = parseSource(got)
	if err != nil {
		t.Fatalf("parseSource(updated) = %v", err)
	}
	again, u, err := updatePackageFetch(context.Background(), got, src)
	if err != nil {
		t.Fatalf("updatePackageFetch(updated) = %v", err)
	}
	if u != nil || string(again) != string(got) {
		t.Errorf("updatePackageFetch(updated) = %+v, want no update", u)
	}
}
