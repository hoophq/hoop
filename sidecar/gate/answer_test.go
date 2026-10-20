package gate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

const reservedRequest = "GET /.well-known/hoop/reviews/x HTTP/1.1\r\nHost: h\r\n\r\n"

// answeringGate owns /.well-known/hoop/reviews/x and counts the replies it
// rendered, which is where the lane would read the plane.
func answeringGate(t *testing.T) (*gate.Gate, *recordingSink, *int) {
	t.Helper()
	sink := &recordingSink{}
	rendered := 0
	g, err := gate.New(newSession(), gate.Config{
		Protocol: inspect.HTTP,
		Audit:    sink,
		Answer: func(stmt inspect.Statement) func(context.Context) []byte {
			if stmt.HTTP.Path != "/.well-known/hoop/reviews/x" {
				return nil
			}
			return func(context.Context) []byte { rendered++; return []byte("reply") }
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g, sink, &rendered
}

// The connection's first request is answered by the lane and never forwarded.
func TestAReservedRouteIsAnsweredAsTheFirstRequest(t *testing.T) {
	g, _, _ := answeringGate(t)
	d := g.Request(context.Background(), []byte(reservedRequest))
	if string(d.Reply) != "reply" || d.Hangup || d.Allowed || len(d.Payload) != 0 {
		t.Errorf("decision = allowed %v hangup %v reply %q payload %d bytes, want the reply only",
			d.Allowed, d.Hangup, d.Reply, len(d.Payload))
	}
}

// HTTP/1.1 pairs responses with requests by order, so a reserved route behind
// another request is never answered: the reply could overtake the earlier
// response. The connection ends unanswered, nothing in the chunk is judged,
// and the plane is never read.
func TestAReservedRouteBehindAnotherRequestHangsUp(t *testing.T) {
	t.Run("same chunk", func(t *testing.T) {
		g, sink, rendered := answeringGate(t)
		d := g.Request(context.Background(), []byte("GET /api/x HTTP/1.1\r\nHost: h\r\n\r\n"+reservedRequest))
		if !d.Hangup || d.Reply != nil || d.Allowed || len(d.Payload) != 0 {
			t.Errorf("decision = allowed %v hangup %v reply %q, want a hangup", d.Allowed, d.Hangup, d.Reply)
		}
		if ev := sink.find(audit.KindStatement); ev != nil {
			t.Errorf("the chunk recorded %q as a statement, but nothing in it ran", ev.Statement)
		}
		if *rendered != 0 {
			t.Error("the plane was read for a reply that was never sent")
		}
	})
	t.Run("later chunk", func(t *testing.T) {
		g, _, rendered := answeringGate(t)
		if d := g.Request(context.Background(), []byte("GET /api/x HTTP/1.1\r\nHost: h\r\n\r\n")); !d.Allowed {
			t.Fatalf("the first request was not forwarded: %+v", d)
		}
		d := g.Request(context.Background(), []byte(reservedRequest))
		if !d.Hangup || d.Reply != nil || *rendered != 0 {
			t.Errorf("decision = hangup %v reply %q rendered %d, want a hangup and no plane read",
				d.Hangup, d.Reply, *rendered)
		}
	})
}

// tokenGate answers the same route on a lane whose resolver admits only the
// credential "good", lifted from X-Hoop-Token. authenticates makes it a lane
// that is the authentication.
func tokenGate(t *testing.T, authenticates bool) (*gate.Gate, *recordingSink, *int) {
	t.Helper()
	sink := &recordingSink{}
	rendered := 0
	g, err := gate.New(newSession(), gate.Config{
		Protocol: inspect.HTTP,
		Audit:    sink,
		CodecFactory: func() inspect.Codec {
			return codechttp.New(codechttp.Options{CredentialHeader: "x-hoop-token", AuthenticatesCaller: authenticates})
		},
		RequestIdentity: gate.RequestIdentityFunc(func(_ context.Context, cred string) (session.Identity, error) {
			if cred != "good" {
				return session.Identity{}, errors.New("not accepted")
			}
			return session.Identity{Subject: "agents"}, nil
		}),
		Answer: func(stmt inspect.Statement) func(context.Context) []byte {
			if stmt.HTTP.Path != "/.well-known/hoop/reviews/x" {
				return nil
			}
			return func(context.Context) []byte { rendered++; return []byte("reply") }
		},
		AuthenticatesCaller: authenticates,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g, sink, &rendered
}

func reservedWith(token string) []byte {
	head := "GET /.well-known/hoop/reviews/x HTTP/1.1\r\nHost: h\r\n"
	if token != "" {
		head += "X-Hoop-Token: " + token + "\r\n"
	}
	return []byte(head + "\r\n")
}

// On a lane that is the authentication, the reserved route is as public as
// the listener, so it answers only a caller the resolver admits. A refused
// caller gets the identity refusal any other path would, audited, and the
// plane is never read.
func TestAReservedRouteWaitsForTheCallerWhenAsked(t *testing.T) {
	for name, token := range map[string]string{"no header": "", "wrong token": "bad"} {
		t.Run(name, func(t *testing.T) {
			g, sink, rendered := tokenGate(t, true)
			d := g.Request(context.Background(), reservedWith(token))
			if d.Allowed || d.Rule != "identity" || d.Reply != nil || d.Hangup || len(d.Payload) != 0 {
				t.Fatalf("decision = allowed %v rule %q reply %q hangup %v, want the identity refusal",
					d.Allowed, d.Rule, d.Reply, d.Hangup)
			}
			if *rendered != 0 {
				t.Error("the plane was read for a caller the lane refused")
			}
			if ev := sink.find(audit.KindViolation); ev == nil || ev.Rule != "identity" {
				t.Errorf("the refusal was not audited: %+v", ev)
			}
		})
	}

	g, _, rendered := tokenGate(t, true)
	d := g.Request(context.Background(), reservedWith("good"))
	if string(d.Reply) != "reply" || *rendered != 1 {
		t.Fatalf("decision = allowed %v rule %q reply %q, want the reply", d.Allowed, d.Rule, d.Reply)
	}
	// A reply is not a statement the session ran, so the caller check does
	// not rotate the session.
	if p := g.Session().Identity.Principal(); p != "alice@example.com" {
		t.Errorf("the reply rotated the session to %q", p)
	}
}

// Without the flag the route answers before identity, as it always has: a
// lane behind an authenticating proxy keeps its behaviour.
func TestAReservedRouteAnswersFirstByDefault(t *testing.T) {
	g, _, rendered := tokenGate(t, false)
	if d := g.Request(context.Background(), reservedWith("")); string(d.Reply) != "reply" || *rendered != 1 {
		t.Errorf("decision = allowed %v rule %q reply %q, want the reply", d.Allowed, d.Rule, d.Reply)
	}
}
