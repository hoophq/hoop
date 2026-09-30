package daemon

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func holdingBlock() *LaneAnalyzerConfig { return holdingLane().Listeners[0].Analyzer }

// Every http lane exposes the header, holding or not: a reload can turn
// holding on and keep the running codec. The stored block stays as the
// operator wrote it.
func TestAnHTTPLaneCapturesTheReviewModeHeader(t *testing.T) {
	stored := &HTTPCodecConfig{Headers: []string{"accept"}}
	for name, h := range map[string]*HTTPCodecConfig{"no http block": nil, "operator headers": stored} {
		t.Run(name, func(t *testing.T) {
			f := laneCodecFactory(inspect.HTTP, h, nil, "")
			if f == nil {
				t.Fatal("an http lane kept the registry codec, which captures no header")
			}
			stmts, _, err := f().Decode(inspect.FromClient, []byte(
				"POST /x HTTP/1.1\r\nHost: h\r\nAccept: application/json\r\n"+
					"X-Hoop-Review-Mode: return\r\nContent-Length: 0\r\n\r\n"))
			if err != nil || len(stmts) != 1 {
				t.Fatalf("Decode: %d statements, %v", len(stmts), err)
			}
			if got := stmts[0].HTTP.Headers[analyzer.HeaderReviewMode]; got != "return" {
				t.Errorf("captured %v, want the review mode header", stmts[0].HTTP.Headers)
			}
		})
	}
	if len(stored.Headers) != 1 {
		t.Errorf("the stored http block changed to %v", stored.Headers)
	}
}

// A grpc lane that holds nothing keeps its metadata as the operator wrote it.
// The lane picks this list per RPC from its live rules, so a reload that
// turns holding on or off reaches the next RPC.
func TestAGRPCLaneThatHoldsNothingCapturesNoReviewModeHeader(t *testing.T) {
	if got := grpcMetadataAllowlist(GRPCCodecConfig{Metadata: []string{" X-Tenant "}}, analyzerHolds(laneBlock())); !slices.Equal(got, []string{"x-tenant"}) {
		t.Errorf("grpc allowlist is %v, want only the operator's", got)
	}
}

// On grpc the analyzer holds request messages, which carry no headers of
// their own, so the call's review mode rides on them. Only that header: the
// operator's metadata stays on the request statement, as before.
func TestAHoldingGRPCLaneCarriesTheReviewModeOntoRequestMessages(t *testing.T) {
	allow := grpcMetadataAllowlist(GRPCCodecConfig{Metadata: []string{"X-Tenant"}}, analyzerHolds(holdingBlock()))
	if !slices.Equal(allow, []string{"x-tenant", analyzer.HeaderReviewMode}) {
		t.Fatalf("grpc allowlist is %v", allow)
	}

	req := httptest.NewRequest(http.MethodPost, "/ledger.v1.Ledger/Transfer", nil)
	req.Header.Set("X-Tenant", "acme")
	req.Header.Set("X-Hoop-Review-Mode", "return")
	stmts := newLaneStatements(req, "ledger.v1.Ledger", "Transfer", allow, inspect.GRPC, nil)

	if got := stmts.request(req).HTTP.Headers; got["x-tenant"] != "acme" || got[analyzer.HeaderReviewMode] != "return" {
		t.Errorf("request statement headers are %v", got)
	}
	msg := stmts.message(inspect.FromClient, `{"amount":"100"}`, false, 1)
	if got := msg.HTTP.Headers; len(got) != 1 || got[analyzer.HeaderReviewMode] != "return" {
		t.Errorf("request message headers are %v, want only the review mode", got)
	}
	if got := stmts.message(inspect.FromServer, `{}`, false, 1).HTTP.Headers; got != nil {
		t.Errorf("a response message carries headers %v", got)
	}
}

// The ticket's done-when, through a real lane policy: on a hold lane, a
// request with the header gets the immediate denial and is never claimed.
func TestAHoldLaneReturnsARequestThatAsksForIt(t *testing.T) {
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
		cp:       cp,
	}
	pol, err := buildPolicy("api", GuardrailsConfig{}, holdingBlock(), nil, nil, deps)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}
	v := pol.Evaluate(inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      "DELETE /users/1",
		Operation: inspect.OpDelete,
		HTTP: &inspect.HTTPDetail{
			Method: "DELETE", Path: "/users/1", Resource: "/users/*",
			Headers: map[string]string{analyzer.HeaderReviewMode: "return"},
		},
	})

	if !v.Denied || !strings.Contains(v.Message, "9f97") || !strings.Contains(v.Message, "resend") {
		t.Fatalf("denied=%v message=%q, want an immediate denial naming the review", v.Denied, v.Message)
	}
	if got := v.Annotations[analyzer.MetadataReviewModeSource]; got != "client" {
		t.Errorf("review_mode_source is %q, want client", got)
	}
	if len(*calls) != 1 {
		t.Errorf("the plane saw %d requests, want 1 filing and no claim", len(*calls))
	}
}
