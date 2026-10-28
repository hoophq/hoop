package login

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	pgauth "github.com/hoophq/libhoop/v2/codec/postgres/auth"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// pgFakeServer answers one upstream login on its end of a pipe with the
// method it is told to, then the post-login messages.
type pgFakeServer struct {
	method   string // "scram", "md5", "cleartext", "trust"
	user     string
	password string

	mu      sync.Mutex
	startup map[string]string
	err     error
}

func (s *pgFakeServer) serve(conn net.Conn) {
	defer conn.Close()
	params, _, err := pgauth.ReadStartup(conn)
	if err != nil {
		s.fail(err)
		return
	}
	s.mu.Lock()
	s.startup = params
	s.mu.Unlock()
	if params["user"] != s.user {
		conn.Write(pgauth.ErrorResponse("28P01", "password authentication failed for user "+params["user"]))
		return
	}
	ok := false
	switch s.method {
	case "trust":
		ok = true
	case "cleartext":
		conn.Write(pgauth.AuthCleartext())
		_, body, err := pgauth.ReadFrontend(conn)
		if err != nil {
			s.fail(err)
			return
		}
		pw, _ := pgauth.ParsePasswordMessage(body)
		ok = pw == s.password
	case "md5":
		salt := [4]byte{1, 2, 3, 4}
		conn.Write(pgauth.AuthMD5(salt))
		_, body, err := pgauth.ReadFrontend(conn)
		if err != nil {
			s.fail(err)
			return
		}
		pw, _ := pgauth.ParsePasswordMessage(body)
		ok = pw == pgauth.MD5Password(s.user, s.password, salt[:])
	case "scram":
		material, _ := pgauth.Material(s.password, []byte("upstreamsalt0000"), 4096)
		srv := pgauth.NewScramServer(material)
		conn.Write(pgauth.AuthSASL([]string{pgauth.ScramSHA256, "SCRAM-SHA-256-PLUS"}))
		_, body, err := pgauth.ReadFrontend(conn)
		if err != nil {
			s.fail(err)
			return
		}
		mech, first, err := pgauth.ParseSASLInitialResponse(body)
		if err != nil || mech != pgauth.ScramSHA256 {
			s.fail(errors.New("bad sasl initial response"))
			return
		}
		sf, err := srv.ServerFirst(first)
		if err != nil {
			s.fail(err)
			return
		}
		conn.Write(pgauth.AuthSASLContinue(sf))
		_, body, err = pgauth.ReadFrontend(conn)
		if err != nil {
			s.fail(err)
			return
		}
		final, err := srv.ServerFinal(body)
		if err == nil {
			conn.Write(pgauth.AuthSASLFinal(final))
			ok = true
		}
	}
	if !ok {
		conn.Write(pgauth.ErrorResponse("28P01", "password authentication failed for user "+s.user))
		return
	}
	conn.Write(pgauth.AuthOk())
	conn.Write(pgauth.ParameterStatus("server_version", "16.2"))
	conn.Write(pgauth.BackendKeyData(4242, 777))
	conn.Write(pgauth.ReadyForQuery())
	// Keep the socket open until the client goes away: the relay pumps
	// after login and a test may read one more frame.
	buf := make([]byte, 64)
	for {
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

func (s *pgFakeServer) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *pgFakeServer) seen() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startup, s.err
}

// pgLoginHarness wires a proxy.LoginConn to a fake client and a fake upstream.
type pgLoginHarness struct {
	t        *testing.T
	client   net.Conn // the test's end of the client leg
	server   *pgFakeServer
	conn     *proxy.LoginConn
	dialed   []*proxy.Credential
	inspects []struct {
		dir inspect.Direction
		pkt []byte
	}
	mu sync.Mutex
}

func newPGLoginHarness(t *testing.T, server *pgFakeServer, lane proxy.LaneInfo) *pgLoginHarness {
	t.Helper()
	clientEnd, relayEnd := net.Pipe()
	h := &pgLoginHarness{t: t, client: clientEnd, server: server}
	h.conn = &proxy.LoginConn{
		Client: relayEnd,
		Lane:   lane,
		Dial: func(_ context.Context, cred *proxy.Credential) (net.Conn, error) {
			h.mu.Lock()
			h.dialed = append(h.dialed, cred)
			h.mu.Unlock()
			up, down := net.Pipe()
			go server.serve(down)
			return up, nil
		},
		Inspect: func(dir inspect.Direction, pkt []byte) error {
			h.mu.Lock()
			h.inspects = append(h.inspects, struct {
				dir inspect.Direction
				pkt []byte
			}{dir, bytes.Clone(pkt)})
			h.mu.Unlock()
			return nil
		},
	}
	t.Cleanup(func() { clientEnd.Close(); relayEnd.Close() })
	return h
}

// run drives the login in the background while the test plays the client.
func (h *pgLoginHarness) run() <-chan struct {
	res proxy.LoginResult
	err error
} {
	out := make(chan struct {
		res proxy.LoginResult
		err error
	}, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := loginPostgres(ctx, h.conn)
		out <- struct {
			res proxy.LoginResult
			err error
		}{res, err}
	}()
	return out
}

func (h *pgLoginHarness) wait(done <-chan struct {
	res proxy.LoginResult
	err error
}) (proxy.LoginResult, error) {
	h.t.Helper()
	select {
	case r := <-done:
		return r.res, r.err
	case <-time.After(5 * time.Second):
		h.t.Fatal("login did not finish")
		return proxy.LoginResult{}, nil
	}
}

func (h *pgLoginHarness) readBackend() (byte, []byte) {
	h.t.Helper()
	h.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	typ, body, err := pgauth.ReadBackend(h.client)
	if err != nil {
		h.t.Fatalf("client read: %v", err)
	}
	return typ, body
}

func (h *pgLoginHarness) expectAuth(code int32) []byte {
	h.t.Helper()
	typ, body := h.readBackend()
	if typ != pgauth.TagAuthentication {
		h.t.Fatalf("client got %q, want authentication", typ)
	}
	got, data, err := pgauth.ParseAuth(body)
	if err != nil || got != code {
		h.t.Fatalf("auth code %d err=%v, want %d", got, err, code)
	}
	return data
}

// expectLoggedIn consumes AuthOk, ParameterStatus, BackendKeyData and
// ReadyForQuery and returns the key data the client was shown.
func (h *pgLoginHarness) expectLoggedIn() (pid, key int32) {
	h.t.Helper()
	h.expectAuth(pgauth.AuthOkCode)
	typ, body := h.readBackend()
	if typ != pgauth.TagParameterStatus || !bytes.Equal(body, []byte("server_version\x0016.2\x00")) {
		h.t.Fatalf("got %q %q, want ParameterStatus", typ, body)
	}
	typ, body = h.readBackend()
	if typ != pgauth.TagBackendKeyData {
		h.t.Fatalf("got %q, want BackendKeyData", typ)
	}
	pid, key, err := pgauth.ParseBackendKeyData(body)
	if err != nil {
		h.t.Fatal(err)
	}
	if typ, _ := h.readBackend(); typ != pgauth.TagReadyForQuery {
		h.t.Fatalf("got %q, want ReadyForQuery", typ)
	}
	return pid, key
}

func (h *pgLoginHarness) expectRefusal() {
	h.t.Helper()
	typ, body := h.readBackend()
	if typ != pgauth.TagErrorResponse {
		h.t.Fatalf("got %q, want ErrorResponse", typ)
	}
	f := pgauth.ErrorFields(body)
	if f['C'] != "28P01" || f['S'] != "FATAL" || !strings.HasPrefix(f['M'], "password authentication failed for user") {
		h.t.Fatalf("refusal fields %v", f)
	}
	if strings.Contains(f['M'], "scram") || strings.Contains(f['M'], "material") {
		h.t.Fatalf("refusal leaks the reason: %q", f['M'])
	}
}

func pgAuthenticator(t *testing.T, wantUser, wantSecret string, cred proxy.Credential) func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
	return func(_ context.Context, user, secret string) (session.Identity, proxy.Credential, error) {
		if user != wantUser || secret != wantSecret {
			return session.Identity{}, proxy.Credential{}, fmt.Errorf("%w: bad secret", proxy.ErrLoginRefused)
		}
		return session.Identity{Subject: "alice@example.com", Email: "alice@example.com", Groups: []string{"dba"}}, cred, nil
	}
}

func pgTCPLane(tls bool) proxy.LaneInfo {
	return proxy.LaneInfo{Protocol: inspect.Postgres, Name: "pg", Network: "tcp", ClientTLS: tls, UpstreamAddr: "db.internal:5432"}
}

func TestPostgresLoginPassthroughForwardsStartupUnchanged(t *testing.T) {
	server := &pgFakeServer{method: "trust", user: "svc"}
	h := newPGLoginHarness(t, server, pgTCPLane(false))
	h.conn.Passthrough = func(user string) bool { return user == "svc" }
	h.conn.Authenticate = func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
		t.Fatal("Authenticate called for a pass-through user")
		return session.Identity{}, proxy.Credential{}, nil
	}
	done := h.run()
	startup := pgauth.Startup(map[string]string{"user": "svc", "database": "app", "options": "-c search_path=x"})
	if _, err := h.client.Write(startup); err != nil {
		t.Fatal(err)
	}
	res, err := h.wait(done)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passthrough || res.Identity.Subject != "svc" || res.Cancel != nil {
		t.Fatalf("result %+v", res)
	}
	if len(h.dialed) != 1 || h.dialed[0] != nil {
		t.Fatalf("dialed with %v, want one nil credential", h.dialed)
	}
	if len(h.inspects) != 1 || h.inspects[0].dir != inspect.FromClient || !bytes.Equal(h.inspects[0].pkt, startup) {
		t.Fatalf("inspected %v, want the startup once", h.inspects)
	}
	// The server read the startup the client sent, byte for byte.
	deadline := time.Now().Add(2 * time.Second)
	for {
		seen, serr := server.seen()
		if serr != nil {
			t.Fatal(serr)
		}
		if seen != nil {
			if seen["user"] != "svc" || seen["options"] != "-c search_path=x" {
				t.Fatalf("server saw %v", seen)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never read the startup")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Pumps run after the driver: the client's first message still reaches
	// the server end through the conns the driver left behind.
	if res.Passthrough && h.conn.Upstream == nil {
		t.Fatal("upstream not stored")
	}
}

func TestPostgresLoginCleartextClientSwapsUpstreamSCRAM(t *testing.T) {
	for _, method := range []string{"scram", "md5", "cleartext", "trust"} {
		t.Run(method, func(t *testing.T) {
			server := &pgFakeServer{method: method, user: "app_rw", password: "pool-secret"}
			h := newPGLoginHarness(t, server, pgTCPLane(true))
			cred := proxy.Credential{Name: "rw", User: "app_rw", Password: "pool-secret"}
			h.conn.Authenticate = pgAuthenticator(t, "alice", "hoop-key-123", cred)
			h.conn.Propagate = proxy.PropagateInfo{ApplicationName: "{{.Principal}} via {{.Context.pod}}"}
			h.conn.Context = map[string]string{"pod": "web-1"}
			done := h.run()

			h.client.Write(pgauth.Startup(map[string]string{"user": "alice", "application_name": "psql"}))
			h.expectAuth(pgauth.AuthCleartextCode)
			h.client.Write(pgauth.PasswordMessage("hoop-key-123"))
			pid, key := h.expectLoggedIn()

			res, err := h.wait(done)
			if err != nil {
				t.Fatal(err)
			}
			if res.Passthrough || res.Identity.Subject != "alice@example.com" || res.Credential.Name != "rw" {
				t.Fatalf("result %+v", res)
			}
			if res.Cancel == nil || res.Cancel.UpstreamPID != 4242 || res.Cancel.UpstreamKey != 777 {
				t.Fatalf("cancel mapping %+v", res.Cancel)
			}
			if res.Cancel.ClientPID != pid || res.Cancel.ClientKey != key || pid == 4242 {
				t.Fatalf("client shown pid=%d key=%d, mapping %+v", pid, key, res.Cancel)
			}
			if len(h.dialed) != 1 || h.dialed[0] == nil || h.dialed[0].User != "app_rw" {
				t.Fatalf("dialed %v", h.dialed)
			}
			seen, serr := server.seen()
			if serr != nil {
				t.Fatal(serr)
			}
			if seen["user"] != "app_rw" || seen["database"] != "alice" || seen["application_name"] != "alice@example.com via web-1" {
				t.Fatalf("upstream startup %v", seen)
			}
			// The gate saw the upstream startup and every message the client
			// got after AuthOk, with the relay's key data, never the secret.
			var fromClient, fromServer int
			for _, in := range h.inspects {
				if bytes.Contains(in.pkt, []byte("hoop-key-123")) || bytes.Contains(in.pkt, []byte("pool-secret")) {
					t.Fatalf("secret reached the gate: %q", in.pkt)
				}
				switch in.dir {
				case inspect.FromClient:
					fromClient++
					if _, _, err := pgauth.ReadStartup(bytes.NewReader(in.pkt)); err != nil {
						t.Fatalf("client-side packet is not the startup: %v", err)
					}
				case inspect.FromServer:
					fromServer++
					if in.pkt[0] == pgauth.TagBackendKeyData {
						if p, k, _ := pgauth.ParseBackendKeyData(in.pkt[5:]); p != pid || k != key {
							t.Fatalf("gate saw upstream key data %d/%d", p, k)
						}
					}
				}
			}
			if fromClient != 1 || fromServer != 4 {
				t.Fatalf("inspected %d client / %d server packets", fromClient, fromServer)
			}
		})
	}
}

func TestPostgresLoginKeepsClientApplicationNameWithoutPropagate(t *testing.T) {
	server := &pgFakeServer{method: "trust", user: "app_rw"}
	h := newPGLoginHarness(t, server, proxy.LaneInfo{Protocol: inspect.Postgres, Network: "unix", UpstreamAddr: "/run/pg.sock"})
	h.conn.Authenticate = pgAuthenticator(t, "alice", "k", proxy.Credential{Name: "rw", User: "app_rw"})
	done := h.run()
	h.client.Write(pgauth.Startup(map[string]string{"user": "alice", "database": "app", "application_name": "psql"}))
	h.expectAuth(pgauth.AuthCleartextCode)
	h.client.Write(pgauth.PasswordMessage("k"))
	h.expectLoggedIn()
	if _, err := h.wait(done); err != nil {
		t.Fatal(err)
	}
	seen, _ := server.seen()
	if seen["database"] != "app" || seen["application_name"] != "psql" {
		t.Fatalf("upstream startup %v", seen)
	}
}

func TestPostgresLoginRefusesWrongSecret(t *testing.T) {
	server := &pgFakeServer{method: "scram", user: "app_rw", password: "pool-secret"}
	h := newPGLoginHarness(t, server, pgTCPLane(true))
	h.conn.Authenticate = pgAuthenticator(t, "alice", "right", proxy.Credential{User: "app_rw", Password: "pool-secret"})
	done := h.run()
	h.client.Write(pgauth.Startup(map[string]string{"user": "alice"}))
	h.expectAuth(pgauth.AuthCleartextCode)
	h.client.Write(pgauth.PasswordMessage("wrong"))
	h.expectRefusal()
	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err=%v", err)
	}
	if res.Identity.Subject != "alice" || len(h.dialed) != 0 {
		t.Fatalf("result %+v dialed %v", res, h.dialed)
	}
}

func TestPostgresLoginRefusesPlainTCP(t *testing.T) {
	h := newPGLoginHarness(t, &pgFakeServer{method: "trust", user: "x"}, pgTCPLane(false))
	h.conn.Authenticate = pgAuthenticator(t, "alice", "k", proxy.Credential{User: "x"})
	done := h.run()
	h.client.Write(pgauth.Startup(map[string]string{"user": "alice"}))
	h.expectRefusal()
	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) || res.Identity.Subject != "alice" {
		t.Fatalf("err=%v res=%+v", err, res)
	}
}

// A swapped login forwards an allowlist of the client's startup
// parameters: options, which can set a role or any GUC, and unknown keys
// stop at the relay; replication goes through only as false.
func TestPostgresLoginForwardsOnlyAllowlistedStartupParams(t *testing.T) {
	server := &pgFakeServer{method: "trust", user: "app_rw"}
	h := newPGLoginHarness(t, server, pgTCPLane(true))
	h.conn.Authenticate = pgAuthenticator(t, "alice", "k", proxy.Credential{Name: "rw", User: "app_rw"})
	done := h.run()
	h.client.Write(pgauth.Startup(map[string]string{
		"user": "alice", "database": "app", "application_name": "psql", "client_encoding": "UTF8",
		"DateStyle": "ISO", "TimeZone": "UTC", "replication": "false",
		"options": "-c role=admin", "search_path": "evil", "x_custom": "1",
	}))
	h.expectAuth(pgauth.AuthCleartextCode)
	h.client.Write(pgauth.PasswordMessage("k"))
	h.expectLoggedIn()
	if _, err := h.wait(done); err != nil {
		t.Fatal(err)
	}
	seen, serr := server.seen()
	if serr != nil {
		t.Fatal(serr)
	}
	want := map[string]string{"user": "app_rw", "database": "app", "application_name": "psql", "client_encoding": "UTF8",
		"DateStyle": "ISO", "TimeZone": "UTC", "replication": "false"}
	if len(seen) != len(want) {
		t.Fatalf("upstream startup %v, want %v", seen, want)
	}
	for k, v := range want {
		if seen[k] != v {
			t.Fatalf("upstream startup %v, want %v", seen, want)
		}
	}
	if _, ok := seen["options"]; ok {
		t.Fatalf("options forwarded: %v", seen)
	}
	if got := pgUpstreamParams(map[string]string{"user": "alice", "replication": "database"}, "app_rw"); got["replication"] != "" || got["database"] != "alice" {
		t.Fatalf("replication=database forwarded or database not defaulted: %v", got)
	}
}

// A login that fails after the credential was selected still reports it,
// so the relay returns it to its source.
func TestPostgresLoginReportsCredentialOnUpstreamFailure(t *testing.T) {
	server := &pgFakeServer{method: "scram", user: "app_rw", password: "rotated"}
	h := newPGLoginHarness(t, server, pgTCPLane(true))
	released := 0
	cred := proxy.Credential{Name: "rw", User: "app_rw", Password: "stale", Release: func(context.Context) error { released++; return nil }}
	h.conn.Authenticate = pgAuthenticator(t, "alice", "k", cred)
	done := h.run()
	h.client.Write(pgauth.Startup(map[string]string{"user": "alice"}))
	h.expectAuth(pgauth.AuthCleartextCode)
	h.client.Write(pgauth.PasswordMessage("k"))
	h.readBackend()
	res, err := h.wait(done)
	if err == nil || errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err=%v, want an upstream failure", err)
	}
	if res.Credential.Name != "rw" || res.Credential.Release == nil {
		t.Fatalf("result carries no credential to release: %+v", res)
	}
	if released != 0 {
		t.Fatal("the driver released; that is the relay's job")
	}
}

func TestPostgresLoginUpstreamRefusalIsNotAClientRefusal(t *testing.T) {
	server := &pgFakeServer{method: "scram", user: "app_rw", password: "rotated"}
	h := newPGLoginHarness(t, server, pgTCPLane(true))
	h.conn.Authenticate = pgAuthenticator(t, "alice", "k", proxy.Credential{Name: "rw", User: "app_rw", Password: "stale"})
	done := h.run()
	h.client.Write(pgauth.Startup(map[string]string{"user": "alice"}))
	h.expectAuth(pgauth.AuthCleartextCode)
	h.client.Write(pgauth.PasswordMessage("k"))
	typ, body := h.readBackend()
	f := pgauth.ErrorFields(body)
	if typ != pgauth.TagErrorResponse || f['C'] != "08006" || strings.Contains(f['M'], "stale") {
		t.Fatalf("client got %q %v", typ, f)
	}
	_, err := h.wait(done)
	if err == nil || errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err=%v, want a non-refusal error", err)
	}
	var se *pgauth.ServerError
	if !errors.As(err, &se) || se.Code != "28P01" {
		t.Fatalf("err=%v, want the upstream 28P01", err)
	}
}

// FuzzLogin feeds arbitrary bytes to the two login decoders. They must
// return an error or a consistent result, never panic or allocate from an
// unchecked length.
func FuzzLogin(f *testing.F) {
	f.Add(pgauth.Startup(map[string]string{"user": "alice", "database": "app"}))
	f.Add(pgauth.CancelRequest(1, 2))
	f.Add([]byte{0, 0, 0, 8, 4, 210, 22, 47}) // SSLRequest
	f.Add([]byte{0, 0, 0, 8, 4, 210, 22, 48}) // GSSENCRequest
	f.Add([]byte{0, 0, 0, 9, 0, 3, 0, 0, 'u'})
	f.Add(pgauth.AuthSASL([]string{pgauth.ScramSHA256}))
	f.Add(pgauth.AuthSASLContinue([]byte("r=abc,s=c2FsdA==,i=4096")))
	f.Add(pgauth.BackendKeyData(1, 2))
	f.Add(pgauth.ErrorResponse("28P01", "nope"))
	f.Add(pgauth.SASLInitialResponse(pgauth.ScramSHA256, []byte("n,,n=u,r=abc")))
	f.Add(pgauth.PasswordMessage("pw"))
	f.Add([]byte{'R', 0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{'R', 0, 0, 0, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		if params, raw, err := pgauth.ReadStartup(bytes.NewReader(data)); err == nil {
			if len(raw) > len(data) || params == nil {
				t.Fatalf("startup: raw=%d data=%d params=%v", len(raw), len(data), params)
			}
			again, _, err := pgauth.ReadStartup(bytes.NewReader(pgauth.Startup(params)))
			if err != nil || len(again) != len(params) {
				t.Fatalf("startup does not round-trip: %v", err)
			}
		} else if raw != nil && !errors.Is(err, pgauth.ErrSSLRequest) &&
			!errors.Is(err, pgauth.ErrGSSEncRequest) && !errors.Is(err, pgauth.ErrCancelRequest) {
			t.Fatalf("raw returned with error %v", err)
		}
		typ, body, err := pgauth.ReadBackend(bytes.NewReader(data))
		if err != nil {
			return
		}
		if len(body)+5 > len(data) {
			t.Fatalf("body longer than input")
		}
		switch typ {
		case pgauth.TagAuthentication:
			if code, d, err := pgauth.ParseAuth(body); err == nil && code == pgauth.AuthSASLCode {
				pgauth.ParseAuthSASL(d)
			}
		case pgauth.TagBackendKeyData:
			pgauth.ParseBackendKeyData(body)
		case pgauth.TagErrorResponse:
			pgauth.ErrorFields(body)
		case pgauth.TagPassword:
			pgauth.ParsePasswordMessage(body)
			pgauth.ParseSASLInitialResponse(body)
		}
	})
}
