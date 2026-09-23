package vertex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/analyzer/gemini"
	"golang.org/x/oauth2"
)

// withToken installs a fixed bearer so a test never touches GCP. The Once
// is spent on purpose: tokenSource must not go looking for ADC afterwards.
func withToken(p *Provider, tok string) {
	p.tokenOnce.Do(func() {})
	p.tokenSrc = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tok})
}

func build(t *testing.T, opts analyzer.Options) *Provider {
	t.Helper()
	if opts.Extra == nil {
		opts.Extra = map[string]string{}
	}
	if opts.Extra[KeyProject] == "" {
		opts.Extra[KeyProject] = "proj"
	}
	if opts.Extra[KeyRegion] == "" {
		opts.Extra[KeyRegion] = "us-central1"
	}
	if opts.Model == "" {
		opts.Model = "m"
	}
	p, err := analyzer.NewProvider(Name, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*Provider)
}

// The publisher selects the method as well as the path segment. Getting the
// method wrong yields a 404 that reads like a typo in the model name.
func TestURLPerPublisher(t *testing.T) {
	for _, tc := range []struct {
		publisher, region, want string
	}{
		{"", "us-central1", "https://us-central1-aiplatform.googleapis.com/v1/projects/proj/locations/us-central1/publishers/anthropic/models/m:rawPredict"},
		{PublisherAnthropic, "global", "https://aiplatform.googleapis.com/v1/projects/proj/locations/global/publishers/anthropic/models/m:rawPredict"},
		{PublisherGoogle, "us-central1", "https://us-central1-aiplatform.googleapis.com/v1/projects/proj/locations/us-central1/publishers/google/models/m:generateContent"},
		{PublisherGoogle, "global", "https://aiplatform.googleapis.com/v1/projects/proj/locations/global/publishers/google/models/m:generateContent"},
	} {
		p := build(t, analyzer.Options{Extra: map[string]string{KeyPublisher: tc.publisher, KeyRegion: tc.region}})
		if got := p.url(); got != tc.want {
			t.Errorf("publisher %q region %q: url = %q, want %q", tc.publisher, tc.region, got, tc.want)
		}
	}
}

func TestUnknownPublisherFailsAtLoad(t *testing.T) {
	_, err := analyzer.NewProvider(Name, analyzer.Options{
		Model: "m",
		Extra: map[string]string{KeyProject: "p", KeyRegion: "r", KeyPublisher: "openai"},
	})
	if err == nil || !strings.Contains(err.Error(), `unknown publisher "openai"`) {
		t.Errorf("err = %v", err)
	}
}

// Gemini on Vertex is the gemini encoder under a bearer. The reply is the
// gemini document, so a functionCall becomes a verdict.
func TestClassifyGooglePublisherSendsBearerAndGeminiBody(t *testing.T) {
	var got *http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[
		  {"functionCall":{"name":"report_low_risk","args":{"title":"Bounded read","explanation":"Single row."}}}
		]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	p := build(t, analyzer.Options{
		Endpoint: srv.URL,
		Extra:    map[string]string{KeyPublisher: PublisherGoogle},
	})
	withToken(p, "tok")

	res, err := p.Classify(context.Background(), "system", "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if res.RiskLevel != analyzer.RiskLow || res.Title != "Bounded read" {
		t.Errorf("result = %+v", res)
	}
	if auth := got.Header.Get("authorization"); auth != "Bearer tok" {
		t.Errorf("authorization = %q, want Bearer tok", auth)
	}
	if got.Header.Get("x-goog-api-key") != "" {
		t.Error("an API key header on the bearer path")
	}

	var req gemini.Request
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("body is not a gemini request: %v", err)
	}
	if req.SystemInstruction.Parts[0].Text != "system" || req.Contents[0].Parts[0].Text != "SELECT 1" {
		t.Errorf("body = %s", gotBody)
	}
}

// The default publisher is unchanged: a config written before the key
// existed still sends the Anthropic body.
func TestClassifyDefaultPublisherSendsAnthropicBody(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"content":[{"type":"tool_use","name":"report_high_risk","input":{"title":"t","explanation":"e"}}],"stop_reason":"tool_use"}`)
	}))
	defer srv.Close()

	p := build(t, analyzer.Options{Endpoint: srv.URL})
	withToken(p, "tok")

	res, err := p.Classify(context.Background(), "system", "DROP TABLE t")
	if err != nil {
		t.Fatal(err)
	}
	if res.RiskLevel != analyzer.RiskHigh {
		t.Errorf("risk = %q", res.RiskLevel)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["anthropic_version"]; !ok {
		t.Errorf("anthropic body missing anthropic_version: %s", gotBody)
	}
	if _, ok := body["contents"]; ok {
		t.Errorf("anthropic path sent a gemini body: %s", gotBody)
	}
}

// userCredential renders an authorized_user file, the shape
// `gcloud auth application-default login` writes, with token_uri pointing at
// a local server. Its refresh request carries the token source's context;
// the service-account JWT flow in oauth2 v0.36 does not, so a test built on
// a service-account key would pass with the bug present.
func userCredential(tokenURI string) []byte {
	cred, _ := json.Marshal(map[string]string{
		"type":          "authorized_user",
		"client_id":     "cid",
		"client_secret": "csecret",
		"refresh_token": "rt",
		"token_uri":     tokenURI,
	})
	return cred
}

// The daemon cancels the Verify context as soon as validation returns. The
// token source must outlive it: oauth2 keeps the context it was built with
// for every refresh, so a source built on the startup context mints once and
// then fails every refresh an hour later with "context canceled". Workload
// Identity Federation and gcloud user credentials both refresh this way.
//
// expires_in: 1 makes every Token() call a refresh, which stands in for the
// hour.
func TestTokenSourceOutlivesVerifyContext(t *testing.T) {
	var mints atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		mints.Add(1)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"tok","token_type":"Bearer","expires_in":1}`)
	})
	mux.HandleFunc("/predict", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"report_low_risk","args":{}}}]},"finishReason":"STOP"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := build(t, analyzer.Options{
		Endpoint:   srv.URL + "/predict",
		Credential: analyzer.NewSecret(userCredential(srv.URL + "/token")),
		Extra:      map[string]string{KeyPublisher: PublisherGoogle},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := p.Verify(ctx); err != nil {
		cancel()
		t.Fatalf("Verify: %v", err)
	}
	cancel()

	if _, err := p.Classify(context.Background(), "s", "c"); err != nil {
		t.Fatalf("Classify after the Verify context was canceled: %v", err)
	}
	if n := mints.Load(); n != 2 {
		t.Errorf("token endpoint saw %d mints, want 2 (one for Verify, one refresh for Classify)", n)
	}
}

// Verify must return when its context ends, or -validate hangs on a token
// endpoint that never answers.
func TestVerifyHonorsContextDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))

	p := build(t, analyzer.Options{
		Credential: analyzer.NewSecret(userCredential(srv.URL)),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := p.Verify(ctx)
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}
