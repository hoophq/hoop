package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

func newProvider(t *testing.T, endpoint string) analyzer.Provider {
	t.Helper()
	p, err := analyzer.NewProvider(Name, analyzer.Options{
		Model:      "gemini-2.5-flash",
		Endpoint:   endpoint,
		Credential: analyzer.NewSecret([]byte("k3y")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const highRiskReply = `{"candidates":[{"content":{"role":"model","parts":[
  {"functionCall":{"name":"report_high_risk","args":{"title":"Unbounded delete","explanation":"No predicate."}}}
]},"finishReason":"STOP"}]}`

// The key travels in a header. The query form Google's quickstarts use
// would print the key in access logs and in /config.
func TestClassifySendsKeyInHeaderAndReadsFunctionCall(t *testing.T) {
	srv := httptest.NewServer(nil)
	var got *http.Request
	var gotBody []byte
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, highRiskReply)
	})
	t.Cleanup(srv.Close)

	res, err := newProvider(t, srv.URL+"/v1beta/models/m:generateContent").
		Classify(context.Background(), "system", "DELETE FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if res.RiskLevel != analyzer.RiskHigh {
		t.Errorf("risk = %q, want %q", res.RiskLevel, analyzer.RiskHigh)
	}
	if res.Title != "Unbounded delete" || res.Explanation != "No predicate." {
		t.Errorf("title/explanation = %q/%q", res.Title, res.Explanation)
	}
	if got.Header.Get("x-goog-api-key") != "k3y" {
		t.Errorf("x-goog-api-key = %q, want k3y", got.Header.Get("x-goog-api-key"))
	}
	if got.URL.RawQuery != "" {
		t.Errorf("key leaked into query: %q", got.URL.RawQuery)
	}

	var req Request
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatal(err)
	}
	if req.SystemInstruction.Parts[0].Text != "system" {
		t.Errorf("systemInstruction = %+v", req.SystemInstruction)
	}
	if req.Contents[0].Role != "user" || req.Contents[0].Parts[0].Text != "DELETE FROM t" {
		t.Errorf("contents = %+v", req.Contents)
	}
	if req.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Errorf("mode = %q, want ANY: a prose answer would be a parse failure", req.ToolConfig.FunctionCallingConfig.Mode)
	}
	if n := len(req.Tools[0].FunctionDeclarations); n != 3 {
		t.Errorf("declared %d tools, want 3", n)
	}
}

// The api key selects the host, and an unknown value fails at config load
// rather than sending a Vertex key to generativelanguage.
func TestDefaultEndpointPerAPI(t *testing.T) {
	for _, tc := range []struct {
		api, want string
	}{
		{"", "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"},
		{APIDeveloper, "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"},
		{APIVertex, "https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-flash:generateContent"},
	} {
		p, err := analyzer.NewProvider(Name, analyzer.Options{
			Model:      "gemini-2.5-flash",
			Credential: analyzer.NewSecret([]byte("k")),
			Extra:      map[string]string{KeyAPI: tc.api},
		})
		if err != nil {
			t.Fatalf("api %q: %v", tc.api, err)
		}
		if got := p.(*Provider).endpoint; got != tc.want {
			t.Errorf("api %q: endpoint = %q, want %q", tc.api, got, tc.want)
		}
	}

	_, err := analyzer.NewProvider(Name, analyzer.Options{
		Model:      "m",
		Credential: analyzer.NewSecret([]byte("k")),
		Extra:      map[string]string{KeyAPI: "openai"},
	})
	if err == nil || !strings.Contains(err.Error(), `unknown api "openai"`) {
		t.Errorf("unknown api: err = %v", err)
	}
}

// Both refusal shapes read as a refusal, and prose reads as "no risk tool".
// The words differ because the operator's next step differs.
func TestParseResponseDistinguishesRefusalFromProse(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			name: "prompt blocked",
			body: `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"},"candidates":[]}`,
			want: `refused to classify this statement (block_reason "PROHIBITED_CONTENT")`,
		},
		{
			name: "reply blocked",
			body: `{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"SAFETY"}]}`,
			want: `refused to classify this statement (finish_reason "SAFETY")`,
		},
		{
			name: "prose",
			body: `{"candidates":[{"content":{"role":"model","parts":[{"text":"Looks fine."}]},"finishReason":"STOP"}]}`,
			want: `called no risk tool (finish_reason "STOP")`,
		},
		{
			name: "unknown tool",
			body: `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"report_no_risk","args":{}}}]},"finishReason":"STOP"}]}`,
			want: `called no risk tool (finish_reason "STOP")`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := newProvider(t, srv.URL).Classify(context.Background(), "s", "c")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// A 4xx body from Google echoes the request. None of it may reach the error
// an operator pastes into a ticket.
func TestParseResponseKeepsErrorBodyOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"DELETE FROM customers WHERE ssn = '123-45-6789'"}}`)
	}))
	defer srv.Close()
	_, err := newProvider(t, srv.URL).Classify(context.Background(), "s", "c")
	if err == nil {
		t.Fatal("want error on 403")
	}
	if strings.Contains(err.Error(), "customers") || strings.Contains(err.Error(), "6789") {
		t.Errorf("error leaks the provider body: %v", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error names no status: %v", err)
	}
}

// A malformed args object costs the title, never the verdict.
func TestParseResponseKeepsLevelWhenArgsMalformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"report_medium_risk","args":"oops"}}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()
	res, err := newProvider(t, srv.URL).Classify(context.Background(), "s", "c")
	if err != nil {
		t.Fatal(err)
	}
	if res.RiskLevel != analyzer.RiskMedium || res.Title != "" {
		t.Errorf("result = %+v, want medium with empty title", res)
	}
}
