package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
	codecgrpc "github.com/hoophq/libhoop/v2/codec/grpc"
)

// returnMCPPolicy is a return lane in a process with an mcp: block, built
// through setupAnalyzer so the block is what turns the clause on.
func returnMCPPolicy(t *testing.T, mcp bool) (policy.Evaluator, *[]reviewCall) {
	t.Helper()
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	cfg := &Config{Analyzer: &AnalyzerConfig{Provider: "stub", Model: "m"}, cp: cp}
	if mcp {
		cfg.MCP = &MCPConfig{Listen: "127.0.0.1:8765"}
	}
	deps, err := setupAnalyzer(cfg, nil)
	if err != nil {
		t.Fatalf("setupAnalyzer: %v", err)
	}
	deps.provider = highRiskProvider{}

	la := holdingBlock()
	la.Trigger = nil
	la.ReviewMode = analyzer.ReviewReturn
	pol, err := buildPolicy("agents", GuardrailsConfig{}, la, nil, nil, deps)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	return pol, calls
}

// wantMCPClause checks the text a client decoded from its error frame.
func wantMCPClause(t *testing.T, got string) {
	t.Helper()
	if !strings.HasPrefix(got, "review 9f97: ") || !strings.Contains(got, "call the MCP tool review_wait") ||
		!strings.Contains(got, "resend the identical statement") {
		t.Errorf("the client read %q, want the review id and the review_wait clause", got)
	}
}

// A sidecar without an mcp: block keeps today's return-mode message.
func TestAReturnDenyWithoutMCPNamesNoTool(t *testing.T) {
	pol, _ := returnMCPPolicy(t, false)
	v := pol.Evaluate(inspect.Statement{
		Protocol: inspect.Postgres, Direction: inspect.FromClient,
		Text: "DELETE FROM users", Operation: inspect.OpDelete, Tables: []string{"users"},
	})
	if !v.Denied || strings.Contains(v.Message, "review_wait") || !strings.Contains(v.Message, "9f97") {
		t.Errorf("denied=%v message=%q, want today's denial with the id and no tool", v.Denied, v.Message)
	}
}

// SQL: every relayed SQL frame carries the whole clause and the id.
func TestAReturnDenyNamesTheMCPToolInEachSQLFrame(t *testing.T) {
	for _, tc := range []struct {
		proto inspect.Protocol
		read  func(frame []byte) string
	}{
		{inspect.Postgres, postgresErrorMessage},
		{inspect.MySQL, func(f []byte) string { return string(f[4+9:]) }},
		{inspect.MSSQL, mssqlErrorMessage},
	} {
		t.Run(string(tc.proto), func(t *testing.T) {
			pol, calls := returnMCPPolicy(t, true)
			v := pol.Evaluate(inspect.Statement{
				Protocol: tc.proto, Direction: inspect.FromClient,
				Text: "DELETE FROM users", Operation: inspect.OpDelete, Tables: []string{"users"},
			})
			if !v.Denied {
				t.Fatal("a pending review was forwarded")
			}
			wantMCPClause(t, tc.read(proxy.ProtocolDenyWriter{}.Deny(tc.proto, inspect.FromClient, v.Message)))
			if len(*calls) != 1 {
				t.Errorf("the plane saw %d requests, want 1 filing and no claim", len(*calls))
			}
		})
	}
}

// HTTP: the 403 body carries the clause and the id.
func TestAReturnDenyNamesTheMCPToolInTheHTTPBody(t *testing.T) {
	pol, _ := returnMCPPolicy(t, true)
	v := pol.Evaluate(inspect.Statement{
		Protocol: inspect.HTTP, Direction: inspect.FromClient,
		Text: "DELETE /users/1", Operation: inspect.OpDelete,
		HTTP: &inspect.HTTPDetail{Method: "DELETE", Path: "/users/1", Resource: "/users/*"},
	})
	frame := string(proxy.HTTPForbidden(v.Message))
	_, body, ok := strings.Cut(frame, "\r\n\r\n")
	if !ok || !strings.HasPrefix(frame, "HTTP/1.1 403") {
		t.Fatalf("not a 403 response: %q", frame)
	}
	wantMCPClause(t, strings.TrimSuffix(body, "\n"))
}

// gRPC: through a real lane, grpc-message carries the clause and the id.
func TestAReturnDenyNamesTheMCPToolInTheGRPCStatus(t *testing.T) {
	pol, _ := returnMCPPolicy(t, true)
	upstream, _ := firstReadUpstream(t)
	laneAddr, stop := startGRPCTestServer(t,
		buildHoldTestServer(t, "grpc", upstream, writeGRPCTestDescriptors(t), pol))
	defer stop()

	resp, err := holdTestRoundTrip(context.Background(), laneAddr, "/test.v1.Echo/Say",
		marshalGRPCTestMessage("s", "v"))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got := spannerTestStatus(resp); got != "7" {
		t.Fatalf("grpc-status = %q, want 7", got)
	}
	msg := resp.Trailer.Get("Grpc-Message")
	if msg == "" {
		msg = resp.Header.Get("Grpc-Message")
	}
	wantMCPClause(t, codecgrpc.DecodeMessage(msg))
}

// postgresErrorMessage reads the M field of an ErrorResponse.
func postgresErrorMessage(frame []byte) string {
	for _, field := range bytes.Split(frame[5:], []byte{0}) {
		if len(field) > 0 && field[0] == 'M' {
			return string(field[1:])
		}
	}
	return ""
}

// mssqlErrorMessage reads the message of the first ERROR token: packet
// header(8), token(1), length(2), number(4), state(1), class(1), MsgLen(2).
func mssqlErrorMessage(frame []byte) string {
	chars := int(binary.LittleEndian.Uint16(frame[17:19]))
	units := make([]uint16, chars)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(frame[19+2*i:])
	}
	return string(utf16.Decode(units))
}
