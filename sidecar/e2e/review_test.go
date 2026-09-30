//go:build integration

package e2e_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// planeToken is the sidecar token the fake plane expects.
const planeToken = "hsc_e2e"

// reviewConfig is a plane-served MySQL lane that holds every UPDATE for
// approval. JSON, because the plane serves JSON; it is valid YAML too, so
// startSidecar substitutes it like any other config.
const reviewConfig = `{
  "log_level": "info",
  "audit": {"file": "-"},
  "mcp": {"listen": "{{mcp}}"},
  "analyzer": {
    "provider": "openai",
    "model": "e2e",
    "endpoint": "{{model}}",
    "credentials_file": "{{credentials}}",
    "fail_open": false
  },
  "listeners": [{
    "name": "appdb",
    "protocol": "mysql",
    "listen": "{{listen}}",
    "upstream": "{{upstream}}",
    "analyzer": {
      "trigger": {"operations": ["update"]},
      "high": "require_review",
      "approval_rule": "appdb-approvers",
      "message": "held for approval on appdb"
    }
  }]
}`

// fakePlane answers the sidecar's control plane routes: the handshake and
// the three review routes. Reviews follow gateway/api/sidecar/reviews.go: a
// filing matches a live review for the same listener, rule and exact bytes,
// an approved review is spent once, and a spent one is no longer live.
type fakePlane struct {
	t    *testing.T
	srv  *httptest.Server
	mu   sync.Mutex
	doc  []byte
	revs map[string]*fakeReview
}

type fakeReview struct {
	id, listener, rule, statement, status string
	created                               time.Time
	decided                               *time.Time
}

func newFakePlane(t *testing.T) *fakePlane {
	p := &fakePlane{t: t, revs: map[string]*fakeReview{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sidecars/handshake", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		_, _ = w.Write(p.doc)
	})
	mux.HandleFunc("POST /api/sidecars/reviews", p.file)
	mux.HandleFunc("POST /api/sidecars/reviews/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if rev := p.revs[r.PathValue("id")]; rev != nil {
			p.answer(w, http.StatusOK, rev)
			return
		}
		notFound(w)
	})
	mux.HandleFunc("GET /api/sidecars/reviews/{id}", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		rev := p.revs[r.PathValue("id")]
		if rev == nil {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": rev.id, "status": rev.status, "listener_name": rev.listener,
			"approval_rule": rev.rule, "created_at": rev.created, "decided_at": rev.decided,
		})
	})
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("hoop-sidecar-token") != planeToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "access denied"})
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakePlane) url() string { return p.srv.URL }

// servedBy makes plane serve the config body over the handshake. The local
// file then names only the plane, as in a plane-connected deployment.
func servedBy(plane *fakePlane) sidecarOption {
	return func(l *sidecarLaunch) {
		plane.serve(l.body)
		l.body = "control_plane_url: " + plane.url() + "\n"
		l.env = append(l.env, "HOOP_SIDECAR_TOKEN="+planeToken)
	}
}

func (p *fakePlane) serve(doc string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.doc = []byte(doc)
}

func (p *fakePlane) file(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Listener string `json:"listener_name"`
		Rule     string `json:"approval_rule"`
		Payload  string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	stmt, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, rev := range p.revs {
		live := rev.status == "PENDING" || rev.status == "APPROVED"
		if live && rev.listener == req.Listener && rev.rule == req.Rule && rev.statement == string(stmt) {
			p.answer(w, http.StatusOK, rev)
			return
		}
	}
	rev := &fakeReview{id: uuid.NewString(), listener: req.Listener, rule: req.Rule,
		statement: string(stmt), status: "PENDING", created: time.Now()}
	p.revs[rev.id] = rev
	p.answer(w, http.StatusCreated, rev)
}

// answer spends an approved review: only this answer forwards.
func (p *fakePlane) answer(w http.ResponseWriter, code int, rev *fakeReview) {
	forward := rev.status == "APPROVED"
	if forward {
		rev.status = "EXECUTED"
	}
	writeJSON(w, code, map[string]any{
		"forward": forward,
		"review":  map[string]string{"id": rev.id, "status": rev.status},
	})
}

// approve is the test's reviewer.
func (p *fakePlane) approve(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rev := p.revs[id]
	if rev == nil || rev.status != "PENDING" {
		p.t.Fatalf("cannot approve review %s: %+v", id, rev)
	}
	now := time.Now()
	rev.status, rev.decided = "APPROVED", &now
}

// waitForReview returns the id of the review filed for statement.
func (p *fakePlane) waitForReview(t *testing.T, statement string) string {
	t.Helper()
	deadline := time.Now().Add(stmtTimeout)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		for _, rev := range p.revs {
			if rev.statement == statement {
				p.mu.Unlock()
				return rev.id
			}
		}
		p.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no review was filed for %q", statement)
	return ""
}

// notFound is the status route's own 404, which the sidecar tells apart from
// a plane that has no such route.
func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "review not found"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// startHighRiskModel is an OpenAI-compatible endpoint that rates every
// statement high risk. The lane trigger decides which statements reach it.
func startHighRiskModel(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"tool_calls": []any{map[string]any{
				"function": map[string]string{
					"name":      "report_high_risk",
					"arguments": `{"title":"writes a row","explanation":"e2e"}`,
				},
			}}},
		}}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// mcpAgent connects to the sidecar's MCP server the way an agent does.
func mcpAgent(t *testing.T, addr string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "e2e-agent"}, nil)
	cs, err := client.Connect(context.Background(), &sdk.StreamableClientTransport{
		Endpoint: "http://" + addr + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// reviewTool is the part of a tool result the agent acts on.
type reviewTool struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Next     string `json:"next"`
	TimedOut *bool  `json:"timed_out"`
}

func callReviewTool(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) reviewTool {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s returned an error: %+v", tool, res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out reviewTool
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s result: %v", tool, err)
	}
	return out
}

var reviewIDInDeny = regexp.MustCompile(`^.*review ([0-9a-f-]{36}): `)

// deniedReviewID asserts a return-mode deny and returns its review id.
func deniedReviewID(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("the statement ran; want a return-mode deny")
	}
	m := reviewIDInDeny.FindStringSubmatch(err.Error())
	if m == nil || !strings.Contains(err.Error(), "call the MCP tool review_wait") {
		t.Fatalf("deny %q does not carry a review id and the review_wait clause", err)
	}
	return m[1]
}

// An agent's whole loop over the shipped binary: its UPDATE is denied at
// once with a review id, review_wait sees the approval, and the identical
// resend runs exactly once. A second resend is a new review, and the first
// reads as used. A human on the same hold listener keeps waiting throughout.
func TestAgentFlowFromHeldStatementToApprovedRetry(t *testing.T) {
	up := startMySQL(t)
	plane := newFakePlane(t)

	cred := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	config := strings.NewReplacer("{{model}}", startHighRiskModel(t), "{{credentials}}", cred).
		Replace(reviewConfig)
	s := startSidecar(t, up, config, servedBy(plane))

	// The human: no attribute, so the listener's hold applies. The read
	// deadline outlasts the agent's flow plus one hold poll.
	const humanStmt = "UPDATE customers SET name = 'Human' WHERE id = 2"
	human := openDSN(t, dsn(s.addr, "readTimeout=90s"))
	held := make(chan error, 1)
	go func() {
		_, err := human.Exec(humanStmt)
		held <- err
	}()
	humanReview := plane.waitForReview(t, humanStmt)

	// The agent. No idle connections: a deny closes the socket, and a pooled
	// dead one would fail the resend before it reached the relay.
	const agentStmt = "UPDATE customers SET name = CONCAT(name, '+') WHERE id = 1"
	agent := s.dial(t, "connectionAttributes=hoop_review_mode:return")
	agent.SetMaxIdleConns(0)
	cs := mcpAgent(t, s.mcpAddr)

	_, err := agent.Exec(agentStmt)
	first := deniedReviewID(t, err)
	if first == humanReview {
		t.Fatal("the agent's statement matched the human's review")
	}

	pending := callReviewTool(t, cs, "review_wait", map[string]any{"id": first, "timeout_seconds": 2})
	if pending.Status != "PENDING" || pending.TimedOut == nil || !*pending.TimedOut || pending.Next != "wait" {
		t.Fatalf("review_wait before approval = %+v, want PENDING, timed out, next=wait", pending)
	}

	plane.approve(first)
	approved := callReviewTool(t, cs, "review_wait", map[string]any{"id": first})
	if approved.Status != "APPROVED" || approved.Next != "resend_identical_statement" {
		t.Fatalf("review_wait after approval = %+v, want APPROVED, next=resend_identical_statement", approved)
	}

	if _, err := agent.Exec(agentStmt); err != nil {
		t.Fatalf("the approved resend was refused: %v", err)
	}
	if got := customerName(t, agent, 1); got != "Ada Lovelace+" {
		t.Fatalf("name after the resend = %q, want it updated once", got)
	}

	_, err = agent.Exec(agentStmt)
	second := deniedReviewID(t, err)
	if second == first {
		t.Fatalf("the second resend reused review %s; an approval releases once", first)
	}
	if got := customerName(t, agent, 1); got != "Ada Lovelace+" {
		t.Fatalf("name after the second resend = %q; it ran twice", got)
	}
	used := callReviewTool(t, cs, "review_status", map[string]any{"id": first})
	if used.Status != "EXECUTED" || used.Next != "stop" {
		t.Fatalf("review_status of the spent review = %+v, want EXECUTED, next=stop", used)
	}

	select {
	case err := <-held:
		t.Fatalf("the human's statement ended while its review was pending: %v", err)
	default:
	}
	if got := customerName(t, agent, 2); got != "Bob Stone" {
		t.Fatalf("name = %q before approval; the held statement ran", got)
	}
	plane.approve(humanReview)
	select {
	case err := <-held:
		if err != nil {
			t.Fatalf("the human's approved statement failed: %v", err)
		}
	case <-time.After(stmtTimeout):
		t.Fatal("the human's statement did not run after approval")
	}
	if got := customerName(t, agent, 2); got != "Human" {
		t.Fatalf("name = %q, want the human's update", got)
	}
}

// customerName reads a name directly; SELECT is outside the review trigger.
func customerName(t *testing.T, db *sql.DB, id int) string {
	t.Helper()
	var name string
	if err := db.QueryRow("SELECT name FROM customers WHERE id = ?", id).Scan(&name); err != nil {
		t.Fatalf("read customer %d: %v", id, err)
	}
	return name
}
