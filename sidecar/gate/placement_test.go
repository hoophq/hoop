package gate_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// A request that carries its own bearer in its URL is refused before any
// of it is forwarded, under its own rule and metrics source, and the trail
// holds the request line with the token replaced and no query: the ENG-871
// row held `POST /echo?t=<token>` in clear.
func TestAPlacementRefusalIsItsOwnRuleAndHoldsNoToken(t *testing.T) {
	const token = "vault-filled-token-0123456789abcdef"
	sink := &recordingSink{}
	m := &countingMetrics{}
	g, err := gate.New(newSession(), gate.Config{
		Protocol:     inspect.HTTP,
		Audit:        sink,
		Metrics:      m,
		CodecFactory: func() inspect.Codec { return codechttp.New(codechttp.Options{}) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := g.Request(context.Background(), []byte("POST /echo/"+token+"?t="+token+" HTTP/1.1\r\nHost: h\r\n"+
		"Authorization: Bearer "+token+"\r\nContent-Length: 0\r\n\r\n"))
	if d.Allowed || d.Rule != "credential-placement" || len(d.Payload) != 0 ||
		!strings.HasPrefix(d.Message, "request refused: ") || strings.Contains(d.Message, token) {
		t.Fatalf("decision = allowed %v rule %q payload %q message %q; want the placement refusal",
			d.Allowed, d.Rule, d.Payload, d.Message)
	}
	ev := sink.find(audit.KindViolation)
	if ev == nil || ev.Rule != "credential-placement" || ev.Principal != session.AnonymousPrincipal ||
		ev.Statement != "POST /echo/[redacted]" {
		t.Fatalf("violation = %+v, want a placement refusal of POST /echo/[redacted] under anonymous", ev)
	}
	raw, err := json.Marshal(sink.events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Errorf("the trail holds the token: %s", raw)
	}
	if len(m.statements) != 1 || !m.statements[0].denied || m.statements[0].source != gate.SourcePlacement {
		t.Errorf("metrics = %+v, want one denial sourced %q", m.statements, gate.SourcePlacement)
	}
}
