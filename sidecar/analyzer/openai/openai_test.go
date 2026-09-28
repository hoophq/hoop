package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// OpenAI's reasoning models reject max_tokens. Vertex uses this encoder with
// the other spelling, so the direct path must keep max_completion_tokens.
func TestClassifyDirectSendsMaxCompletionTokens(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[
		  {"function":{"name":"report_low_risk","arguments":"{\"title\":\"t\",\"explanation\":\"e\"}"}}
		]}}]}`)
	}))
	defer srv.Close()

	p, err := analyzer.NewProvider(Name, analyzer.Options{
		Endpoint:        srv.URL,
		Model:           "gpt-5",
		MaxOutputTokens: 256,
		Credential:      analyzer.NewSecret([]byte("k")),
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Classify(context.Background(), "system", "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if res.RiskLevel != analyzer.RiskLow {
		t.Errorf("risk = %q", res.RiskLevel)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if string(body["max_completion_tokens"]) != "256" {
		t.Errorf("max_completion_tokens = %s, want 256: %s", body["max_completion_tokens"], gotBody)
	}
	if _, ok := body["max_tokens"]; ok {
		t.Errorf("direct path sent max_tokens: %s", gotBody)
	}
}
