package clickhouse_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ClickHouse/ch-go/proto"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/codec/clickhouse"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	codecclickhouse "github.com/hoophq/libhoop/v2/codec/clickhouse"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

func TestRegistryBuildsFullClickHouseCodec(t *testing.T) {
	insp, err := inspect.New(inspect.ClickHouse)
	if err != nil {
		t.Fatalf("inspect.New: %v", err)
	}
	codec := insp.Codec()
	if codec.Protocol() != inspect.ClickHouse {
		t.Fatalf("protocol = %q", codec.Protocol())
	}
	if _, ok := codec.(gate.Duplex); !ok {
		t.Fatal("ClickHouse codec did not declare shared bidirectional state")
	}
	if _, ok := codec.(gate.StreamFilter); !ok {
		t.Fatal("ClickHouse codec did not expose its hello revision clamp")
	}
	if _, ok := codec.(gate.Reframer); !ok {
		t.Fatal("ClickHouse codec did not expose response masking")
	}
	if !gate.MaskSupported(inspect.ClickHouse) {
		t.Fatal("ClickHouse registration is not reported as mask-capable")
	}
}

// loginTerminator is gate.LoginTerminator, spelled out so the wrapper is
// pinned to the shape the gate reads.
type loginTerminator interface {
	LoginIdentity() (session.Identity, map[string]string, bool)
}

var _ gate.LoginTerminator = loginTerminator(nil)

func hello(user, password string) []byte {
	var b proto.Buffer
	proto.ClientHello{
		Name: "t", Major: 1, Minor: 0, ProtocolVersion: codecclickhouse.PinRevision,
		Database: "db", User: user, Password: password,
	}.Encode(&b)
	return b.Buf
}

func authenticator(t *testing.T) codectypes.Authenticator {
	return func(_ context.Context, user, secret string) (codectypes.ClientIdentity, codectypes.UpstreamCredential, error) {
		if user == "alice" && secret == "pw" {
			return codectypes.ClientIdentity{Subject: "alice@example.com", Groups: []string{"g"}},
				codectypes.UpstreamCredential{User: "svc", Password: "s"}, nil
		}
		return codectypes.ClientIdentity{}, codectypes.UpstreamCredential{}, fmt.Errorf("%w: unknown user", codectypes.ErrLoginRefused)
	}
}

func TestTerminatingLaneSwapsAndReportsIdentity(t *testing.T) {
	c := clickhouse.NewWithOptions(clickhouse.Options{Authenticate: authenticator(t)})
	for _, cap := range []struct {
		name string
		ok   bool
	}{
		{"Duplex", is[gate.Duplex](c)},
		{"StreamFilter", is[gate.StreamFilter](c)},
		{"Reframer", is[gate.Reframer](c)},
		{"LoginIdentity", is[loginTerminator](c)},
	} {
		if !cap.ok {
			t.Fatalf("terminating codec lost %s", cap.name)
		}
	}
	term := c.(loginTerminator)
	if _, _, ok := term.LoginIdentity(); ok {
		t.Fatal("identity reported before the Hello")
	}
	out, err := c.(gate.StreamFilter).Filter(inspect.FromClient, hello("alice", "pw"))
	if err != nil || !bytes.Equal(out, hello("svc", "s")) {
		t.Fatalf("Filter = %x, %v; want the swapped hello", out, err)
	}
	id, md, ok := term.LoginIdentity()
	if !ok || id.Subject != "alice@example.com" || len(id.Groups) != 1 {
		t.Fatalf("identity = %+v, %v", id, ok)
	}
	if md["upstream_user"] != "svc" || md["verified"] != "true" {
		t.Fatalf("metadata = %v", md)
	}
}

func TestTerminatingLanePassthroughUserIsUnverified(t *testing.T) {
	c := clickhouse.NewWithOptions(clickhouse.Options{
		Authenticate: authenticator(t),
		Passthrough:  func(user string) bool { return user == "bob" },
	})
	in := hello("bob", "own")
	out, err := c.(gate.StreamFilter).Filter(inspect.FromClient, in)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("Filter = %x, %v; want the hello unchanged", out, err)
	}
	id, md, ok := c.(loginTerminator).LoginIdentity()
	if !ok || id.Subject != "bob" || md["verified"] != "false" || md["upstream_user"] != "bob" {
		t.Fatalf("identity = %+v metadata = %v ok = %v", id, md, ok)
	}
}

// A refused Hello reaches the gate as the identity refusal: nothing is
// forwarded, the audit row names the claimed user, never the password.
func TestTerminatingLaneRefusalReachesTheGate(t *testing.T) {
	c := clickhouse.NewWithOptions(clickhouse.Options{Authenticate: authenticator(t)})
	out, err := c.(gate.StreamFilter).Filter(inspect.FromClient, hello("mallory", "guess"))
	var refusal *inspect.CredentialRefusal
	if !errors.As(err, &refusal) || len(out) != 0 {
		t.Fatalf("Filter = %x, %v; want a CredentialRefusal", out, err)
	}
	if refusal.Request != "Hello mallory" || refusal.User != "mallory" || !errors.Is(err, codectypes.ErrLoginRefused) {
		t.Fatalf("refusal = %+v", refusal)
	}
	if bytes.Contains([]byte(err.Error()), []byte("guess")) {
		t.Fatalf("password in the refusal: %v", err)
	}

	sink := audit.NewMemorySink(8)
	sess := session.New(inspect.ClickHouse, session.Identity{PeerAddr: "10.0.0.1:1"})
	g, err := gate.New(sess, gate.Config{
		Protocol: inspect.ClickHouse,
		Audit:    sink,
		CodecFactory: func() inspect.Codec {
			return clickhouse.NewWithOptions(clickhouse.Options{Authenticate: authenticator(t)})
		},
	})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	d := g.Request(context.Background(), hello("mallory", "guess"))
	if d.Allowed || d.Rule != "identity" || len(d.Payload) != 0 {
		t.Fatalf("decision = %+v", d)
	}
	// Audited under the user the Hello claimed, not the bare peer.
	var violation *audit.Event
	for _, ev := range sink.Events() {
		if ev.Kind == audit.KindViolation {
			violation = &ev
			break
		}
	}
	if violation == nil || violation.Principal != "mallory" || violation.Metadata["login.user"] != "mallory" {
		t.Fatalf("violation = %+v, want principal mallory with login.user", violation)
	}
	// The happy path through the gate: the Hello is swapped and the session
	// now names the verified principal.
	sess = session.New(inspect.ClickHouse, session.Identity{PeerAddr: "10.0.0.1:1"})
	g, err = gate.New(sess, gate.Config{
		Protocol: inspect.ClickHouse,
		CodecFactory: func() inspect.Codec {
			return clickhouse.NewWithOptions(clickhouse.Options{Authenticate: authenticator(t)})
		},
	})
	if err != nil {
		t.Fatalf("gate.New: %v", err)
	}
	d = g.Request(context.Background(), hello("alice", "pw"))
	if !d.Allowed || !bytes.Equal(d.Payload, hello("svc", "s")) {
		t.Fatalf("decision = %+v", d)
	}
	if got := g.Session(); got.Identity.Subject != "alice@example.com" || got.Metadata["upstream_user"] != "svc" || got.Metadata["verified"] != "true" {
		t.Fatalf("session after login = %+v %v", got.Identity, got.Metadata)
	}
}

func TestNilAuthenticateStaysPassthrough(t *testing.T) {
	c := clickhouse.NewWithOptions(clickhouse.Options{})
	if _, ok := c.(loginTerminator); ok {
		t.Fatal("a pass-through lane claims to terminate the login")
	}
	in := hello("alice", "pw")
	out, err := c.(gate.StreamFilter).Filter(inspect.FromClient, in)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("Filter = %x, %v; want the hello unchanged", out, err)
	}
}

func is[T any](v any) bool { _, ok := v.(T); return ok }
