package review

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// gatewayStub stands in for the hoop gateway. It records what it was asked, so
// the tests assert against REAL request bodies rather than a hand-rolled fake
// — which also covers the encoding, and is the reason this package has no
// broker interface.
type gatewayStub struct {
	*httptest.Server

	path   string
	body   map[string]any
	auth   string
	status int
	reply  any
}

func newGatewayStub(t *testing.T) *gatewayStub {
	t.Helper()
	g := &gatewayStub{status: http.StatusOK}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.path = r.URL.Path
		g.auth = r.Header.Get("Authorization")
		g.body = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&g.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(g.status)
		if g.reply != nil {
			_ = json.NewEncoder(w).Encode(g.reply)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *gatewayStub) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(g.URL, "hpk_sandbox", time.Second)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestClaimSendsTheHashAndTheCredential(t *testing.T) {
	g := newGatewayStub(t)
	g.reply = Ticket{ReviewID: "rev-1", SessionID: "sess-1", Status: "EXECUTED", URL: "https://gw/sessions/sess-1"}

	ticket, err := g.client(t).Claim(context.Background(), "appdb", "abc123")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if g.path != claimPath {
		t.Errorf("path = %q, want %q", g.path, claimPath)
	}
	if g.auth != "Bearer hpk_sandbox" {
		t.Errorf("authorization = %q", g.auth)
	}
	if g.body["connection"] != "appdb" || g.body["statement_hash"] != "abc123" {
		t.Errorf("body = %v", g.body)
	}
	// The marker must NEVER reach the authorization path. If it did, the
	// agent would be choosing its own permissions.
	if _, leaked := g.body["marker"]; leaked {
		t.Error("the claim carried a marker; authorization must not read an agent-supplied key")
	}
	if ticket.ReviewID != "rev-1" || ticket.Status != "EXECUTED" {
		t.Errorf("ticket = %+v", ticket)
	}
}

// 404 is the ordinary answer on a first attempt, not a failure, and callers
// branch on it.
func TestClaimReportsNoApprovalOn404(t *testing.T) {
	g := newGatewayStub(t)
	g.status = http.StatusNotFound
	g.reply = map[string]string{"message": "no approved review"}

	_, err := g.client(t).Claim(context.Background(), "appdb", "abc123")
	if !errors.Is(err, ErrNoApproval) {
		t.Fatalf("err = %v, want ErrNoApproval", err)
	}
}

func TestClaimSurfacesTheGatewayMessageOnError(t *testing.T) {
	g := newGatewayStub(t)
	g.status = http.StatusForbidden
	g.reply = map[string]string{"message": "connection not found or not authorized"}

	_, err := g.client(t).Claim(context.Background(), "appdb", "abc123")
	if err == nil || errors.Is(err, ErrNoApproval) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("the gateway's own reason is missing: %v", err)
	}
}

// A 2xx with no review id is a gateway that answered something else. Treating
// it as a ticket would report a review that does not exist.
func TestEmptyTicketIsAnError(t *testing.T) {
	g := newGatewayStub(t)
	g.reply = map[string]string{}

	if _, err := g.client(t).Claim(context.Background(), "appdb", "abc123"); err == nil {
		t.Fatal("a response with no review id was accepted")
	}
}

func TestRequestSendsTheStatementAndTheMarker(t *testing.T) {
	g := newGatewayStub(t)
	g.status = http.StatusCreated
	g.reply = Ticket{ReviewID: "rev-2", SessionID: "sess-2", Status: "PENDING", URL: "https://gw/sessions/sess-2"}

	ticket, err := g.client(t).Request(context.Background(), Request{
		Connection:    "appdb",
		StatementHash: "abc123",
		Statement:     "DELETE FROM users WHERE id = 7",
		Marker:        "task-42",
		RiskLevel:     "high",
		Rule:          "risky-writes",
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	if g.path != createPath {
		t.Errorf("path = %q, want %q", g.path, createPath)
	}
	for k, want := range map[string]string{
		"statement":  "DELETE FROM users WHERE id = 7",
		"marker":     "task-42",
		"risk_level": "high",
		"rule":       "risky-writes",
	} {
		if g.body[k] != want {
			t.Errorf("body[%q] = %v, want %q", k, g.body[k], want)
		}
	}
	if ticket.Status != "PENDING" {
		t.Errorf("status = %q", ticket.Status)
	}
}

func TestValidateAPIURL(t *testing.T) {
	bad := map[string]string{
		"":                            "empty",
		"gateway.hoop.internal":       "no scheme",
		"ftp://gateway":               "wrong scheme",
		"https://":                    "no host",
		"https://user:pw@gateway":     "credential in userinfo",
		"https://gateway?token=hpk_1": "credential in a query string",
	}
	for raw, why := range bad {
		if err := ValidateAPIURL(raw); err == nil {
			t.Errorf("%q was accepted (%s)", raw, why)
		}
	}
	if err := ValidateAPIURL("https://gateway.hoop.internal"); err != nil {
		t.Errorf("a good url was refused: %v", err)
	}
}

func TestNewClientRefusesAnEmptyToken(t *testing.T) {
	if _, err := NewClient("https://gateway", "  ", time.Second); err == nil {
		t.Fatal("an empty token was accepted")
	}
}

// A 404 means "no approval" on the CLAIM endpoint and nothing of the sort on
// the create endpoint, where it is the gateway saying it cannot resolve the
// connection under this caller's access. Collapsing the two sends an operator
// looking for a review queue when the real problem is the connection name in
// their config.
func TestCreate404IsNotNoApproval(t *testing.T) {
	g := newGatewayStub(t)
	g.status = http.StatusNotFound
	g.reply = map[string]string{"message": "connection not found"}

	_, err := g.client(t).Request(context.Background(), Request{
		Connection: "pghoop", StatementHash: "abc", Statement: "DELETE FROM t",
	})
	if err == nil {
		t.Fatal("a 404 on the create path was accepted")
	}
	if errors.Is(err, ErrNoApproval) {
		t.Error("a create 404 was reported as ErrNoApproval")
	}
	if !strings.Contains(err.Error(), "connection not found") {
		t.Errorf("the gateway's own reason is missing: %v", err)
	}
}

// And the claim endpoint still reports the sentinel the gate branches on.
func TestClaim404StillMeansNoApproval(t *testing.T) {
	g := newGatewayStub(t)
	g.status = http.StatusNotFound
	g.reply = map[string]string{"message": "no approved review for this statement"}

	if _, err := g.client(t).Claim(context.Background(), "pghoop", "abc"); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("err = %v, want ErrNoApproval", err)
	}
}
