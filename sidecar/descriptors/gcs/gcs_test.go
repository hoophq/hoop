package gcs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/descriptors"
	"golang.org/x/oauth2"
)

// standIn serves as storage.googleapis.com for one test and records the
// request the fetcher made. The credential is a static token, so what is
// under test is the request the fetcher builds and how it reads the answer.
func standIn(t *testing.T, handler http.HandlerFunc) (requests *[]*http.Request) {
	t.Helper()
	var got []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Clone(context.Background()))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	prevEndpoint, prevFind, prevSrc := endpoint, findCredentials, tokenSrc
	endpoint = srv.URL
	findCredentials = func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t0k3n"}), nil
	}
	tokenSrc = nil
	t.Cleanup(func() { endpoint, findCredentials, tokenSrc = prevEndpoint, prevFind, prevSrc })
	return &got
}

func fetch(t *testing.T, entry string) ([]byte, error) {
	t.Helper()
	u, err := url.Parse(entry)
	if err != nil {
		t.Fatal(err)
	}
	return Fetch(context.Background(), u)
}

// The object name is one path segment to the JSON API, so its slashes must
// travel escaped; the version pin must reach the API under its own name;
// and the bearer token must be on the request. Any of these wrong is a 404
// or a 401 in production against a bucket that is configured correctly.
func TestFetchBuildsTheJSONAPIRequest(t *testing.T) {
	got := standIn(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("descriptor bytes"))
	})

	blob, err := fetch(t, "gs://acme-schemas/billing/v42 final.pb?generation=1726480000123456")
	if err != nil {
		t.Fatal(err)
	}
	if string(blob) != "descriptor bytes" {
		t.Fatalf("blob = %q", blob)
	}
	if len(*got) != 1 {
		t.Fatalf("%d requests, want 1", len(*got))
	}
	r := (*got)[0]
	if want := "/storage/v1/b/acme-schemas/o/billing%2Fv42%20final.pb"; r.URL.EscapedPath() != want {
		t.Errorf("path = %q, want %q", r.URL.EscapedPath(), want)
	}
	if r.URL.Query().Get("alt") != "media" {
		t.Errorf("alt = %q, want media", r.URL.Query().Get("alt"))
	}
	if r.URL.Query().Get("generation") != "1726480000123456" {
		t.Errorf("generation = %q", r.URL.Query().Get("generation"))
	}
	if r.Header.Get("Authorization") != "Bearer t0k3n" {
		t.Errorf("authorization = %q", r.Header.Get("Authorization"))
	}
}

func TestFetchRefusesURLsThatWouldReadTheWrongObject(t *testing.T) {
	got := standIn(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("a malformed URL reached the API")
	})
	for entry, want := range map[string]string{
		"gs://acme-schemas":                          "gs://BUCKET/OBJECT",
		"gs://acme-schemas/":                         "gs://BUCKET/OBJECT",
		"gs://acme-schemas/api.pb?generaton=7":       "unsupported query",
		"gs://acme-schemas/api.pb?generation=latest": "unsupported query",
		"gs://acme-schemas/api.pb#generation=7":      "?generation=N",
		"gs://user:pw@acme-schemas/api.pb":           "no credentials",
	} {
		_, err := fetch(t, entry)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", entry, err, want)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("%d requests reached the API", len(*got))
	}
}

func TestFetchReportsTheAPIAnswer(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusNotFound:            "object not found in bucket \"acme-schemas\"",
		http.StatusForbidden:           "storage.objects.get",
		http.StatusUnauthorized:        "storage.objects.get",
		http.StatusServiceUnavailable:  "answered 503",
		http.StatusInternalServerError: "answered 500",
	} {
		standIn(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})
		_, err := fetch(t, "gs://acme-schemas/api.pb")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: error %v, want %q", status, err, want)
		}
	}
}

func TestFetchBoundsTheObjectSize(t *testing.T) {
	prev := maxBytes
	maxBytes = 16
	t.Cleanup(func() { maxBytes = prev })
	standIn(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 17)))
	})
	_, err := fetch(t, "gs://acme-schemas/dump.sql")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want a size refusal", err)
	}
}

// The credential is resolved once and kept: every entry of a config and
// every reload reads through the same source, and a resolution that failed
// is retried rather than remembered.
func TestCredentialResolutionIsCachedOnSuccessOnly(t *testing.T) {
	standIn(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	calls := 0
	failing := true
	findCredentials = func(context.Context) (oauth2.TokenSource, error) {
		calls++
		if failing {
			return nil, context.DeadlineExceeded
		}
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"}), nil
	}
	if _, err := fetch(t, "gs://b/o"); err == nil {
		t.Fatal("a failed credential resolution fetched")
	}
	failing = false
	for range 3 {
		if _, err := fetch(t, "gs://b/o"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("credential resolutions = %d, want 2 (one failure, one success)", calls)
	}
}

// The registry sees this module on import; without that, a gs:// entry is
// refused at config validation and this whole package is dead weight.
func TestRegistersOnImport(t *testing.T) {
	if !descriptors.Linked("gs") {
		t.Fatal("gs is not registered")
	}
}
