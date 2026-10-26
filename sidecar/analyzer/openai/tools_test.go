package openai

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
		},
		Required: []string{"verdict"},
	},
}

func newTestProvider(t *testing.T, url string) analyzer.ToolCaller {
	t.Helper()
	p, err := analyzer.NewProvider(Name, analyzer.Options{
		Endpoint: url, Model: "gpt", Credential: analyzer.NewSecret([]byte("k")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p.(analyzer.ToolCaller)
}

// The caller's tools reach the model as functions with their schema, a call
// is required, and every call comes back; arguments that are not JSON come
// back as nil Args.
func TestDecideSendsCallerToolsAndReturnsEveryCall(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"tool_calls":[
		  {"function":{"name":"decide","arguments":"{\"verdict\":\"allow\"}"}},
		  {"function":{"name":"decide","arguments":"not json"}}]}}]}`)
	}))
	defer srv.Close()

	calls, err := newTestProvider(t, srv.URL).Decide(context.Background(), "sys", "stmt", []analyzer.ToolSpec{verdictTool})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || string(calls[0].Args) != `{"verdict":"allow"}` || calls[1].Args != nil {
		t.Fatalf("calls = %+v", calls)
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.ToolChoice != "required" || req.Messages[0].Content != "sys" {
		t.Fatalf("request = %s", body)
	}
	params := req.Tools[0].Function.Parameters
	if v := params.Properties["verdict"]; strings.Join(v.Enum, ",") != "allow,block" || v.Type != "string" {
		t.Errorf("verdict = %+v", v)
	}
	if strings.Join(params.Required, ",") != "verdict" {
		t.Errorf("required = %v", params.Required)
	}
}

// A refusal or a content filter is an error, even beside a call; an empty
// reply is not.
func TestDecideReportsARefusal(t *testing.T) {
	for reply, wantErr := range map[string]bool{
		`{"choices":[{"message":{"refusal":"I can't help with that."}}]}`:                                                                                  true,
		`{"choices":[{"message":{"refusal":"no","tool_calls":[{"function":{"name":"decide","arguments":"{\"verdict\":\"allow\"}"}}]}}]}`:                   true,
		`{"choices":[{"message":{"tool_calls":[{"function":{"name":"decide","arguments":"{\"verdict\":\"allow\"}"}}]},"finish_reason":"content_filter"}]}`: true,
		`{"choices":[{"message":{"content":"prose"}}]}`:                                                                                                    false,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, reply)
		}))
		calls, err := newTestProvider(t, srv.URL).Decide(context.Background(), "s", "c", []analyzer.ToolSpec{verdictTool})
		srv.Close()
		if (err != nil) != wantErr || len(calls) != 0 {
			t.Errorf("%s: calls=%v err=%v, want error %t", reply, calls, err, wantErr)
		}
	}
}
