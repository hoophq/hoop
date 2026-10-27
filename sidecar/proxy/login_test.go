package proxy_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// lineProto is a line protocol invented for these tests: the client sends
// "login <user> <secret>\n", then one statement per line, and the upstream
// echoes. It has no codec in the registry, so the lane builds one.
const lineProto inspect.Protocol = "lineproto"

type lineCodec struct{}

func (lineCodec) Protocol() inspect.Protocol { return lineProto }

func (lineCodec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	i := bytes.IndexByte(data, '\n')
	if i < 0 {
		return nil, 0, nil
	}
	// The login line is the handshake: forwarded, never a statement.
	if bytes.HasPrefix(data, []byte("login ")) {
		return nil, i + 1, nil
	}
	stmt := inspect.Statement{
		Protocol: lineProto, Direction: dir, Operation: inspect.OpSelect,
		Text: string(data[:i]),
	}
	return []inspect.Statement{stmt}, i + 1, nil
}

// lineLogin is the fake driver. It reads the login line, verifies through
// c.Authenticate, dials, and answers "ok" or "refused" to the client.
func lineLogin(ctx context.Context, c *proxy.LoginConn) (proxy.LoginResult, error) {
	line, err := bufio.NewReader(c.Client).ReadString('\n')
	if err != nil {
		return proxy.LoginResult{}, err
	}
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[0] != "login" {
		return proxy.LoginResult{}, errors.New("malformed login")
	}
	user, secret := fields[1], fields[2]
	if err := c.Inspect(inspect.FromClient, []byte(line)); err != nil {
		return proxy.LoginResult{}, err
	}
	if c.Passthrough(user) {
		up, err := c.Dial(ctx, nil)
		if err != nil {
			return proxy.LoginResult{}, err
		}
		c.Upstream = up
		_, _ = c.Client.Write([]byte("ok\n"))
		return proxy.LoginResult{Identity: session.Identity{Subject: user}, Passthrough: true}, nil
	}
	id, cred, err := c.Authenticate(ctx, user, secret)
	if err != nil {
		_, _ = c.Client.Write([]byte("refused\n"))
		return proxy.LoginResult{Identity: session.Identity{Subject: user}}, err
	}
	up, err := c.Dial(ctx, &cred)
	if err != nil {
		return proxy.LoginResult{Identity: id, Credential: cred}, err
	}
	c.Upstream = up
	_, _ = c.Client.Write([]byte("ok as " + c.Propagate.Render(id, c.Context) + "\n"))
	return proxy.LoginResult{
		Identity:   id,
		Credential: cred,
		Cancel:     &proxy.CancelMapping{ClientPID: 1, ClientKey: 2, UpstreamPID: 3, UpstreamKey: 4},
	}, nil
}

func init() {
	proxy.RegisterLogin(lineProto, lineLogin)
}

func lineAuthenticate(released *atomic.Int32) func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
	return func(_ context.Context, user, secret string) (session.Identity, proxy.Credential, error) {
		if user != "alice" || secret != "s3cret" {
			return session.Identity{}, proxy.Credential{}, fmt.Errorf("bad secret for %s: %w", user, proxy.ErrLoginRefused)
		}
		return session.Identity{
			Subject:    "alice@example.com",
			Attributes: map[string]string{"identity_type": "kubernetes"},
		}, proxy.Credential{
			Name: "app-rw", User: "app_rw", Password: "pw",
			Release: func(context.Context) error { released.Add(1); return nil },
		}, nil
	}
}

func dialLine(t *testing.T, srv *proxy.Server, login string) *bufio.Reader {
	t.Helper()
	c, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(login)); err != nil {
		t.Fatalf("write login: %v", err)
	}
	return bufio.NewReader(c)
}

func waitKind(t *testing.T, sink *audit.MemorySink, k audit.Kind) []audit.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ev := sink.Events(); hasKind(ev, k) {
			return ev
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s recorded: %+v", k, sink.Events())
	return nil
}

func TestLoginAdoptsVerifiedIdentity(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)
	var released atomic.Int32
	srv := startServer(t, proxy.Config{
		Upstream:     up.addr(),
		Protocol:     lineProto,
		Connection:   "appdb",
		Audit:        sink,
		CodecFactory: func() inspect.Codec { return lineCodec{} },
		Authenticate: lineAuthenticate(&released),
		Propagate:    proxy.PropagateInfo{ApplicationName: "{{.Principal}} via {{.Context.pod}}"},
		LaneContext:  map[string]string{"pod": "web-1"},
	})

	cl, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	_ = cl.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := cl.Write([]byte("login alice s3cret\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(cl)
	if line, _ := br.ReadString('\n'); line != "ok as alice@example.com via web-1\n" {
		t.Fatalf("login answer = %q", line)
	}
	if _, err := cl.Write([]byte("SELECT 1\n")); err != nil {
		t.Fatal(err)
	}
	if line, _ := br.ReadString('\n'); line != "SELECT 1\n" {
		t.Fatalf("echo = %q", line)
	}
	// The upstream saw the statement and never the login line.
	if got := up.got(); bytes.Contains(got, []byte("login")) || !bytes.Contains(got, []byte("SELECT 1\n")) {
		t.Fatalf("upstream received %q", got)
	}
	cl.Close()

	events := waitKind(t, sink, audit.KindSessionEnd)
	var start *audit.Event
	for i := range events {
		if events[i].Kind == audit.KindSessionStart && events[i].Metadata["credential"] != "" {
			start = &events[i]
		}
	}
	if start == nil {
		t.Fatalf("no session_start with login metadata: %+v", events)
	}
	if start.Principal != "alice@example.com" {
		t.Errorf("session_start principal = %q", start.Principal)
	}
	want := map[string]string{
		"credential": "app-rw", "upstream_user": "app_rw",
		"identity_type": "kubernetes", "verified": "true",
	}
	for k, v := range want {
		if start.Metadata[k] != v {
			t.Errorf("session_start metadata[%s] = %q, want %q", k, start.Metadata[k], v)
		}
	}
	for _, e := range events {
		if e.Kind == audit.KindStatement && e.Statement == "SELECT 1" && e.Principal != "alice@example.com" {
			t.Errorf("statement principal = %q", e.Principal)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for released.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if released.Load() != 1 {
		t.Errorf("credential released %d times, want 1", released.Load())
	}
}

// A credential selected by a login that then fails goes back to its source
// at once: the relay releases it, with no session to end.
func TestLoginFailureAfterSelectionReleasesCredential(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	var released atomic.Int32
	srv := startServer(t, proxy.Config{
		Upstream:     dead,
		Protocol:     lineProto,
		Connection:   "appdb",
		Audit:        audit.NewMemorySink(64),
		CodecFactory: func() inspect.Codec { return lineCodec{} },
		Authenticate: lineAuthenticate(&released),
	})
	br := dialLine(t, srv, "login alice s3cret\n")
	if _, err := br.ReadString('\n'); err == nil {
		t.Fatal("a login with no upstream answered")
	}
	deadline := time.Now().Add(3 * time.Second)
	for released.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if released.Load() != 1 {
		t.Fatalf("credential released %d times, want 1", released.Load())
	}
}

// A login the lane verified and then could not complete on its own side
// is in the trail under the verified principal, with the failure: an
// outage after a successful login is not an anonymous connection, and not
// a refusal either.
func TestLoginFailureAfterVerificationIsAuditedUnderThePrincipal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	sink := audit.NewMemorySink(64)
	var released atomic.Int32
	srv := startServer(t, proxy.Config{
		Upstream:     dead,
		Protocol:     lineProto,
		Connection:   "appdb",
		Audit:        sink,
		CodecFactory: func() inspect.Codec { return lineCodec{} },
		Authenticate: lineAuthenticate(&released),
	})
	br := dialLine(t, srv, "login alice s3cret\n")
	if _, err := br.ReadString('\n'); err == nil {
		t.Fatal("a login with no upstream answered")
	}

	events := waitKind(t, sink, audit.KindSessionEnd)
	var kinds []audit.Kind
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		if e.Principal != "alice@example.com" {
			t.Errorf("%s principal = %q, want the verified principal", e.Kind, e.Principal)
		}
		switch e.Kind {
		case audit.KindSessionStart:
			want := map[string]string{"verified": "true", "credential": "app-rw", "upstream_user": "app_rw", "identity_type": "kubernetes"}
			for k, v := range want {
				if e.Metadata[k] != v {
					t.Errorf("session_start metadata[%s] = %q, want %q", k, e.Metadata[k], v)
				}
			}
		case audit.KindError:
			if !strings.Contains(e.Error, "connection refused") {
				t.Errorf("error event = %q, want the dial failure", e.Error)
			}
		case audit.KindViolation:
			t.Errorf("a failure after verification was recorded as a refusal: %+v", e)
		}
	}
	if len(kinds) != 3 || kinds[0] != audit.KindSessionStart || kinds[1] != audit.KindError || kinds[2] != audit.KindSessionEnd {
		t.Fatalf("events = %v, want start, error, end", kinds)
	}
	if _, _, denied := srv.Stats(); denied != 0 {
		t.Errorf("denied = %d, want 0: a failure is not a refusal", denied)
	}
}

func TestLoginRefusedIsOneViolation(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)
	var released atomic.Int32
	srv := startServer(t, proxy.Config{
		Upstream:     up.addr(),
		Protocol:     lineProto,
		Connection:   "appdb",
		Audit:        sink,
		DenyWriter:   proxy.ProtocolDenyWriter{},
		CodecFactory: func() inspect.Codec { return lineCodec{} },
		Authenticate: lineAuthenticate(&released),
	})

	r := dialLine(t, srv, "login mallory wrong\n")
	if line, _ := r.ReadString('\n'); line != "refused\n" {
		t.Fatalf("login answer = %q", line)
	}
	if _, err := r.ReadByte(); err != io.EOF {
		t.Fatalf("connection stayed open after the refusal: %v", err)
	}

	events := waitKind(t, sink, audit.KindSessionEnd)
	var kinds []audit.Kind
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		if e.Principal != "mallory" {
			t.Errorf("%s principal = %q, want mallory", e.Kind, e.Principal)
		}
		switch e.Kind {
		case audit.KindSessionStart:
			if e.Metadata["verified"] != "false" {
				t.Errorf("session_start verified = %q", e.Metadata["verified"])
			}
		case audit.KindViolation:
			if e.Rule != "identity" || !strings.Contains(e.Message, "bad secret for mallory") {
				t.Errorf("violation = rule %q message %q", e.Rule, e.Message)
			}
			if e.Metadata["login.user"] != "mallory" {
				t.Errorf("violation metadata = %v", e.Metadata)
			}
		case audit.KindSessionEnd:
			if e.StatementCount != 1 || e.DeniedCount != 1 {
				t.Errorf("session_end counts = %d/%d", e.StatementCount, e.DeniedCount)
			}
		}
	}
	if len(kinds) != 3 || kinds[0] != audit.KindSessionStart || kinds[1] != audit.KindViolation || kinds[2] != audit.KindSessionEnd {
		t.Fatalf("events = %v, want start, violation, end", kinds)
	}
	if got := up.got(); len(got) > 0 {
		t.Fatalf("upstream received %q on a refused login", got)
	}
	if _, _, denied := srv.Stats(); denied != 1 {
		t.Errorf("denied = %d", denied)
	}
}

func TestLoginPassthroughUserIsClaimed(t *testing.T) {
	up := newEchoUpstream(t, nil)
	sink := audit.NewMemorySink(64)
	var released atomic.Int32
	srv := startServer(t, proxy.Config{
		Upstream:         up.addr(),
		Protocol:         lineProto,
		Audit:            sink,
		CodecFactory:     func() inspect.Codec { return lineCodec{} },
		Authenticate:     lineAuthenticate(&released),
		PassthroughUsers: []string{"migrator"},
	})
	r := dialLine(t, srv, "login migrator anything\n")
	if line, _ := r.ReadString('\n'); line != "ok\n" {
		t.Fatalf("login answer = %q", line)
	}
	events := waitKind(t, sink, audit.KindSessionStart)
	for _, e := range events {
		if e.Kind == audit.KindSessionStart {
			if e.Principal != "migrator" || e.Metadata["verified"] != "false" {
				t.Errorf("session_start = principal %q metadata %v", e.Principal, e.Metadata)
			}
		}
	}
}

// noDriverCodec speaks a protocol nothing registered a login driver for.
type noDriverCodec struct{ lineCodec }

func (noDriverCodec) Protocol() inspect.Protocol { return "nodriver" }

func TestAuthenticateNeedsADriver(t *testing.T) {
	_, err := proxy.NewServer(proxy.Config{
		Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1", Protocol: "nodriver",
		CodecFactory: func() inspect.Codec { return noDriverCodec{} },
		Authenticate: func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
			return session.Identity{}, proxy.Credential{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no login driver") {
		t.Fatalf("NewServer error = %v, want a missing-driver refusal", err)
	}
	_, err = proxy.NewServer(proxy.Config{
		Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1", Protocol: inspect.Postgres,
		PassthroughUsers: []string{"x"},
	})
	if err == nil {
		t.Fatal("PassthroughUsers without Authenticate was accepted")
	}
}

func TestPropagateRender(t *testing.T) {
	id := session.Identity{Subject: "sub", Email: "a@b"}
	cases := []struct{ tmpl, want string }{
		{"", ""},
		{"{{.Principal}}", "sub"},
		{"{{.Email}}/{{.Subject}}", "a@b/sub"},
		{"{{.Context.missing}}|{{.Context.pod}}", "|p1"},
	}
	for _, tc := range cases {
		got := proxy.PropagateInfo{ApplicationName: tc.tmpl}.Render(id, map[string]string{"pod": "p1"})
		if got != tc.want {
			t.Errorf("Render(%q) = %q, want %q", tc.tmpl, got, tc.want)
		}
	}
}
