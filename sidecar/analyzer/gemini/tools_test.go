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

var verdictTool = analyzer.ToolSpec{
	Name:        "decide",
	Description: "Decide.",
	Input: analyzer.ToolInput{
		Properties: map[string]analyzer.ToolProperty{
			"verdict": {Type: "string", Description: "The verdict.", Enum: []string{"allow", "block"}},
			"reason":  {Type: "string", Description: "Why."},
		},
		Required: []string{"verdict"},
	},
}

// The caller's tools reach the model as function declarations in Gemini's
// schema dialect (upper-case types, format enum), a call is forced, and every
// functionCall comes back.
func TestDecideSendsCallerToolsAndReturnsEveryCall(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[
		  {"functionCall":{"name":"decide","args":{"verdict":"block"}}},
		  {"functionCall":{"name":"decide","args":{"verdict":"allow"}}}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	calls, err := newProvider(t, srv.URL).(analyzer.ToolCaller).
		Decide(context.Background(), "sys", "stmt", []analyzer.ToolSpec{verdictTool})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Name != "decide" || string(calls[0].Args) != `{"verdict":"block"}` {
		t.Fatalf("calls = %+v", calls)
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.ToolConfig.FunctionCallingConfig.Mode != "ANY" || len(req.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("request = %s", body)
	}
	params := req.Tools[0].FunctionDeclarations[0].Parameters
	v := params.Properties["verdict"]
	if v.Type != "STRING" || v.Format != "enum" || strings.Join(v.Enum, ",") != "allow,block" {
		t.Errorf("verdict = %+v", v)
	}
	if r := params.Properties["reason"]; r.Format != "" || r.Enum != nil {
		t.Errorf("reason = %+v, want no enum", r)
	}
	if strings.Join(params.Required, ",") != "verdict" {
		t.Errorf("required = %v", params.Required)
	}
}

// A blocked prompt, a safety stop or a reply cut off at the token limit on
// any candidate is an error, even beside a call; a reply with no call is not.
func TestDecideReportsARefusal(t *testing.T) {
	for reply, wantErr := range map[string]bool{
		`{"promptFeedback":{"blockReason":"SAFETY"}}`:                                                                                                                                               true,
		`{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`:                                                                                                                         true,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"decide","args":{"verdict":"allow"}}}]},"finishReason":"SAFETY"}]}`:                                                            true,
		`{"promptFeedback":{"blockReason":"OTHER"},"candidates":[{"content":{"parts":[{"functionCall":{"name":"decide","args":{"verdict":"allow"}}}]},"finishReason":"STOP"}]}`:                     true,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"decide","args":{"verdict":"allow"}}}]},"finishReason":"STOP"},{"content":{"parts":[]},"finishReason":"PROHIBITED_CONTENT"}]}`: true,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"decide","args":{"verdict":"allow"}}}]},"finishReason":"MAX_TOKENS"}]}`:                                                        true,
		`{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"STOP"}]}`:                                                                                                               false,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, reply)
		}))
		calls, err := newProvider(t, srv.URL).(analyzer.ToolCaller).
			Decide(context.Background(), "s", "c", []analyzer.ToolSpec{verdictTool})
		srv.Close()
		if (err != nil) != wantErr || len(calls) != 0 {
			t.Errorf("%s: calls=%v err=%v, want error %t", reply, calls, err, wantErr)
		}
	}
}

// The classification request carries no format or enum on the risk tools.
func TestTheClassificationRequestHasNoEnum(t *testing.T) {
	body, err := json.Marshal(BuildRequest(GenerationConfig{MaxOutputTokens: 1}, "s", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"format"`) || strings.Contains(string(body), `"enum"`) {
		t.Errorf("classification request changed: %s", body)
	}
}
