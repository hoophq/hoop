package vertex

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// Decide delegates to the publisher's encoder and parser under the bearer,
// like Classify: the caller's tool reaches the body and its call comes back.
func TestDecideUsesThePublisherWireFormat(t *testing.T) {
	tool := analyzer.ToolSpec{Name: "approve", Description: "Approve.", Input: analyzer.ToolInput{
		Properties: map[string]analyzer.ToolProperty{"reason": {Type: "string", Description: "Why."}},
		Required:   []string{"reason"},
	}}
	for _, tc := range []struct {
		publisher, model, reply, bodyMarker string
	}{
		{PublisherAnthropic, "m",
			`{"content":[{"type":"tool_use","name":"approve","input":{"reason":"ok"}}],"stop_reason":"tool_use"}`,
			`"anthropic_version"`},
		{PublisherGoogle, "m",
			`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"approve","args":{"reason":"ok"}}}]},"finishReason":"STOP"}]}`,
			`"functionDeclarations"`},
		{PublisherOpenAPI, "meta/m",
			`{"choices":[{"message":{"tool_calls":[{"function":{"name":"approve","arguments":"{\"reason\":\"ok\"}"}}]}}]}`,
			`"tool_choice":"required"`},
	} {
		t.Run(tc.publisher, func(t *testing.T) {
			var body []byte
			var auth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				auth = r.Header.Get("authorization")
				_, _ = io.WriteString(w, tc.reply)
			}))
			defer srv.Close()

			p := build(t, analyzer.Options{Endpoint: srv.URL, Model: tc.model,
				Extra: map[string]string{KeyPublisher: tc.publisher}})
			withToken(p, "tok")
			calls, err := p.Decide(context.Background(), "sys", "stmt", []analyzer.ToolSpec{tool})
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 1 || calls[0].Name != "approve" || !strings.Contains(string(calls[0].Args), `"ok"`) {
				t.Fatalf("calls = %+v", calls)
			}
			if auth != "Bearer tok" || !strings.Contains(string(body), tc.bodyMarker) ||
				!strings.Contains(string(body), `"approve"`) || strings.Contains(string(body), "report_high_risk") {
				t.Errorf("auth %q body %s", auth, body)
			}
		})
	}
}
