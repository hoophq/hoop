package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	mssqlauth "github.com/hoophq/libhoop/v2/codec/mssql/auth"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// mssqlFakeServer is an in-process SQL Server for the login phase: PRELOGIN,
// the TLS handshake inside PRELOGIN packets, LOGIN7, and a LOGINACK or an
// 18456 ERROR. accept decides the login; sspi, when set, answers an
// integrated login with one challenge and accepts the client's answer.
type mssqlFakeServer struct {
	accept func(user, password string) bool
	sspi   func(token []byte) (challenge []byte, ok bool)

	mu        sync.Mutex
	login     *mssqlauth.Login
	prelogin  mssqlauth.Prelogin
	sspiSteps [][]byte
	// after is the TLS conn the session continues on once login ended.
	after *tls.Conn
}

func (s *mssqlFakeServer) serve(conn net.Conn) error {
	p, err := mssqlauth.ReadPacket(conn)
	if err != nil {
		return fmt.Errorf("prelogin: %w", err)
	}
	if p.Type != mssqlauth.PacketPrelogin {
		return fmt.Errorf("first packet %#x", p.Type)
	}
	pre, err := mssqlauth.ParsePrelogin(p.Payload)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.prelogin = pre
	s.mu.Unlock()
	reply := mssqlauth.ServerPrelogin(mssqlauth.EncryptOn)
	if err := mssqlauth.WritePacket(conn, mssqlauth.Packet{Type: mssqlauth.PacketReply, Status: mssqlauth.StatusEOM, ID: 1, Payload: reply.Encode()}); err != nil {
		return err
	}

	cfg, err := mssqlauth.LoginTLSConfig()
	if err != nil {
		return err
	}
	framed := mssqlauth.NewFramedConn(conn)
	tc := tls.Server(framed, cfg)
	if err := tc.Handshake(); err != nil {
		return fmt.Errorf("server handshake: %w", err)
	}
	framed.Upgrade()

	typ, body, err := mssqlauth.ReadMessage(tc)
	if err != nil {
		return fmt.Errorf("login7: %w", err)
	}
	if typ != mssqlauth.PacketLogin7 {
		return fmt.Errorf("login message %#x", typ)
	}
	login, err := mssqlauth.DecodeLogin(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.login = login
	s.after = tc
	s.mu.Unlock()

	ok := false
	if login.Integrated() {
		if s.sspi == nil {
			return errors.New("integrated login on a server without SSPI")
		}
		challenge, accepted := s.sspi(login.SSPI)
		s.mu.Lock()
		s.sspiSteps = append(s.sspiSteps, login.SSPI)
		s.mu.Unlock()
		if accepted && challenge != nil {
			// Challenge, then read the client's answer as an SSPI packet.
			if err := mssqlauth.WritePacket(tc, mssqlauth.Packet{Type: mssqlauth.PacketReply, Status: mssqlauth.StatusEOM, ID: 1, Payload: mssqlSSPIToken(challenge)}); err != nil {
				return err
			}
			styp, answer, err := mssqlauth.ReadMessage(tc)
			if err != nil {
				return fmt.Errorf("sspi answer: %w", err)
			}
			if styp != mssqlauth.PacketSSPI {
				return fmt.Errorf("sspi answer packet %#x", styp)
			}
			s.mu.Lock()
			s.sspiSteps = append(s.sspiSteps, answer)
			s.mu.Unlock()
			_, accepted = s.sspi(answer)
			ok = accepted
			if ok {
				// Mutual authentication: the final reply carries a token.
				_, err := tc.Write(mssqlauth.Packet{Type: mssqlauth.PacketReply, Status: mssqlauth.StatusEOM, ID: 1,
					Payload: append(mssqlSSPIToken([]byte("server-final")), mssqlLoginAck()...)}.Encode())
				return err
			}
		} else {
			ok = accepted
		}
	} else {
		ok = s.accept(login.UserName, login.Password)
	}
	if !ok {
		_, err := tc.Write(mssqlauth.LoginFailedReply(login.UserName))
		return err
	}
	_, err = tc.Write(mssqlauth.Packet{Type: mssqlauth.PacketReply, Status: mssqlauth.StatusEOM, ID: 1, Payload: mssqlLoginAck()}.Encode())
	return err
}

// mssqlLoginAck is ENVCHANGE(database) + LOGINACK + DONE.
func mssqlLoginAck() []byte {
	var out []byte
	env := []byte{0x01}
	env = append(env, byte(len("app")))
	env = append(env, ucs2("app")...)
	env = append(env, byte(len("master")))
	env = append(env, ucs2("master")...)
	out = append(out, mssqlauth.TokenEnvChange)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(env)))
	out = append(out, env...)

	ack := []byte{0x01}
	ack = binary.LittleEndian.AppendUint32(ack, 0x74000004)
	ack = append(ack, byte(len("fake")))
	ack = append(ack, ucs2("fake")...)
	ack = append(ack, 16, 0, 0, 0)
	out = append(out, mssqlauth.TokenLoginAck)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(ack)))
	out = append(out, ack...)

	out = append(out, mssqlauth.TokenDone)
	out = append(out, make([]byte, 12)...)
	return out
}

func mssqlSSPIToken(token []byte) []byte {
	out := []byte{mssqlauth.TokenSSPI}
	out = binary.LittleEndian.AppendUint16(out, uint16(len(token)))
	return append(out, token...)
}

// mssqlFakeClient drives the client leg: PRELOGIN with enc, the login TLS
// handshake, one LOGIN7, and returns the transports the session continues
// on: tlsConn under ENCRYPT_ON, raw otherwise.
type mssqlFakeClient struct {
	conn net.Conn
	tls  *tls.Conn
	// in and out are where the client reads replies and writes after login.
	in  io.Reader
	out io.Writer
}

func mssqlConnect(t *testing.T, conn net.Conn, enc byte) *mssqlFakeClient {
	t.Helper()
	fc := &mssqlFakeClient{conn: conn, in: conn, out: conn}
	pre := mssqlauth.Prelogin{
		{Token: mssqlauth.PreloginVersion, Data: []byte{0, 0, 0, 1, 0, 0}},
		{Token: mssqlauth.PreloginEncryption, Data: []byte{enc}},
		{Token: mssqlauth.PreloginInstOpt, Data: []byte{0}},
		{Token: mssqlauth.PreloginThreadID, Data: []byte{0, 0, 0, 0}},
		{Token: mssqlauth.PreloginMARS, Data: []byte{0}},
	}
	if err := mssqlauth.WritePacket(conn, mssqlauth.Packet{Type: mssqlauth.PacketPrelogin, Status: mssqlauth.StatusEOM, ID: 1, Payload: pre.Encode()}); err != nil {
		t.Fatal(err)
	}
	reply, err := mssqlauth.ReadPacket(conn)
	if err != nil {
		t.Fatalf("prelogin reply: %v", err)
	}
	got, err := mssqlauth.ParsePrelogin(reply.Payload)
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := got.Encryption()
	want := enc
	if enc == mssqlauth.EncryptReq {
		want = mssqlauth.EncryptOn
	}
	if answer != want {
		t.Fatalf("relay answered %s to %s", mssqlauth.EncryptionName(answer), mssqlauth.EncryptionName(enc))
	}
	if answer == mssqlauth.EncryptNotSup {
		return fc
	}
	framed := mssqlauth.NewFramedConn(conn)
	fc.tls = tls.Client(framed, &tls.Config{InsecureSkipVerify: true})
	if err := fc.tls.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	framed.Upgrade()
	if answer == mssqlauth.EncryptOn {
		fc.in, fc.out = fc.tls, fc.tls
	}
	return fc
}

// login sends one LOGIN7: through TLS when there is one, as the first
// (only) encrypted packet under ENCRYPT_OFF.
func (fc *mssqlFakeClient) login(t *testing.T, l *mssqlauth.Login) {
	t.Helper()
	w := fc.out
	if fc.tls != nil {
		w = fc.tls
	}
	if _, err := w.Write(mssqlauth.Frame(mssqlauth.PacketLogin7, mssqlauth.EncodeLogin(l), mssqlauth.DefaultPacketSize)); err != nil {
		t.Fatal(err)
	}
}

func (fc *mssqlFakeClient) response(t *testing.T) mssqlauth.LoginResponse {
	t.Helper()
	typ, payload, err := mssqlauth.ReadMessage(fc.in)
	if err != nil {
		t.Fatalf("login response: %v", err)
	}
	if typ != mssqlauth.PacketReply {
		t.Fatalf("login response packet %#x", typ)
	}
	r, err := mssqlauth.ParseLoginResponse(payload)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mssqlTestLogin(user, password string) *mssqlauth.Login {
	return &mssqlauth.Login{
		TDSVersion: 0x74000004, PacketSize: 4096,
		HostName: "laptop", UserName: user, Password: password,
		AppName: "sqlcmd", ServerName: "relay", CtlIntName: "go-mssqldb", Database: "app",
	}
}

// mssqlHarness wires a LoginConn to a fake client and a fake server over
// pipes and runs the driver in the background.
type mssqlHarness struct {
	t        *testing.T
	lc       *LoginConn
	client   net.Conn
	server   *mssqlFakeServer
	serveErr chan error
	fed      []inspectRecord
	mu       sync.Mutex
}

type inspectRecord struct {
	dir inspect.Direction
	pkt []byte
}

func newMSSQLHarness(t *testing.T, server *mssqlFakeServer) *mssqlHarness {
	t.Helper()
	clientSide, relaySide := net.Pipe()
	h := &mssqlHarness{t: t, client: clientSide, server: server, serveErr: make(chan error, 1)}
	h.lc = &LoginConn{
		Client: relaySide,
		Dial: func(ctx context.Context, cred *Credential) (net.Conn, error) {
			up, srv := net.Pipe()
			t.Cleanup(func() { up.Close(); srv.Close() })
			go func() { h.serveErr <- server.serve(srv) }()
			return up, nil
		},
		Lane: LaneInfo{Protocol: inspect.MSSQL, Name: "mssql", Network: "tcp", UpstreamAddr: "db.internal:1433"},
		Inspect: func(dir inspect.Direction, pkt []byte) error {
			h.mu.Lock()
			h.fed = append(h.fed, inspectRecord{dir, append([]byte(nil), pkt...)})
			h.mu.Unlock()
			return nil
		},
	}
	t.Cleanup(func() { clientSide.Close(); relaySide.Close() })
	return h
}

func (h *mssqlHarness) run() <-chan struct {
	res LoginResult
	err error
} {
	done := make(chan struct {
		res LoginResult
		err error
	}, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := loginMSSQL(ctx, h.lc)
		done <- struct {
			res LoginResult
			err error
		}{res, err}
	}()
	return done
}

func (h *mssqlHarness) wait(done <-chan struct {
	res LoginResult
	err error
}) (LoginResult, error) {
	h.t.Helper()
	select {
	case r := <-done:
		return r.res, r.err
	case <-time.After(10 * time.Second):
		h.t.Fatal("login did not finish")
		return LoginResult{}, nil
	}
}

func (h *mssqlHarness) fedPackets(dir inspect.Direction, typ byte) [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out [][]byte
	for _, r := range h.fed {
		if r.dir == dir && len(r.pkt) > 0 && r.pkt[0] == typ {
			out = append(out, r.pkt)
		}
	}
	return out
}

func mssqlAuthenticator(t *testing.T, wantUser, wantSecret string, cred Credential) func(context.Context, string, string) (session.Identity, Credential, error) {
	return func(_ context.Context, user, secret string) (session.Identity, Credential, error) {
		if user != wantUser || secret != wantSecret {
			return session.Identity{}, Credential{}, fmt.Errorf("%w: bad secret for %q", ErrLoginRefused, user)
		}
		return session.Identity{Subject: "alice@example.com", Email: "alice@example.com"}, cred, nil
	}
}

func TestMSSQLLoginSwapsTheCredentialUnderEncryptOff(t *testing.T) {
	server := &mssqlFakeServer{accept: func(u, p string) bool { return u == "svc_app" && p == "hunter2" }}
	h := newMSSQLHarness(t, server)
	h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{Name: "app", User: "svc_app", Password: "hunter2"})
	done := h.run()

	fc := mssqlConnect(t, h.client, mssqlauth.EncryptOff)
	fc.login(t, mssqlTestLogin("alice", "s3cret"))
	resp := fc.response(t)
	if !resp.LoginAck || resp.Err != nil {
		t.Fatalf("client saw %+v", resp)
	}

	res, err := h.wait(done)
	if err != nil {
		t.Fatal(err)
	}
	if res.Identity.Subject != "alice@example.com" || res.Credential.Name != "app" || res.Passthrough {
		t.Fatalf("result = %+v", res)
	}
	if _, ok := h.lc.Upstream.(*tls.Conn); !ok {
		t.Fatalf("upstream leg is %T, want the TLS conn", h.lc.Upstream)
	}
	if _, ok := h.lc.Client.(*tls.Conn); ok {
		t.Fatal("client leg wrapped in TLS under ENCRYPT_OFF")
	}

	server.mu.Lock()
	login, pre := server.login, server.prelogin
	server.mu.Unlock()
	if login.UserName != "svc_app" || login.Password != "hunter2" || login.Database != "app" || login.AppName != "sqlcmd" {
		t.Fatalf("upstream LOGIN7 = %+v", login)
	}
	if login.ServerName != "db.internal" {
		t.Fatalf("upstream ServerName = %q", login.ServerName)
	}
	if enc, _ := pre.Encryption(); enc != mssqlauth.EncryptOn {
		t.Fatalf("upstream PRELOGIN encryption = %s", mssqlauth.EncryptionName(enc))
	}

	// The session continues in clear on the client leg and in TLS upstream,
	// both positioned at the first post-login message.
	batch := mssqlauth.Packet{Type: mssqlauth.PacketSQLBatch, Status: mssqlauth.StatusEOM, ID: 1, Payload: ucs2("select 1")}.Encode()
	go fc.out.Write(batch)
	got, err := mssqlauth.ReadPacket(h.lc.Client)
	if err != nil || got.Type != mssqlauth.PacketSQLBatch {
		t.Fatalf("first post-login packet on the client leg: %+v, %v", got, err)
	}
	go h.lc.Upstream.Write(batch)
	server.mu.Lock()
	after := server.after
	server.mu.Unlock()
	up, err := mssqlauth.ReadPacket(after)
	if err != nil || !bytes.Equal(up.Payload, ucs2("select 1")) {
		t.Fatalf("first post-login packet upstream: %+v, %v", up, err)
	}

	// The gate saw the login: the swapped LOGIN7 and the response.
	logins := h.fedPackets(inspect.FromClient, mssqlauth.PacketLogin7)
	if len(logins) != 1 {
		t.Fatalf("LOGIN7 packets fed to the gate = %d", len(logins))
	}
	fed, _, _ := mssqlauth.DecodePacket(logins[0])
	if l, _ := mssqlauth.DecodeLogin(fed.Payload); l.UserName != "svc_app" {
		t.Fatalf("gate saw LOGIN7 for %q", l.UserName)
	}
	if len(h.fedPackets(inspect.FromServer, mssqlauth.PacketReply)) != 2 {
		t.Fatal("gate did not see the PRELOGIN reply and the login response")
	}
}

func TestMSSQLLoginKeepsTheClientLegEncryptedUnderEncryptOn(t *testing.T) {
	for _, enc := range []byte{mssqlauth.EncryptOn, mssqlauth.EncryptReq} {
		t.Run(mssqlauth.EncryptionName(enc), func(t *testing.T) {
			server := &mssqlFakeServer{accept: func(u, p string) bool { return u == "svc_app" }}
			h := newMSSQLHarness(t, server)
			h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{Name: "app", User: "svc_app", Password: "x"})
			done := h.run()

			fc := mssqlConnect(t, h.client, enc)
			fc.login(t, mssqlTestLogin("alice", "s3cret"))
			if resp := fc.response(t); !resp.LoginAck {
				t.Fatalf("client saw %+v", resp)
			}
			if _, err := h.wait(done); err != nil {
				t.Fatal(err)
			}
			if _, ok := h.lc.Client.(*tls.Conn); !ok {
				t.Fatalf("client leg is %T, want the TLS conn", h.lc.Client)
			}
			batch := mssqlauth.Packet{Type: mssqlauth.PacketSQLBatch, Status: mssqlauth.StatusEOM, ID: 1, Payload: ucs2("select 2")}.Encode()
			go fc.out.Write(batch)
			got, err := mssqlauth.ReadPacket(h.lc.Client)
			if err != nil || !bytes.Equal(got.Payload, ucs2("select 2")) {
				t.Fatalf("decrypted post-login packet: %+v, %v", got, err)
			}
		})
	}
}

func TestMSSQLLoginRefusesABadSecretWith18456(t *testing.T) {
	server := &mssqlFakeServer{accept: func(string, string) bool { t.Error("upstream login attempted"); return false }}
	h := newMSSQLHarness(t, server)
	h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{})
	done := h.run()

	fc := mssqlConnect(t, h.client, mssqlauth.EncryptOff)
	fc.login(t, mssqlTestLogin("alice", "wrong"))
	resp := fc.response(t)
	if resp.Err == nil || resp.Err.Number != mssqlauth.LoginFailedNumber || resp.Err.Class != 14 {
		t.Fatalf("client saw %+v", resp)
	}
	if !strings.Contains(resp.Err.Message, "'alice'") {
		t.Fatalf("message = %q", resp.Err.Message)
	}

	res, err := h.wait(done)
	if !errors.Is(err, ErrLoginRefused) {
		t.Fatalf("err = %v, want ErrLoginRefused", err)
	}
	if res.Identity.Subject != "alice" {
		t.Fatalf("refusal audited under %q", res.Identity.Subject)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("the secret leaked into the error: %v", err)
	}
}

func TestMSSQLLoginRefusesStrictClientsByName(t *testing.T) {
	h := newMSSQLHarness(t, &mssqlFakeServer{})
	h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{})
	done := h.run()

	// A TDS 8 client opens with a TLS ClientHello record.
	hello := []byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x01, 0x03}
	go h.client.Write(hello)
	_, err := h.wait(done)
	if !errors.Is(err, ErrLoginRefused) || !strings.Contains(err.Error(), "Encrypt=strict") {
		t.Fatalf("err = %v", err)
	}
}

func TestMSSQLLoginPassesAListedUserThroughUnchanged(t *testing.T) {
	server := &mssqlFakeServer{accept: func(u, p string) bool { return u == "alice" && p == "s3cret" }}
	h := newMSSQLHarness(t, server)
	h.lc.Authenticate = func(context.Context, string, string) (session.Identity, Credential, error) {
		t.Error("Authenticate called for a pass-through user")
		return session.Identity{}, Credential{}, ErrLoginRefused
	}
	h.lc.Passthrough = func(user string) bool { return user == "alice" }
	done := h.run()

	fc := mssqlConnect(t, h.client, mssqlauth.EncryptOff)
	fc.login(t, mssqlTestLogin("alice", "s3cret"))
	if resp := fc.response(t); !resp.LoginAck {
		t.Fatalf("client saw %+v", resp)
	}
	res, err := h.wait(done)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passthrough || res.Identity.Subject != "alice" || res.Credential.Name != "" {
		t.Fatalf("result = %+v", res)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.login.ServerName != "relay" || server.login.Password != "s3cret" {
		t.Fatalf("pass-through LOGIN7 changed: %+v", server.login)
	}
}

func TestMSSQLLoginIntegratedNeedsThePassthroughEntry(t *testing.T) {
	integrated := func() *mssqlauth.Login {
		l := mssqlTestLogin("", "")
		l.SetIntegrated([]byte("NTLMSSP-negotiate"))
		return l
	}

	t.Run("refused by default", func(t *testing.T) {
		h := newMSSQLHarness(t, &mssqlFakeServer{})
		h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{})
		done := h.run()
		fc := mssqlConnect(t, h.client, mssqlauth.EncryptOff)
		fc.login(t, integrated())
		if resp := fc.response(t); resp.Err == nil || resp.Err.Number != mssqlauth.LoginFailedNumber {
			t.Fatalf("client saw %+v", resp)
		}
		if _, err := h.wait(done); !errors.Is(err, ErrLoginRefused) || !strings.Contains(err.Error(), mssqlIntegratedUser) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("relayed when listed", func(t *testing.T) {
		server := &mssqlFakeServer{sspi: func(token []byte) ([]byte, bool) {
			switch string(token) {
			case "NTLMSSP-negotiate":
				return []byte("NTLMSSP-challenge"), true
			case "NTLMSSP-authenticate":
				return nil, true
			}
			return nil, false
		}}
		h := newMSSQLHarness(t, server)
		h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{})
		h.lc.Passthrough = func(user string) bool { return user == mssqlIntegratedUser }
		done := h.run()

		fc := mssqlConnect(t, h.client, mssqlauth.EncryptOff)
		fc.login(t, integrated())
		challenge := fc.response(t)
		if string(challenge.SSPI) != "NTLMSSP-challenge" || challenge.LoginAck {
			t.Fatalf("client saw %+v", challenge)
		}
		// The client's answer travels in clear after the encrypted LOGIN7.
		if _, err := fc.out.Write(mssqlauth.Frame(mssqlauth.PacketSSPI, []byte("NTLMSSP-authenticate"), 4096)); err != nil {
			t.Fatal(err)
		}
		final := fc.response(t)
		if !final.LoginAck || string(final.SSPI) != "server-final" {
			t.Fatalf("client saw %+v", final)
		}
		res, err := h.wait(done)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Passthrough || res.Identity.Subject != "" {
			t.Fatalf("result = %+v", res)
		}
	})
}

// fakeGSS is a GSSInitiator whose tokens name the step.
type fakeGSS struct {
	spn  string
	seen [][]byte
}

func (g *fakeGSS) Initiate(_ context.Context, spn string) (codectypes.GSSContext, error) {
	g.spn = spn
	return g, nil
}

func (g *fakeGSS) Step(in []byte) ([]byte, bool, error) {
	g.seen = append(g.seen, in)
	switch len(g.seen) {
	case 1:
		return []byte("krb-ap-req"), false, nil
	case 2:
		if string(in) != "krb-challenge" {
			return nil, false, fmt.Errorf("unexpected challenge %q", in)
		}
		return []byte("krb-answer"), false, nil
	}
	if string(in) != "server-final" {
		return nil, false, fmt.Errorf("unexpected final token %q", in)
	}
	return nil, true, nil
}

func TestMSSQLLoginDrivesSSPIUpstreamForAKerberosCredential(t *testing.T) {
	server := &mssqlFakeServer{sspi: func(token []byte) ([]byte, bool) {
		switch string(token) {
		case "krb-ap-req":
			return []byte("krb-challenge"), true
		case "krb-answer":
			return nil, true
		}
		return nil, false
	}}
	h := newMSSQLHarness(t, server)
	gss := &fakeGSS{}
	h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{Name: "krb", User: "svc@EXAMPLE", GSS: gss})
	done := h.run()

	fc := mssqlConnect(t, h.client, mssqlauth.EncryptOff)
	fc.login(t, mssqlTestLogin("alice", "s3cret"))
	resp := fc.response(t)
	if !resp.LoginAck || resp.Err != nil {
		t.Fatalf("client saw %+v", resp)
	}
	if resp.SSPI != nil {
		t.Fatalf("the server's final GSS token reached a SQL-auth client: %q", resp.SSPI)
	}
	if _, err := h.wait(done); err != nil {
		t.Fatal(err)
	}
	if gss.spn != "MSSQLSvc/db.internal:1433" {
		t.Fatalf("spn = %q", gss.spn)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if !server.login.Integrated() || server.login.UserName != "" || len(server.sspiSteps) != 2 {
		t.Fatalf("upstream login = %+v, steps = %q", server.login, server.sspiSteps)
	}
	// The SSPI continuation went through the gate like every login packet.
	if len(h.fedPackets(inspect.FromClient, mssqlauth.PacketSSPI)) != 1 {
		t.Fatal("SSPI packet not fed to the gate")
	}
}

func TestMSSQLLoginForwardsAnUpstreamRefusal(t *testing.T) {
	server := &mssqlFakeServer{accept: func(string, string) bool { return false }}
	h := newMSSQLHarness(t, server)
	h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{Name: "app", User: "svc_app", Password: "stale"})
	done := h.run()

	fc := mssqlConnect(t, h.client, mssqlauth.EncryptOff)
	fc.login(t, mssqlTestLogin("alice", "s3cret"))
	resp := fc.response(t)
	if resp.Err == nil || resp.Err.Number != mssqlauth.LoginFailedNumber || !strings.Contains(resp.Err.Message, "svc_app") {
		t.Fatalf("client saw %+v", resp)
	}
	_, err := h.wait(done)
	if err == nil || errors.Is(err, ErrLoginRefused) {
		t.Fatalf("err = %v, want an upstream failure that is not a client refusal", err)
	}
}

func TestMSSQLLoginRefusesAnUpstreamWithoutEncryption(t *testing.T) {
	h := newMSSQLHarness(t, &mssqlFakeServer{})
	h.lc.Dial = func(context.Context, *Credential) (net.Conn, error) {
		up, srv := net.Pipe()
		go func() {
			defer srv.Close()
			if _, err := mssqlauth.ReadPacket(srv); err != nil {
				return
			}
			_ = mssqlauth.WritePacket(srv, mssqlauth.Packet{Type: mssqlauth.PacketReply, Status: mssqlauth.StatusEOM,
				Payload: mssqlauth.ServerPrelogin(mssqlauth.EncryptNotSup).Encode()})
		}()
		return up, nil
	}
	h.lc.Authenticate = mssqlAuthenticator(t, "alice", "s3cret", Credential{})
	done := h.run()
	go mssqlauth.WritePacket(h.client, mssqlauth.Packet{Type: mssqlauth.PacketPrelogin, Status: mssqlauth.StatusEOM,
		Payload: mssqlauth.ServerPrelogin(mssqlauth.EncryptOff).Encode()})
	_, err := h.wait(done)
	if err == nil || !strings.Contains(err.Error(), "does not support encryption") {
		t.Fatalf("err = %v", err)
	}
}

// FuzzMSSQLLogin feeds the PRELOGIN and LOGIN7 decoders the client-controlled
// bytes the driver reads before anything is verified.
func FuzzMSSQLLogin(f *testing.F) {
	f.Add(mssqlauth.EncodeLogin(mssqlTestLogin("alice", "s3cret")))
	l := mssqlTestLogin("", "")
	l.SetIntegrated([]byte("NTLMSSP"))
	l.FeatureExt = []byte{0x04, 0x01, 0, 0, 0, 0x01, 0xff}
	f.Add(mssqlauth.EncodeLogin(l))
	f.Add(mssqlauth.ServerPrelogin(mssqlauth.EncryptOff).Encode())
	f.Add([]byte{0xff})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if p, err := mssqlauth.ParsePrelogin(data); err == nil {
			p.SetEncryption(mssqlauth.EncryptOn)
			if _, err := mssqlauth.ParsePrelogin(p.Encode()); err != nil {
				t.Fatalf("rewritten PRELOGIN does not parse: %v", err)
			}
		}
		if l, err := mssqlauth.DecodeLogin(data); err == nil {
			l.SetSQLAuth("svc", "pw")
			l.DisablePasswordChange()
			back, err := mssqlauth.DecodeLogin(mssqlauth.EncodeLogin(l))
			if err != nil {
				t.Fatalf("swapped LOGIN7 does not decode: %v", err)
			}
			if back.UserName != "svc" || back.Password != "pw" || back.Integrated() {
				t.Fatalf("swap lost: %+v", back)
			}
		}
	})
}
