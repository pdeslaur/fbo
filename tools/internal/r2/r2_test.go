package r2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestGitHubToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer request-token" {
			t.Errorf("Authorization = %q", got)
		}
		// The request URL GitHub gives already has a query.
		if got := r.URL.Query().Get("api-version"); got != "2.0" {
			t.Errorf("api-version = %q, want the request URL's query kept", got)
		}
		fmt.Fprintf(w, `{"value": "token for %s"}`, r.URL.Query().Get("audience"))
	}))
	t.Cleanup(srv.Close)

	got, err := GitHubToken(context.Background(), srv.URL+"/token?api-version=2.0", "request-token", "https://broker.example.com")
	if err != nil {
		t.Fatalf("GitHubToken() = %v", err)
	}
	if want := "token for https://broker.example.com"; got != want {
		t.Errorf("GitHubToken() = %q, want %q", got, want)
	}

	if _, err := GitHubToken(context.Background(), "", "", "aud"); err == nil {
		t.Error("GitHubToken() without the job's request URL succeeded, want error")
	}
}

func TestExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("subject_token") != "oidc" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error": "invalid_request", "error_description": "the policy doesn't admit the token"}`)
			return
		}
		for k, want := range map[string]string{
			"grant_type":         "urn:ietf:params:oauth:grant-type:token-exchange",
			"subject_token_type": "urn:ietf:params:oauth:token-type:id_token",
			"scope":              "owner/repo:apk",
		} {
			if got := r.Form.Get(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		fmt.Fprint(w, `{
			"issued_token_type": "urn:cloudflare-sts:params:oauth:token-type:r2_credentials",
			"token_type": "N_A",
			"bucket": {"name": "apk", "access_key_id": "id", "secret_access_key": "secret", "session_token": "session", "endpoint": "https://account.r2.cloudflarestorage.com"}
		}`)
	}))
	t.Cleanup(srv.Close)

	got, err := Exchange(context.Background(), srv.URL+"/", "owner/repo:apk", "oidc")
	if err != nil {
		t.Fatalf("Exchange() = %v", err)
	}
	want := Bucket{Name: "apk", AccessKeyID: "id", SecretAccessKey: "secret", SessionToken: "session", Endpoint: "https://account.r2.cloudflarestorage.com"}
	if got != want {
		t.Errorf("Exchange() = %+v, want %+v", got, want)
	}

	_, err = Exchange(context.Background(), srv.URL, "owner/repo:apk", "someone else's")
	if err == nil || !strings.Contains(err.Error(), "the policy doesn't admit the token") {
		t.Errorf("Exchange() of a refused token = %v, want the broker's explanation", err)
	}
}

// fakeS3 serves a path-style bucket from memory, with R2's conditional PUT.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	headers map[string]http.Header
}

func newFakeS3(t *testing.T) (*fakeS3, *Store) {
	t.Helper()
	f := &fakeS3{objects: map[string][]byte{}, headers: map[string]http.Header{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, NewStore(Bucket{Name: "apk", AccessKeyID: "id", SecretAccessKey: "secret", SessionToken: "session", Endpoint: srv.URL})
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := strings.CutPrefix(r.URL.Path, "/apk/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/apk" && r.URL.Query().Get("list-type") == "2":
		prefix := r.URL.Query().Get("prefix")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>apk</Name><IsTruncated>false</IsTruncated>`)
		for k, v := range f.objects {
			if strings.HasPrefix(k, prefix) {
				fmt.Fprintf(w, `<Contents><Key>%s</Key><Size>%d</Size></Contents>`, k, len(v))
			}
		}
		fmt.Fprint(w, `</ListBucketResult>`)
	case !ok:
		http.NotFound(w, r)
	case r.Method == http.MethodGet:
		data, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`)
			return
		}
		w.Write(data)
	case r.Method == http.MethodPut:
		if _, exists := f.objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
			w.WriteHeader(http.StatusPreconditionFailed)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>PreconditionFailed</Code><Message>At least one of the pre-conditions you specified did not hold</Message></Error>`)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		f.objects[key] = data
		f.headers[key] = r.Header.Clone()
	default:
		http.Error(w, "unsupported", http.StatusMethodNotAllowed)
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	f, s := newFakeS3(t)

	if _, err := s.Get(ctx, "os/x86_64/APKINDEX.tar.gz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() of a missing key = %v, want ErrNotFound", err)
	}

	created, err := s.PutIfAbsent(ctx, "os/x86_64/foo-1.0-r0.apk", []byte("first"), "application/octet-stream", "public, max-age=31536000, immutable")
	if err != nil || !created {
		t.Fatalf("PutIfAbsent() = %v, %v, want created", created, err)
	}
	if got := f.headers["os/x86_64/foo-1.0-r0.apk"].Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	created, err = s.PutIfAbsent(ctx, "os/x86_64/foo-1.0-r0.apk", []byte("second"), "application/octet-stream", "")
	if err != nil || created {
		t.Fatalf("PutIfAbsent() of an existing key = %v, %v, want not created", created, err)
	}
	if got, err := s.Get(ctx, "os/x86_64/foo-1.0-r0.apk"); err != nil || string(got) != "first" {
		t.Errorf("Get() = %q, %v, want the first write kept", got, err)
	}

	if err := s.Put(ctx, "os/x86_64/APKINDEX.tar.gz", []byte("index"), "application/gzip", "no-cache"); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if err := s.Put(ctx, "os/x86_64/APKINDEX.tar.gz", []byte("index 2"), "application/gzip", "no-cache"); err != nil {
		t.Fatalf("Put() over an existing key = %v", err)
	}
	if got, _ := s.Get(ctx, "os/x86_64/APKINDEX.tar.gz"); string(got) != "index 2" {
		t.Errorf("Get() = %q, want Put to replace", got)
	}

	objects, err := s.List(ctx, "os/x86_64/")
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(objects) != 2 {
		t.Errorf("List() = %+v, want 2 objects", objects)
	}
}
