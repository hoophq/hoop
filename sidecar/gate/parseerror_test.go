package gate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// lineOrJunkCodec decodes newline-terminated lines as statements and refuses
// a line that starts with 0xff, the way a real codec refuses bytes that are
// malformed for its protocol.
type lineOrJunkCodec struct{}

func (lineOrJunkCodec) Protocol() inspect.Protocol { return inspect.Postgres }
func (lineOrJunkCodec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	var stmts []inspect.Statement
	consumed := 0
	for {
		rest := data[consumed:]
		if len(rest) > 0 && rest[0] == 0xff {
			return stmts, consumed, errors.New("unexpected message type 0xff")
		}
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			return stmts, consumed, nil
		}
		stmts = append(stmts, inspect.Statement{
			Protocol: inspect.Postgres, Direction: dir, Text: string(rest[:i]), Operation: inspect.OpSelect,
		})
		consumed += i + 1
	}
}

// parseErrorOPA denies a statement carrying input.metadata.parse_error and
// allows the rest, recording every input it was handed.
func parseErrorOPA(t *testing.T) (string, func() []map[string]any) {
	t.Helper()
	var (
		mu     sync.Mutex
		inputs []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input map[string]any `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		mu.Lock()
		inputs = append(inputs, body.Input)
		mu.Unlock()
		result := `{"result":{"allow":true}}`
		if md, _ := body.Input["metadata"].(map[string]any); md[inspect.MetadataParseError] != nil {
			result = `{"result":{"allow":false,"rule":"parse-error","message":"undecodable request"}}`
		}
		if _, err := w.Write([]byte(result)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return inputs
	}
}

func newJunkGate(t *testing.T, pol policy.Evaluator, sink audit.Sink) *gate.Gate {
	t.Helper()
	g, err := gate.New(newSession(), gate.Config{
		Protocol:     inspect.Postgres,
		Policy:       pol,
		Audit:        sink,
		CodecFactory: func() inspect.Codec { return lineOrJunkCodec{} },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

var junkChunk = []byte("SELECT 1\n\xffjunk")

// Client bytes the codec cannot decode reach OPA as an `unknown` statement
// carrying the reason, after the statements decoded before them, and an OPA
// denial stops them.
func TestOPADecidesOnClientBytesTheCodecCannotDecode(t *testing.T) {
	url, inputs := parseErrorOPA(t)
	sink := &recordingSink{}
	g := newJunkGate(t, &policy.OPAClient{URL: url}, sink)

	d := g.Request(context.Background(), junkChunk)
	if d.Allowed || len(d.Payload) != 0 {
		t.Fatalf("undecodable bytes forwarded: allowed=%v payload=%q", d.Allowed, d.Payload)
	}
	if d.Rule != "parse-error" || d.Err == nil {
		t.Errorf("rule=%q err=%v, want OPA's rule and the decode error", d.Rule, d.Err)
	}
	if d.DeniedStatement == nil || d.DeniedStatement.Metadata[inspect.MetadataParseError] == "" {
		t.Errorf("DeniedStatement = %+v, want the parse-error statement", d.DeniedStatement)
	}

	got := inputs()
	if len(got) != 2 || got[0]["statement"] != "SELECT 1" {
		t.Fatalf("OPA inputs = %v, want SELECT 1 then the parse error", got)
	}
	md, _ := got[1]["metadata"].(map[string]any)
	if got[1]["operation"] != string(inspect.OpUnknown) || got[1]["direction"] != string(inspect.FromClient) ||
		!strings.Contains(md[inspect.MetadataParseError].(string), "0xff") {
		t.Errorf("parse-error input = %v", got[1])
	}

	ev := sink.find(audit.KindViolation)
	if ev == nil || ev.Metadata[inspect.MetadataParseError] == "" {
		t.Errorf("violation = %+v, want one carrying parse_error", ev)
	}
}

// A policy that allows the parse error forwards every byte, as before; a
// lane with no policy forwards them unjudged; and response bytes keep the
// forwarding default whatever the policy says.
func TestUndecodableBytesForwardUnlessPolicyRefuses(t *testing.T) {
	ctx := context.Background()

	t.Run("policy allows", func(t *testing.T) {
		url, _ := echoOPA(t)
		d := newJunkGate(t, &policy.OPAClient{URL: url}, nil).Request(ctx, junkChunk)
		if !d.Allowed || !bytes.Equal(d.Payload, junkChunk) || d.Err == nil {
			t.Fatalf("allowed=%v payload=%q err=%v, want the whole chunk forwarded", d.Allowed, d.Payload, d.Err)
		}
	})

	t.Run("no policy", func(t *testing.T) {
		sink := &recordingSink{}
		d := newJunkGate(t, nil, sink).Request(ctx, junkChunk)
		if !d.Allowed || !bytes.Equal(d.Payload, junkChunk) {
			t.Fatalf("allowed=%v payload=%q, want the whole chunk forwarded", d.Allowed, d.Payload)
		}
		if sink.find(audit.KindError) == nil {
			t.Error("the decode error was not audited")
		}
	})

	t.Run("response", func(t *testing.T) {
		url, inputs := parseErrorOPA(t)
		d := newJunkGate(t, &policy.OPAClient{URL: url}, nil).Response(ctx, junkChunk)
		if !d.Allowed || !bytes.Equal(d.Payload, junkChunk) {
			t.Fatalf("allowed=%v payload=%q, want the response forwarded", d.Allowed, d.Payload)
		}
		if len(inputs()) != 1 {
			t.Errorf("OPA saw %d statements, want only the decoded response", len(inputs()))
		}
	})
}

// A local rule naming `unknown` refuses undecodable client bytes too, and its
// generated message says why.
func TestUnknownRuleRefusesUndecodableClientBytes(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{{
		Name: "fail-closed", Type: policy.MatchOperation, Operations: []inspect.Operation{inspect.OpUnknown},
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	d := newJunkGate(t, rules, nil).Request(context.Background(), junkChunk)
	if d.Allowed || len(d.Payload) != 0 {
		t.Fatalf("undecodable bytes forwarded: %+v", d)
	}
	if !strings.Contains(d.Message, "parse error: unexpected message type 0xff") {
		t.Errorf("message = %q, want the decode error named", d.Message)
	}
}

// parseErrorMaskPolicy applies a response mask to the statement that stands
// for undecodable bytes, as the AI analyzer could.
type parseErrorMaskPolicy struct{}

func (parseErrorMaskPolicy) Evaluate(inspect.Statement) policy.Verdict { return policy.Verdict{} }

func (parseErrorMaskPolicy) EvaluateWith(s inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if s.Metadata[inspect.MetadataParseError] != "" {
		ec.MaskResponse(aiMask)
	}
	return policy.Verdict{}
}

// reframingJunkCodec is lineOrJunkCodec with a Reframer, so a gate can run
// a mask scope over it.
type reframingJunkCodec struct{ lineOrJunkCodec }

func (reframingJunkCodec) Rewrite(data []byte, _ func(string, []byte) []byte) ([]byte, inspect.ReframeResult, error) {
	return data, inspect.ReframeResult{}, nil
}
func (reframingJunkCodec) Flush(func(string, []byte) []byte) []byte { return nil }

// A mask on undecodable client bytes is refused: the codec lost the stream,
// so no response can be tied to them. Before, the mask was dropped and the
// bytes were forwarded.
func TestAMaskOnUndecodableClientBytesIsRefused(t *testing.T) {
	g, err := gate.New(newSession(), gate.Config{
		Protocol:     inspect.Postgres,
		Policy:       parseErrorMaskPolicy{},
		MaskScope:    gate.MaskScopeSerial,
		CodecFactory: func() inspect.Codec { return reframingJunkCodec{} },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := g.Request(context.Background(), junkChunk)
	if d.Allowed || len(d.Payload) != 0 || d.Rule != "ai-mask" || !strings.Contains(d.Message, "could not decode") {
		t.Fatalf("masked undecodable bytes: allowed=%v rule=%q message=%q payload=%q",
			d.Allowed, d.Rule, d.Message, d.Payload)
	}
}
