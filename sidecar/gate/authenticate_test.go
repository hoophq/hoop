package gate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// onlyGood admits the credential "good" as agents, and names a request
// without one the way the token resolver does.
var onlyGood = gate.RequestIdentityFunc(func(_ context.Context, cred string) (session.Identity, error) {
	switch cred {
	case "":
		return session.Identity{}, errors.New("the request carried no credential")
	case "good":
		return session.Identity{Subject: "agents"}, nil
	}
	return session.Identity{}, errors.New("not accepted")
})

func authenticatingGate(t *testing.T, sink audit.Sink) *gate.Gate {
	t.Helper()
	g, err := gate.New(newSession(), gate.Config{
		Protocol: inspect.HTTP,
		Audit:    sink,
		CodecFactory: func() inspect.Codec {
			return codechttp.New(codechttp.Options{CredentialHeader: "x-hoop-token", AuthenticatesCaller: true})
		},
		RequestIdentity:     onlyGood,
		AuthenticatesCaller: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

// The gate judges a request once its body has arrived. On a lane that is
// the authentication the codec refuses the head before then, and the gate
// records that refusal
// as the identity refusal it would have made: the same 403 text, rule
// identity, filed under anonymous with the request line as the statement.
func TestACallerRefusedBeforeItsBodyIsAnIdentityRefusal(t *testing.T) {
	for name, tc := range map[string]struct{ head, want string }{
		"no header": {"DELETE /items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\n\r\n",
			"request refused: the request carried no credential"},
		"wrong token": {"DELETE /items/1 HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: guess\r\nContent-Length: 1\r\n\r\n",
			"request refused: not accepted"},
	} {
		t.Run(name, func(t *testing.T) {
			sink := &recordingSink{}
			g := authenticatingGate(t, sink)
			d := g.Request(context.Background(), []byte(tc.head))
			if d.Allowed || d.Rule != "identity" || len(d.Payload) != 0 || d.Message != tc.want {
				t.Fatalf("decision = allowed %v rule %q payload %q message %q; want the identity refusal %q",
					d.Allowed, d.Rule, d.Payload, d.Message, tc.want)
			}
			ev := sink.find(audit.KindViolation)
			if ev == nil || ev.Rule != "identity" || ev.Principal != session.AnonymousPrincipal || ev.Statement != "DELETE /items/1" {
				t.Fatalf("violation = %+v, want an identity refusal of DELETE /items/1 under anonymous", ev)
			}
			if p := g.Session().Identity.Principal(); p != "alice@example.com" {
				t.Errorf("the refusal moved the running session to %q", p)
			}
		})
	}
}

// A lane that is the authentication refuses client bytes its codec could
// not decode instead of forwarding them as the honest default: nothing
// judged them. The case that reaches it: a head the filter framed within
// 1 MiB, which the Via line it adds pushes past the codec's own 1 MiB.
func TestAnAuthenticatingGateRefusesWhatItCannotDecode(t *testing.T) {
	head := "GET / HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: good\r\nX-Pad: "
	tail := "\r\n\r\n"
	in := head + strings.Repeat("a", 1<<20-len(head)-len(tail)) + tail
	// Allowed false is the whole contract: the relay forwards nothing of a
	// refused chunk, Payload or not.
	d := authenticatingGate(t, &recordingSink{}).Request(context.Background(), []byte(in))
	if d.Allowed || d.Rule != "stream-unsafe" {
		t.Fatalf("decision = allowed %v rule %q; want the undecodable request refused", d.Allowed, d.Rule)
	}
}

// A lane that is the authentication needs a caller check, and a codec that
// runs it before forwarding a request; New refuses anything less, because
// the gate alone would judge a request only after its head went out.
func TestAuthenticatesCallerNeedsAResolverAndACodecThatChecks(t *testing.T) {
	for name, cfg := range map[string]gate.Config{
		"no resolver": {
			CodecFactory: func() inspect.Codec {
				return codechttp.New(codechttp.Options{CredentialHeader: "x-hoop-token", AuthenticatesCaller: true})
			},
		},
		"a codec built not to check": {
			CodecFactory: func() inspect.Codec {
				return codechttp.New(codechttp.Options{CredentialHeader: "x-hoop-token"})
			},
			RequestIdentity: onlyGood,
		},
		"a codec that cannot check": {
			CodecFactory:    func() inspect.Codec { return &lineCodec{} },
			RequestIdentity: onlyGood,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg.Protocol = inspect.HTTP
			cfg.AuthenticatesCaller = true
			if _, err := gate.New(newSession(), cfg); err == nil {
				t.Error("New accepted it")
			}
		})
	}
}
