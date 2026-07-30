package gate_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	_ "github.com/hoophq/hoopinspect/codec/all"
	"github.com/hoophq/hoopinspect/gate"
	"github.com/hoophq/hoopinspect/policy"
	"github.com/hoophq/hoopinspect/session"
)

// pgQuery builds a Postgres simple-query message.
func pgQuery(sql string) []byte {
	var b bytes.Buffer
	b.WriteByte('Q')
	binary.Write(&b, binary.BigEndian, uint32(len(sql)+5))
	b.WriteString(sql)
	b.WriteByte(0)
	return b.Bytes()
}

// recordingSink captures events in order and can be made to fail.
type recordingSink struct {
	mu     sync.Mutex
	events []audit.Event
	failOn audit.Kind // when set, Write returns an error for this kind
	closed bool
}

func (s *recordingSink) Write(_ context.Context, ev audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn != "" && ev.Kind == s.failOn {
		return errors.New("sink unavailable")
	}
	s.events = append(s.events, ev)
	return nil
}

func (s *recordingSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *recordingSink) kinds() []audit.Kind {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]audit.Kind, 0, len(s.events))
	for _, e := range s.events {
		out = append(out, e.Kind)
	}
	return out
}

func (s *recordingSink) find(k audit.Kind) *audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.events {
		if s.events[i].Kind == k {
			return &s.events[i]
		}
	}
	return nil
}

func newSession() *session.Session {
	s := session.New(hoopinspect.Postgres, session.Identity{
		Subject:  "alice@example.com",
		PeerAddr: "10.0.0.7:51234",
	})
	s.Connection = "appdb"
	return s
}

func denyDrops(t *testing.T) *policy.Rules {
	t.Helper()
	r, err := policy.NewRules([]policy.Rule{{
		Name:       "no-destructive",
		Type:       policy.MatchOperation,
		Operations: []hoopinspect.Operation{hoopinspect.OpDrop, hoopinspect.OpDelete},
		Message:    "destructive statements are not permitted on appdb",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	return r
}

func TestAllowedStatementIsForwardedAndAudited(t *testing.T) {
	sink := &recordingSink{}
	g, err := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy:   denyDrops(t),
		Audit:    sink,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	in := pgQuery("SELECT name FROM customers")
	d := g.Request(context.Background(), in)

	if !d.Allowed {
		t.Fatalf("SELECT denied: %+v", d)
	}
	if !bytes.Equal(d.Payload, in) {
		t.Error("payload was altered on an allowed request")
	}
	if len(d.Statements) != 1 {
		t.Fatalf("got %d statements, want 1", len(d.Statements))
	}

	ev := sink.find(audit.KindStatement)
	if ev == nil {
		t.Fatal("no statement event recorded")
	}
	if ev.Principal != "alice@example.com" {
		t.Errorf("Principal = %q; the audit trail must name the human", ev.Principal)
	}
	if ev.Statement != "SELECT name FROM customers" {
		t.Errorf("Statement = %q", ev.Statement)
	}
	if !ev.Allowed {
		t.Error("Allowed = false on an allowed statement")
	}
}

// A denial must both block the bytes and produce a violation record a
// security team can select without scanning every statement.
func TestDeniedStatementBlocksAndRecordsViolation(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy:   denyDrops(t),
		Audit:    sink,
	})

	d := g.Request(context.Background(), pgQuery("DROP TABLE customers"))

	if d.Allowed {
		t.Fatal("DROP was allowed")
	}
	if d.Payload != nil {
		t.Error("payload is non-nil on a denial; the caller might forward it")
	}
	if d.Message != "destructive statements are not permitted on appdb" {
		t.Errorf("Message = %q; the operator's text must reach the user", d.Message)
	}
	if d.Rule != "no-destructive" {
		t.Errorf("Rule = %q", d.Rule)
	}

	ev := sink.find(audit.KindViolation)
	if ev == nil {
		t.Fatal("no violation event recorded")
	}
	if ev.Allowed {
		t.Error("violation event has Allowed = true")
	}
	if ev.Message != d.Message || ev.Rule != d.Rule {
		t.Error("violation event does not carry the denial reason")
	}
}

// A multi-statement query must be judged per statement. Classifying the whole
// payload by its leading verb would let "SELECT 1; DROP TABLE users" through.
func TestMultiStatementDenialStopsAtTheOffender(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy:   denyDrops(t),
		Audit:    sink,
	})

	d := g.Request(context.Background(), pgQuery("SELECT 1; DROP TABLE users; SELECT 2"))

	if d.Allowed {
		t.Fatal("a payload containing DROP was allowed")
	}
	// The leading SELECT is audited (it was evaluated), the DROP is the
	// violation, and evaluation stops there.
	kinds := sink.kinds()
	var statements, violations int
	for _, k := range kinds {
		switch k {
		case audit.KindStatement:
			statements++
		case audit.KindViolation:
			violations++
		}
	}
	if violations != 1 {
		t.Errorf("violations = %d, want 1 (kinds=%v)", violations, kinds)
	}
	if statements != 1 {
		t.Errorf("allowed statements audited = %d, want 1 (the leading SELECT)", statements)
	}
}

// Audit must be written BEFORE the caller forwards, so a crash between the
// two cannot lose the record of the statement that crashed you.
func TestAuditPrecedesTheDecisionBeingReturned(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Audit:    sink,
	})

	// By the time Request returns, the event must already be in the sink.
	_ = g.Request(context.Background(), pgQuery("SELECT 1"))

	if sink.find(audit.KindStatement) == nil {
		t.Error("statement event not present when Request returned")
	}
}

// The default is uncomfortable and deliberate: a broken sink lets statements
// through. FailOnAuditError inverts that for compliance deployments.
func TestFailOnAuditError(t *testing.T) {
	t.Run("default allows", func(t *testing.T) {
		sink := &recordingSink{failOn: audit.KindStatement}
		g, _ := gate.New(newSession(), gate.Config{
			Protocol: hoopinspect.Postgres,
			Audit:    sink,
		})
		d := g.Request(context.Background(), pgQuery("SELECT 1"))
		if !d.Allowed {
			t.Error("a sink failure denied the statement under the default config")
		}
		if d.Err == nil {
			t.Error("the sink failure was not reported in Err")
		}
	})

	t.Run("fail closed denies", func(t *testing.T) {
		sink := &recordingSink{failOn: audit.KindStatement}
		g, _ := gate.New(newSession(), gate.Config{
			Protocol:         hoopinspect.Postgres,
			Audit:            sink,
			FailOnAuditError: true,
		})
		d := g.Request(context.Background(), pgQuery("SELECT 1"))
		if d.Allowed {
			t.Error("FailOnAuditError did not deny on a sink failure")
		}
		if d.Message == "" {
			t.Error("denial carries no message")
		}
	})
}

// Observe-only mode: inspect and audit, never deny.
func TestNilPolicyAllowsEverythingButStillAudits(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Audit:    sink,
	})

	d := g.Request(context.Background(), pgQuery("DROP TABLE customers"))
	if !d.Allowed {
		t.Error("a nil policy denied a statement")
	}
	if sink.find(audit.KindStatement) == nil {
		t.Error("observe-only mode recorded nothing")
	}
}

// A partial message cannot be judged, so the gate holds it.
func TestPartialMessageIsBufferedNotJudged(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy:   denyDrops(t),
		Audit:    sink,
	})

	full := pgQuery("DROP TABLE customers")

	d := g.Request(context.Background(), full[:8])
	if !d.Allowed {
		t.Error("a partial message was denied before it could be parsed")
	}
	if len(d.Statements) != 0 {
		t.Errorf("got %d statements from a fragment", len(d.Statements))
	}
	if len(sink.kinds()) != 0 {
		t.Errorf("a fragment produced audit events: %v", sink.kinds())
	}

	d = g.Request(context.Background(), full[8:])
	if d.Allowed {
		t.Error("the reassembled DROP was allowed")
	}
}

// --- masking -------------------------------------------------------------

type stubMasker struct {
	find    string
	replace string
}

func (m stubMasker) Mask(data []byte) ([]byte, []string, int) {
	n := bytes.Count(data, []byte(m.find))
	if n == 0 {
		return data, nil, 0
	}
	return bytes.ReplaceAll(data, []byte(m.find), []byte(m.replace)), []string{"email"}, n
}

func (m stubMasker) MaskCell(_ string, value []byte) ([]byte, []string, int) {
	return m.Mask(value)
}

func TestResponseMaskingRewritesAndAudits(t *testing.T) {
	sink := &recordingSink{}
	// HTTP here, because byte substitution changes payload length and would
	// corrupt a length-prefixed binary frame. Postgres takes the re-framing
	// path; see TestMaskingReframesLengthPrefixedProtocols.
	sess := session.New(hoopinspect.HTTP, session.Identity{Subject: "alice@example.com"})
	sess.Connection = "api"
	g, _ := gate.New(sess, gate.Config{
		Protocol: hoopinspect.HTTP,
		Audit:    sink,
		Masker:   stubMasker{find: "ada@example.com", replace: "[REDACTED]"},
	})

	body := []byte("HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\nada@example.com")
	d := g.Response(context.Background(), body)

	if !d.Allowed {
		t.Fatalf("response denied: %+v", d)
	}
	if strings.Contains(string(d.Payload), "ada@example.com") {
		t.Error("the sensitive value survived masking")
	}
	if d.MaskedCount != 1 {
		t.Errorf("MaskedCount = %d, want 1", d.MaskedCount)
	}

	ev := sink.find(audit.KindMasked)
	if ev == nil {
		t.Fatal("no masked event recorded")
	}
	if len(ev.MaskedEntities) != 1 || ev.MaskedEntities[0] != "email" {
		t.Errorf("MaskedEntities = %v", ev.MaskedEntities)
	}
	// An audit record of what you masked, in the clear, has un-masked it.
	if strings.Contains(ev.Statement, "ada@example.com") ||
		strings.Contains(ev.Message, "ada@example.com") {
		t.Error("the masked value leaked into the audit record")
	}
}

// Masking a request would change the statement the upstream executes, which
// breaks correctness instead of protecting privacy.
func TestRequestsAreNeverMasked(t *testing.T) {
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Masker:   stubMasker{find: "customers", replace: "XXXXX"},
	})

	in := pgQuery("SELECT * FROM customers")
	d := g.Request(context.Background(), in)

	if !bytes.Equal(d.Payload, in) {
		t.Error("a request payload was masked; the upstream would run a different query")
	}
	if d.MaskedCount != 0 {
		t.Errorf("MaskedCount = %d on a request", d.MaskedCount)
	}
}

// --- lifecycle -----------------------------------------------------------

func TestSessionLifecycleEvents(t *testing.T) {
	sink := &recordingSink{}
	sess := newSession()
	g, _ := gate.New(sess, gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy:   denyDrops(t),
		Audit:    sink,
	})
	ctx := context.Background()

	if err := g.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	g.Request(ctx, pgQuery("SELECT 1"))
	g.Request(ctx, pgQuery("DROP TABLE t"))
	if err := g.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	kinds := sink.kinds()
	if len(kinds) < 2 || kinds[0] != audit.KindSessionStart {
		t.Fatalf("first event = %v, want session_start", kinds)
	}
	if kinds[len(kinds)-1] != audit.KindSessionEnd {
		t.Fatalf("last event = %v, want session_end", kinds)
	}

	end := sink.find(audit.KindSessionEnd)
	if end.StatementCount != 2 {
		t.Errorf("StatementCount = %d, want 2", end.StatementCount)
	}
	if end.DeniedCount != 1 {
		t.Errorf("DeniedCount = %d, want 1", end.DeniedCount)
	}
	if end.Duration <= 0 {
		t.Error("Duration not recorded")
	}
	if sess.IsOpen() {
		t.Error("session still open after Close")
	}
}

// A double Close in a defer chain must not emit two end events or corrupt the
// recorded duration.
func TestCloseIsIdempotent(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Audit:    sink,
	})
	ctx := context.Background()

	g.Close(ctx)
	g.Close(ctx)

	var ends int
	for _, k := range sink.kinds() {
		if k == audit.KindSessionEnd {
			ends++
		}
	}
	if ends != 1 {
		t.Errorf("session_end emitted %d times, want 1", ends)
	}
}

func TestStartIsIdempotent(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Audit:    sink,
	})
	ctx := context.Background()

	g.Start(ctx)
	g.Start(ctx)

	var starts int
	for _, k := range sink.kinds() {
		if k == audit.KindSessionStart {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("session_start emitted %d times, want 1", starts)
	}
}

func TestStats(t *testing.T) {
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Policy:   denyDrops(t),
	})
	ctx := context.Background()

	g.Request(ctx, pgQuery("SELECT 1"))
	g.Request(ctx, pgQuery("SELECT 2"))
	g.Request(ctx, pgQuery("DROP TABLE t"))

	stmts, denied := g.Stats()
	if stmts != 3 || denied != 1 {
		t.Errorf("Stats = (%d, %d), want (3, 1)", stmts, denied)
	}
}

// Client and server directions must not share a reassembly buffer, or a
// duplex stream corrupts both halves.
func TestDirectionsHaveIndependentBuffers(t *testing.T) {
	g, _ := gate.New(newSession(), gate.Config{Protocol: hoopinspect.Postgres})
	ctx := context.Background()

	full := pgQuery("SELECT name FROM customers")

	// Leave the client direction mid-message.
	g.Request(ctx, full[:6])
	// Server traffic must not disturb it.
	g.Response(ctx, []byte("arbitrary server bytes"))

	d := g.Request(ctx, full[6:])
	if len(d.Statements) != 1 {
		t.Fatalf("got %d statements, want 1; the buffers were shared", len(d.Statements))
	}
	if d.Statements[0].Text != "SELECT name FROM customers" {
		t.Errorf("Text = %q", d.Statements[0].Text)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := gate.New(nil, gate.Config{Protocol: hoopinspect.Postgres}); err == nil {
		t.Error("nil session accepted")
	}
	s := session.New("", session.Identity{})
	if _, err := gate.New(s, gate.Config{}); err == nil {
		t.Error("missing protocol accepted")
	}
	if _, err := gate.New(newSession(), gate.Config{Protocol: "oracle"}); err == nil {
		t.Error("unsupported protocol accepted")
	}
}

// The gate must carry session identity into the policy input, or a Rego rule
// cannot reference the actor.
func TestPolicyContextCarriesIdentity(t *testing.T) {
	sess := newSession()
	sess.CorrelationID = "ticket-4711"

	ctx := sess.PolicyContext()
	if ctx["principal"] != "alice@example.com" {
		t.Errorf("principal = %q", ctx["principal"])
	}
	if ctx["connection"] != "appdb" {
		t.Errorf("connection = %q", ctx["connection"])
	}
	if ctx["correlation_id"] != "ticket-4711" {
		t.Errorf("correlation_id = %q", ctx["correlation_id"])
	}
	if ctx["session_id"] == "" {
		t.Error("session_id missing")
	}
}

// The HTTP codec exercises the same gate, including response-side policy, the
// case Envoy's ext_authz cannot express.
func TestHTTPResponsePolicy(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{
		policy.Rule{Name: "no-5xx", Type: policy.MatchHTTPStatus}.
			WithStatuses("5xx").
			WithMessage("upstream failure suppressed by policy"),
	})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	sess := session.New(hoopinspect.HTTP, session.Identity{Subject: "alice"})
	g, _ := gate.New(sess, gate.Config{
		Protocol: hoopinspect.HTTP,
		Policy:   rules,
	})
	ctx := context.Background()

	ok := g.Response(ctx, []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	if !ok.Allowed {
		t.Errorf("200 denied: %+v", ok)
	}

	bad := g.Response(ctx, []byte("HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n"))
	if bad.Allowed {
		t.Error("503 allowed by a 5xx rule")
	}
	if bad.Message != "upstream failure suppressed by policy" {
		t.Errorf("Message = %q", bad.Message)
	}
}

// Byte substitution changes payload LENGTH, and a pgwire DataRow carries that
// length in a frame header the masker cannot see. Substituting in place made
// psql report "lost synchronization with server", a real bug caught against a
// live database.
//
// The gate no longer refuses; the codec re-frames instead. The property to
// check is that the result is still valid pgwire, so this test re-parses the
// output the way a client would.
func TestMaskingReframesLengthPrefixedProtocols(t *testing.T) {
	sink := &recordingSink{}
	g, _ := gate.New(newSession(), gate.Config{
		Protocol: hoopinspect.Postgres,
		Audit:    sink,
		Masker:   stubMasker{find: "ada@example.com", replace: "[REDACTED:email]"},
	})

	// RowDescription naming one column, then a DataRow carrying the value.
	desc := []byte("T\x00\x00\x00\x1b\x00\x01mail\x00" +
		"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x19\x00\x00\x00\x00\x00\x00\x00\x00")
	row := []byte("D\x00\x00\x00\x19\x00\x01\x00\x00\x00\x0fada@example.com")
	done := []byte("C\x00\x00\x00\x0bSELECT 1\x00")

	var out []byte
	for _, chunk := range [][]byte{desc, row, done} {
		d := g.Response(context.Background(), chunk)
		out = append(out, d.Payload...)
	}
	out = append(out, g.FlushResponse()...)

	if bytes.Contains(out, []byte("ada@example.com")) {
		t.Error("the value survived masking")
	}
	if !bytes.Contains(out, []byte("[REDACTED:email]")) {
		t.Errorf("nothing was masked: %q", out)
	}
	if sink.find(audit.KindMasked) == nil {
		t.Error("no masked event recorded")
	}

	// The decisive check: a client parsing this must not desynchronize. A
	// wrong length prefix shows up here exactly as it would in psql.
	insp, err := hoopinspect.New(hoopinspect.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	stmts, err := insp.Inspect(hoopinspect.FromServer, out)
	if err != nil {
		t.Fatalf("re-framed stream is not valid pgwire: %v", err)
	}
	if len(stmts) != 1 || stmts[0].Result == nil || stmts[0].Result.RowCount != 1 {
		t.Errorf("re-framed stream lost its row: %+v", stmts)
	}
}

// HTTP bodies are delimited by Content-Length or chunked framing that the
// relay forwards as a unit, so substitution is safe there.
func TestMaskingAppliesToHTTP(t *testing.T) {
	sess := session.New(hoopinspect.HTTP, session.Identity{Subject: "alice"})
	g, _ := gate.New(sess, gate.Config{
		Protocol: hoopinspect.HTTP,
		Masker:   stubMasker{find: "ada@example.com", replace: "[REDACTED]"},
	})

	body := []byte("HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\nada@example.com")
	d := g.Response(context.Background(), body)

	if d.MaskedCount != 1 {
		t.Fatalf("MaskedCount = %d, want 1", d.MaskedCount)
	}
	if bytes.Contains(d.Payload, []byte("ada@example.com")) {
		t.Error("the sensitive value survived masking on an HTTP response")
	}
}

// A body that arrives without its header block cannot have Content-Length
// corrected, because the header already went out. Masking it anyway grows the
// body past the length the client was told to read, so the client stops
// mid-token and reports a corrupt upstream.
//
// This was a live bug: about 3-10% of responses through the Envoy stack came
// back truncated, whenever the upstream's header and body landed in separate
// TCP reads.
func TestMaskingSkipsBodyWhoseLengthCannotBeCorrected(t *testing.T) {
	g, _ := gate.New(session.New(hoopinspect.HTTP, session.Identity{Subject: "alice"}),
		gate.Config{
			Protocol: hoopinspect.HTTP,
			Masker:   stubMasker{find: "ada@example.com", replace: "[REDACTED:email]"},
		})

	// The header block, forwarded on its own. Content-Length is now committed.
	head := []byte("HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\n")
	if d := g.Response(context.Background(), head); !bytes.Equal(d.Payload, head) {
		t.Fatalf("header block was rewritten: %q", d.Payload)
	}

	// The body, in the next read. Masking would take it from 15 bytes to 16.
	body := []byte("ada@example.com")
	d := g.Response(context.Background(), body)

	if len(d.Payload) != 15 {
		t.Errorf("payload is %d bytes but the client will read 15, truncating it: %q",
			len(d.Payload), d.Payload)
	}
	if d.MaskedCount != 0 {
		t.Errorf("MaskedCount = %d: reported masking a body it could not safely rewrite",
			d.MaskedCount)
	}
}

// The same buffer, whole, MUST still be masked and retagged: the skip above
// is about what the gate cannot correct, never a blanket retreat.
func TestMaskingStillAppliesWhenHeaderAndBodyArriveTogether(t *testing.T) {
	g, _ := gate.New(session.New(hoopinspect.HTTP, session.Identity{Subject: "alice"}),
		gate.Config{
			Protocol: hoopinspect.HTTP,
			Masker:   stubMasker{find: "ada@example.com", replace: "[REDACTED:email]"},
		})

	whole := []byte("HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\nada@example.com")
	d := g.Response(context.Background(), whole)

	if d.MaskedCount != 1 {
		t.Fatalf("MaskedCount = %d, want 1", d.MaskedCount)
	}
	// Assert the invariant, not the header's spelling: what a client acts on
	// is the number, and the retag preserves the upstream's own spacing.
	head, body, _ := bytes.Cut(d.Payload, []byte("\r\n\r\n"))
	_, value, _ := bytes.Cut(head, []byte("Content-Length:"))
	declared, err := strconv.Atoi(string(bytes.TrimSpace(value)))
	if err != nil {
		t.Fatalf("unparseable Content-Length in %q", head)
	}
	if declared != len(body) {
		t.Errorf("declared %d but body is %d bytes: %q", declared, len(body), d.Payload)
	}
}
