package gcs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
	prevEndpoint, prevFind, prevToken := endpoint, findCredentials, token
	endpoint = srv.URL
	findCredentials = func(context.Context) (oauth2.TokenSource, error) {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t0k3n"}), nil
	}
	token = nil
	t.Cleanup(func() { endpoint, findCredentials, token = prevEndpoint, prevFind, prevToken })
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

// realFindCredentials is the resolver as shipped, captured before any test
// stubs the variable, so the credential-source tests exercise the real one.
var realFindCredentials = findCredentials

// A key passed inline outranks ADC, its contents never reach an error, and
// a value that is set but wrong is an error rather than a fallthrough to
// whatever identity ADC would have found.
func TestInlineJSONCredentialOutranksADCAndIsNeverEchoed(t *testing.T) {
	// ADC would fail here: the path variable names a file that does not
	// exist. The inline key must be consulted first and win.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", t.TempDir()+"/absent.json")
	const secret = "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC-not-a-real-key"
	t.Setenv(CredentialsJSONEnv, `{"type":"service_account","project_id":"p",`+
		`"client_email":"reader@p.iam.gserviceaccount.com",`+
		`"private_key":"-----BEGIN PRIVATE KEY-----\n`+secret+`\n-----END PRIVATE KEY-----\n",`+
		`"token_uri":"https://oauth2.googleapis.com/token"}`)
	if _, err := realFindCredentials(context.Background()); err != nil {
		t.Fatalf("inline key was not accepted: %v", err)
	}

	t.Setenv(CredentialsJSONEnv, `{"type":"service_account","private_key":"`+secret+`"`) // truncated
	_, err := realFindCredentials(context.Background())
	if err == nil {
		t.Fatal("a malformed inline key fell through")
	}
	if !strings.Contains(err.Error(), CredentialsJSONEnv+" is set but is not") {
		t.Errorf("error %q does not name the variable", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error echoes the key material")
	}

	t.Setenv(CredentialsJSONEnv, "   ")
	_, err = realFindCredentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no application default credentials") {
		t.Errorf("blank inline value must fall through to ADC, got %v", err)
	}
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
		// url.URL.Query drops a pair it cannot decode; either of these
		// would otherwise read the current version under a URL that
		// looks pinned.
		"gs://acme-schemas/api.pb?generation=%zz":   "malformed query",
		"gs://acme-schemas/api.pb?generation=7&x=%": "malformed query",
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

// The TOKEN is cached while it is valid, not the source: every entry of a
// config and every reload reads under one mint, a lapsed token is minted
// again, and a resolution that failed is retried rather than remembered.
func TestTokenIsReusedWhileValidAndResolutionRetriedOnFailure(t *testing.T) {
	got := standIn(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	calls := 0
	failing := true
	findCredentials = func(context.Context) (oauth2.TokenSource, error) {
		calls++
		if failing {
			return nil, errors.New("binding still propagating")
		}
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fresh", Expiry: time.Now().Add(time.Hour)}), nil
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

	// A token past its expiry is not reused; the next fetch mints again.
	token = &oauth2.Token{AccessToken: "stale", Expiry: time.Now().Add(-time.Minute)}
	if _, err := fetch(t, "gs://b/o"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("credential resolutions = %d, want 3 after the token lapsed", calls)
	}
	last := (*got)[len(*got)-1]
	if last.Header.Get("Authorization") != "Bearer fresh" {
		t.Errorf("authorization = %q, want the re-minted token", last.Header.Get("Authorization"))
	}
}

// blockingSource is a credential backend that never answers, the metadata
// server of a node whose Workload Identity is misconfigured.
type blockingSource struct{ release chan struct{} }

func (b blockingSource) Token() (*oauth2.Token, error) {
	<-b.release
	return nil, errors.New("released")
}

// The fetch deadline covers credential discovery and the token exchange,
// not only the object read. Either of them hanging used to be a startup
// that never completed; now it is a fetch that fails within the budget the
// caller set, and nothing reaches the API.
func TestDeadlineBoundsCredentialDiscoveryAndMinting(t *testing.T) {
	got := standIn(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("a fetch with no token reached the API")
	})
	u, err := url.Parse("gs://b/o")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	for name, find := range map[string]func(context.Context) (oauth2.TokenSource, error){
		"discovery": func(ctx context.Context) (oauth2.TokenSource, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		"minting": func(context.Context) (oauth2.TokenSource, error) {
			return blockingSource{release}, nil
		},
	} {
		findCredentials = find
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		_, err := Fetch(ctx, u)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: error = %v, want the deadline", name, err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("%s: fetch took %s past a 50ms deadline", name, took)
		}
		if token != nil {
			t.Errorf("%s: a token was cached from a fetch that timed out", name)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("%d requests reached the API", len(*got))
	}
}

// The registry sees this module on import; without that, a gs:// entry is
// refused at config validation and this whole package is dead weight.
func TestRegistersOnImport(t *testing.T) {
	if !descriptors.Linked("gs") {
		t.Fatal("gs is not registered")
	}
}
