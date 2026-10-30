package anthropic

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

func newTestProvider(t *testing.T, url string) analyzer.ToolCaller {
	t.Helper()
	p, err := analyzer.NewProvider(Name, analyzer.Options{
		Endpoint: url, Model: "claude", Credential: analyzer.NewSecret([]byte("k")),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p.(analyzer.ToolCaller)
}

// The caller's tools reach the model with their schema, a call is forced,
// and every tool_use block comes back in order, unknown names included.
func TestDecideSendsCallerToolsAndReturnsEveryCall(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"stop_reason":"tool_use","content":[
		  {"type":"text","text":"thinking"},
		  {"type":"tool_use","name":"decide","input":{"verdict":"block"}},
		  {"type":"tool_use","name":"other","input":{}}]}`)
	}))
	defer srv.Close()

	calls, err := newTestProvider(t, srv.URL).Decide(context.Background(), "sys", "stmt", []analyzer.ToolSpec{verdictTool})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Name != "decide" || string(calls[0].Args) != `{"verdict":"block"}` || calls[1].Name != "other" {
		t.Fatalf("calls = %+v", calls)
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.ToolChoice.Type != "any" || req.System != "sys" || req.Messages[0].Content != "stmt" {
		t.Fatalf("request = %s", body)
	}
	schema := req.Tools[0].InputSchema
	if v := schema.Properties["verdict"]; v.Type != "string" || strings.Join(v.Enum, ",") != "allow,block" {
		t.Errorf("verdict = %+v", v)
	}
	if strings.Join(schema.Required, ",") != "verdict" {
		t.Errorf("required = %v", schema.Required)
	}
}

// A refusal or a reply cut off at a limit is an error, even beside a call; a
// reply with no call is not, the caller judges it.
func TestDecideReportsARefusal(t *testing.T) {
	for reply, wantErr := range map[string]bool{
		`{"stop_reason":"refusal","content":[]}`: true,
		`{"stop_reason":"refusal","content":[{"type":"tool_use","name":"decide","input":{"verdict":"allow"}}]}`:                       true,
		`{"stop_reason":"max_tokens","content":[{"type":"tool_use","name":"decide","input":{"verdict":"allow"}}]}`:                    true,
		`{"stop_reason":"model_context_window_exceeded","content":[{"type":"tool_use","name":"decide","input":{"verdict":"allow"}}]}`: true,
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"x"}]}`:                                                           false,
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

// The classification request keeps its wire shape: the risk tools carry no
// enum, so the bytes every deployed provider sends do not change.
func TestTheClassificationRequestKeepsItsShape(t *testing.T) {
	body, err := json.Marshal(BuildRequest("m", 10, analyzer.Sampling{}, "s", "c", false))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"name":"report_high_risk","description":"Report that the statement is high risk.",` +
		`"input_schema":{"type":"object","properties":{"explanation":{"type":"string","description":` +
		`"Why the statement carries this risk. Never quote a value from the statement."},"title":` +
		`{"type":"string","description":"Under 80 characters. Shown to the user when the statement is ` +
		`blocked. Never quote a value from the statement."}},"required":["title","explanation"]}}],` +
		`"tool_choice":{"type":"any"}}`
	if !strings.HasSuffix(string(body), want) {
		t.Errorf("classification request changed:\n%s", body)
	}
}
