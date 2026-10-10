package gate_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// lineCodec is an HTTP-shaped codec over a line protocol, so these tests
// pin the gate's identity handling without depending on HTTP framing:
// "REQ <credential>\n" is a request, "RESP <status>\n" a response. It lifts
// the credential the way the real HTTP codec does, keeping the value and
// putting only a handle on the statement.
type lineCodec struct {
	mu    sync.Mutex
	next  int
	creds map[string]string
}

const credentialKey = "hoop.credential"

func (*lineCodec) Duplex()                    {}
func (*lineCodec) Protocol() inspect.Protocol { return inspect.HTTP }

func (c *lineCodec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []inspect.Statement
	pos := 0
	for {
		i := bytes.IndexByte(data[pos:], '\n')
		if i < 0 {
			return out, pos, nil
		}
		verb, arg, _ := strings.Cut(string(data[pos:pos+i]), " ")
		pos += i + 1
		switch verb {
		case "REQ":
			c.next++
			h := strconv.Itoa(c.next)
			if c.creds == nil {
				c.creds = map[string]string{}
			}
			c.creds[h] = arg
			out = append(out, inspect.Statement{
				Protocol: inspect.HTTP, Direction: inspect.FromClient, Text: "GET /",
				HTTP:     &inspect.HTTPDetail{Method: "GET", Path: "/", Resource: "/"},
				Metadata: map[string]string{credentialKey: h},
			})
		case "RESP":
			code, _ := strconv.Atoi(arg)
			out = append(out, inspect.Statement{
				Protocol: inspect.HTTP, Direction: inspect.FromServer, Text: arg,
				HTTP: &inspect.HTTPDetail{Method: "GET", Path: "/", Resource: "/", StatusCode: code},
			})
		}
	}
}

func (c *lineCodec) TakeCredential(stmt *inspect.Statement) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := stmt.Metadata[credentialKey]
	if !ok {
		return "", false
	}
	delete(stmt.Metadata, credentialKey)
	v, ok := c.creds[h]
	delete(c.creds, h)
	return v, ok
}

// plainLineCodec decodes the same lines and lifts nothing.
type plainLineCodec struct{ lineCodec }

func (*plainLineCodec) TakeCredential() {}

// tokens resolves "tok-<name>" to <name>@example.com and refuses the rest,
// the shape of a bearer verifier.
var tokens = gate.RequestIdentityFunc(func(_ context.Context, cred string) (session.Identity, error) {
	name, ok := strings.CutPrefix(cred, "tok-")
	if !ok {
		return session.Identity{}, errors.New("token rejected")
	}
	return session.Identity{Subject: name + "@example.com", PeerAddr: "10.0.0.7:51234"}, nil
})

// principalRecorder allows everything and remembers the principal each
// statement was judged under, which is what OPA sees as input.context.
type principalRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (p *principalRecorder) Evaluate(inspect.Statement) policy.Verdict { return policy.Allow() }

func (p *principalRecorder) EvaluateWith(_ inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	p.mu.Lock()
	p.seen = append(p.seen, ec.Context["principal"])
	p.mu.Unlock()
	return policy.Allow()
}

func identityGate(t *testing.T, sink audit.Sink, pol policy.Evaluator, first string) *gate.Gate {
	t.Helper()
	sess := session.New(inspect.HTTP, session.Identity{Subject: first, PeerAddr: "10.0.0.7:51234"})
	sess.Connection = "gke"
	g, err := gate.New(sess, gate.Config{
		Protocol:        inspect.HTTP,
		Policy:          pol,
		Audit:           sink,
		CodecFactory:    func() inspect.Codec { return &lineCodec{} },
		RequestIdentity: tokens,
	})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return g
}

func mustAllow(t *testing.T, d gate.Decision) {
	t.Helper()
	if !d.Allowed {
		t.Fatalf("denied: %s (%s)", d.Message, d.Rule)
	}
}

// Envoy pools one upstream connection across users. The request that names
// a new caller must close the running session and open one for that caller,
// so the trail and the policy context name who sent each request.
func TestRequestIdentityOpensASessionPerCaller(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	pol := &principalRecorder{}
	g := identityGate(t, sink, pol, "alice@example.com")

	mustAllow(t, g.Request(ctx, []byte("REQ tok-alice\n")))
	mustAllow(t, g.Response(ctx, []byte("RESP 200\n")))
	mustAllow(t, g.Request(ctx, []byte("REQ tok-alice\n")))
	mustAllow(t, g.Response(ctx, []byte("RESP 200\n")))
	aliceSession := g.Session().ID
	mustAllow(t, g.Request(ctx, []byte("REQ tok-bob\n")))
	if g.Session().ID == aliceSession {
		t.Fatal("a request from bob kept alice's session")
	}
	mustAllow(t, g.Response(ctx, []byte("RESP 200\n")))
	if err := g.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	type row struct {
		kind      audit.Kind
		principal string
		count     int
	}
	var got []row
	sink.mu.Lock()
	for _, ev := range sink.events {
		got = append(got, row{ev.Kind, ev.Principal, ev.StatementCount})
	}
	sink.mu.Unlock()
	want := []row{
		{audit.KindSessionStart, "alice@example.com", 0},
		{audit.KindStatement, "alice@example.com", 0},
		{audit.KindStatement, "alice@example.com", 0},
		{audit.KindStatement, "alice@example.com", 0},
		{audit.KindStatement, "alice@example.com", 0},
		{audit.KindSessionEnd, "alice@example.com", 4},
		{audit.KindSessionStart, "bob@example.com", 0},
		{audit.KindStatement, "bob@example.com", 0},
		{audit.KindStatement, "bob@example.com", 0},
		{audit.KindSessionEnd, "bob@example.com", 2},
	}
	if len(got) != len(want) {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d = %v, want %v\nall: %v", i, got[i], want[i], got)
		}
	}

	pol.mu.Lock()
	defer pol.mu.Unlock()
	if last := pol.seen[len(pol.seen)-1]; last != "bob@example.com" {
		t.Errorf("policy judged bob's response under %q", last)
	}
	if stmts, _ := g.Stats(); stmts != 6 {
		t.Errorf("connection totals = %d statements, want 6 across both sessions", stmts)
	}
}

// frameCodec is a non-HTTP line protocol: "AUTH <credential>\n" names the
// caller, "DO <text>\n" is a statement with no credential. It lifts the
// credential the way a plug-in codec does, off the frame that carries one.
type frameCodec struct{ lineCodec }

func (*frameCodec) Protocol() inspect.Protocol { return "x-frame" }

func (c *frameCodec) Decode(_ inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []inspect.Statement
	pos := 0
	for {
		i := bytes.IndexByte(data[pos:], '\n')
		if i < 0 {
			return out, pos, nil
		}
		verb, arg, _ := strings.Cut(string(data[pos:pos+i]), " ")
		pos += i + 1
		stmt := inspect.Statement{Protocol: "x-frame", Direction: inspect.FromClient, Text: verb, Operation: inspect.OpOther}
		if verb == "AUTH" {
			c.next++
			h := strconv.Itoa(c.next)
			if c.creds == nil {
				c.creds = map[string]string{}
			}
			c.creds[h] = arg
			stmt.Metadata = map[string]string{credentialKey: h}
		}
		out = append(out, stmt)
	}
}

// A database or plug-in protocol names its caller on the frames that carry
// a credential and on no other. A frame that lifts nothing stays with the
// running session, and a frame that names a new caller rotates the session
// at once: such a codec does not pair responses to requests, so nothing is
// outstanding.
func TestFrameProtocolLiftsCredentialsOnlyWhereTheyAre(t *testing.T) {
	ctx := context.Background()
	pol := &principalRecorder{}
	sess := session.New("x-frame", session.Identity{Subject: "alice@example.com", PeerAddr: "10.0.0.7:51234"})
	g, err := gate.New(sess, gate.Config{
		Protocol:        "x-frame",
		Policy:          pol,
		CodecFactory:    func() inspect.Codec { return &frameCodec{} },
		RequestIdentity: tokens,
	})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	mustAllow(t, g.Request(ctx, []byte("DO one\n")))
	alice := g.Session().ID
	mustAllow(t, g.Request(ctx, []byte("AUTH tok-bob\n")))
	if g.Session().ID == alice {
		t.Fatal("an AUTH frame naming bob kept alice's session")
	}
	d := g.Request(ctx, []byte("DO two\n"))
	mustAllow(t, d)
	if d.Statements[0].Metadata[credentialKey] != "" {
		t.Fatal("the credential handle leaked onto a statement")
	}
	if d := g.Request(ctx, []byte("AUTH nope\n")); d.Allowed {
		t.Fatal("an AUTH frame with a rejected token was allowed")
	}
	pol.mu.Lock()
	defer pol.mu.Unlock()
	want := []string{"alice@example.com", "bob@example.com", "bob@example.com"}
	if strings.Join(pol.seen, ",") != strings.Join(want, ",") {
		t.Errorf("principals = %v, want %v", pol.seen, want)
	}
}

// Rotating while alice's response is still coming would file that response
// under bob. The request is refused instead, and filed under bob, not alice.
func TestRequestIdentityRefusesACallerChangeWithAResponseOutstanding(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	g := identityGate(t, sink, nil, "alice@example.com")

	mustAllow(t, g.Request(ctx, []byte("REQ tok-alice\n")))
	d := g.Request(ctx, []byte("REQ tok-bob\n"))
	if d.Allowed || d.Rule != "identity" || d.Payload != nil {
		t.Fatalf("decision = %+v, want an identity refusal forwarding nothing", d)
	}
	if !strings.Contains(d.Message, "outstanding") {
		t.Errorf("message %q does not say why", d.Message)
	}
	if p := g.Session().Identity.Principal(); p != "alice@example.com" {
		t.Errorf("running session is %q, want alice's untouched", p)
	}
	v := sink.find(audit.KindViolation)
	if v == nil || v.Principal != "bob@example.com" {
		t.Fatalf("violation = %+v, want it filed under bob", v)
	}
	if v.SessionID == g.Session().ID {
		t.Error("bob's refused request was filed in alice's session")
	}

	// alice's session end counts her own request only.
	if err := g.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, ev := range sink.events {
		if ev.Kind == audit.KindSessionEnd && ev.Principal == "alice@example.com" &&
			(ev.StatementCount != 1 || ev.DeniedCount != 0) {
			t.Errorf("alice's session_end counts %d/%d, want 1/0", ev.StatementCount, ev.DeniedCount)
		}
	}
}

// A credential that does not verify must not ride on the previous caller's
// identity. The refusal carries the resolver's reason and an anonymous actor.
func TestUnresolvedCredentialIsRefusedAsAnonymous(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	g := identityGate(t, sink, nil, "alice@example.com")

	mustAllow(t, g.Request(ctx, []byte("REQ tok-alice\n")))
	mustAllow(t, g.Response(ctx, []byte("RESP 200\n")))
	d := g.Request(ctx, []byte("REQ forged\n"))
	if d.Allowed || !strings.Contains(d.Message, "token rejected") {
		t.Fatalf("decision = %+v, want a refusal naming the resolver's reason", d)
	}
	v := sink.find(audit.KindViolation)
	if v == nil || v.Principal != session.AnonymousPrincipal {
		t.Fatalf("violation = %+v, want it filed as anonymous", v)
	}
}

// The handle the codec put on the statement is gone before the statement
// reaches a caller, policy or the trail.
func TestLiftedCredentialHandleNeverLeavesTheGate(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	g := identityGate(t, sink, nil, "alice@example.com")

	d := g.Request(ctx, []byte("REQ tok-alice\n"))
	mustAllow(t, d)
	if _, ok := d.Statements[0].Metadata[credentialKey]; ok {
		t.Error("the decision's statement still carries the credential handle")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, ev := range sink.events {
		if _, ok := ev.Metadata[credentialKey]; ok {
			t.Errorf("%s event carries the credential handle", ev.Kind)
		}
	}
}

// A lane that resolves identity per request with a codec that lifts
// nothing would resolve every request to anonymous. New refuses it.
func TestRequestIdentityNeedsACodecThatLiftsTheCredential(t *testing.T) {
	sess := session.New(inspect.HTTP, session.Identity{})
	_, err := gate.New(sess, gate.Config{
		Protocol:        inspect.HTTP,
		CodecFactory:    func() inspect.Codec { return &plainLineCodec{} },
		RequestIdentity: tokens,
	})
	if err == nil {
		t.Fatal("gate.New accepted RequestIdentity with a codec that lifts no credential")
	}
}

// heldResponses allows everything but holds each response's evaluation
// until released, so a test can put a request on the other pump while a
// response is mid-judgment. It records the principal each response was
// judged under.
type heldResponses struct {
	entered, release chan struct{}
	mu               sync.Mutex
	seen             []string
}

func (p *heldResponses) Evaluate(inspect.Statement) policy.Verdict { return policy.Allow() }

func (p *heldResponses) EvaluateWith(stmt inspect.Statement, ec *policy.EvalContext) policy.Verdict {
	if stmt.Direction == inspect.FromServer {
		p.entered <- struct{}{}
		<-p.release
		p.mu.Lock()
		p.seen = append(p.seen, ec.Context["principal"])
		p.mu.Unlock()
	}
	return policy.Allow()
}

// A response is its request's until it has been judged and audited. A new
// caller arriving while alice's response is still being judged must not
// rotate the session under it: the request is refused, and the response is
// judged and recorded as alice's. Once the response is done, the new caller
// rotates normally.
func TestResponseInJudgmentKeepsItsCaller(t *testing.T) {
	ctx := context.Background()
	sink := &recordingSink{}
	pol := &heldResponses{entered: make(chan struct{}), release: make(chan struct{})}
	g := identityGate(t, sink, pol, "alice@example.com")

	mustAllow(t, g.Request(ctx, []byte("REQ tok-alice\n")))
	resp := make(chan gate.Decision, 1)
	go func() { resp <- g.Response(ctx, []byte("RESP 200\n")) }()
	<-pol.entered

	if d := g.Request(ctx, []byte("REQ tok-bob\n")); d.Allowed {
		t.Fatal("bob rotated the session while alice's response was being judged")
	}
	close(pol.release)
	mustAllow(t, <-resp)

	pol.mu.Lock()
	seen := append([]string(nil), pol.seen...)
	pol.mu.Unlock()
	if len(seen) != 1 || seen[0] != "alice@example.com" {
		t.Fatalf("alice's response was judged under %v", seen)
	}
	sink.mu.Lock()
	for _, ev := range sink.events {
		if ev.Kind == audit.KindStatement && ev.Statement == "200" && ev.Principal != "alice@example.com" {
			t.Errorf("alice's response was recorded under %q", ev.Principal)
		}
	}
	sink.mu.Unlock()

	mustAllow(t, g.Request(ctx, []byte("REQ tok-bob\n")))
	if p := g.Session().Identity.Principal(); p != "bob@example.com" {
		t.Errorf("after the response, the session is %q, want bob's", p)
	}
}
