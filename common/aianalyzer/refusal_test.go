package aianalyzer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeProvider answers every request with body.
func fakeProvider(t *testing.T, body string) *string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	url := srv.URL + "/"
	return &url
}

// An Anthropic refusal can carry no content. It is an answer with Refusal
// set, not an empty-response error.
func TestAnthropicReportsAStopReasonRefusal(t *testing.T) {
	key := "k"
	c, err := newAnthropicClient(ProviderConfig{APIKey: &key, Model: "m", APIURL: fakeProvider(t,
		`{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],
		"stop_reason":"refusal","usage":{"input_tokens":1,"output_tokens":0}}`)})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatalf("a refusal is an answer, got %v", err)
	}
	if resp.Refusal == "" || len(resp.ToolCalls) != 0 {
		t.Errorf("response = %+v, want Refusal set and no tool call", resp)
	}
}

func TestOpenAIReportsARefusal(t *testing.T) {
	key := "k"
	c, err := newOpenAIClient(ProviderConfig{APIKey: &key, Model: "m", APIURL: fakeProvider(t,
		`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,
		"finish_reason":"stop","message":{"role":"assistant","content":null,"refusal":"I can't help with that."}}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Refusal != "I can't help with that." {
		t.Errorf("Refusal = %q", resp.Refusal)
	}
}
