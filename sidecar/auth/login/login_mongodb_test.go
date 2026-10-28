package login

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	mongoauth "github.com/hoophq/libhoop/v2/codec/mongodb/auth"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// Minimal BSON writers for the test's documents. This module imports no
// BSON library; the driver under test reads documents through libhoop.

func bdoc(elems ...[]byte) []byte {
	n := 5
	for _, e := range elems {
		n += len(e)
	}
	out := make([]byte, 4, n)
	binary.LittleEndian.PutUint32(out, uint32(n))
	for _, e := range elems {
		out = append(out, e...)
	}
	return append(out, 0)
}

func belem(typ byte, key string, value []byte) []byte {
	out := make([]byte, 0, 2+len(key)+len(value))
	out = append(out, typ)
	out = append(out, key...)
	out = append(out, 0)
	return append(out, value...)
}

func bint32(key string, v int32) []byte {
	return belem(0x10, key, binary.LittleEndian.AppendUint32(nil, uint32(v)))
}

func bdouble(key string, v float64) []byte {
	return belem(0x01, key, binary.LittleEndian.AppendUint64(nil, math.Float64bits(v)))
}

func bstr(key, v string) []byte {
	value := binary.LittleEndian.AppendUint32(nil, uint32(len(v)+1))
	value = append(value, v...)
	return belem(0x02, key, append(value, 0))
}

func bbool(key string, v bool) []byte {
	if v {
		return belem(0x08, key, []byte{1})
	}
	return belem(0x08, key, []byte{0})
}

func bbin(key string, v []byte) []byte {
	value := binary.LittleEndian.AppendUint32(nil, uint32(len(v)))
	value = append(value, 0)
	return belem(0x05, key, append(value, v...))
}

func bsub(key string, doc []byte) []byte { return belem(0x03, key, doc) }

func barr(key string, items ...string) []byte {
	elems := make([][]byte, len(items))
	for i, item := range items {
		elems[i] = bstr(fmt.Sprint(i), item)
	}
	return belem(0x04, key, bdoc(elems...))
}

// mongoFakeServer answers hello, SCRAM-SHA-256 and MONGODB-X509 like a
// mongod with the given users.
type mongoFakeServer struct {
	users    map[string]string   // user -> password
	mechs    map[string][]string // saslSupportedMechs per user; default SCRAM-SHA-256
	x509User string              // authenticate MONGODB-X509 succeeds as this user when set
	// ignoreSkip makes the server run the empty final exchange even when
	// the client asked skipEmptyExchange, the pre-4.4 shape.
	ignoreSkip bool

	mu            sync.Mutex
	hellos        []mongoauth.Hello
	authenticated []string
	err           error
}

func (s *mongoFakeServer) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *mongoFakeServer) state() ([]mongoauth.Hello, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mongoauth.Hello(nil), s.hellos...), append([]string(nil), s.authenticated...), s.err
}

func (s *mongoFakeServer) material(user string) (codectypes.SCRAMMaterial, bool) {
	password, ok := s.users[user]
	if !ok {
		return codectypes.SCRAMMaterial{}, false
	}
	m, err := mongoauth.Material(mongoauth.ScramSHA256, user, password, []byte("upstreamsalt0000"), 4096)
	if err != nil {
		panic(err)
	}
	return m, true
}

func (s *mongoFakeServer) helloReply(id int32, extra ...[]byte) []byte {
	elems := [][]byte{
		bbool("helloOk", true),
		bbool("isWritablePrimary", true),
		bint32("maxWireVersion", 21),
		bint32("minWireVersion", 0),
		bint32("maxMessageSizeBytes", 48000000),
		bint32("connectionId", id),
	}
	elems = append(elems, extra...)
	return bdoc(append(elems, bdouble("ok", 1))...)
}

func (s *mongoFakeServer) serve(conn net.Conn) {
	defer conn.Close()
	var (
		conv     *mongoauth.ServerConversation
		convUser string
		skip     bool
		proven   bool
		replyID  int32 = 1000
	)
	for {
		msg, err := mongoauth.ReadMessage(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				s.fail(fmt.Errorf("server read: %w", err))
			}
			return
		}
		replyID++
		reply := func(doc []byte) bool {
			_, err := conn.Write(mongoauth.ReplyTo(msg, replyID, doc))
			return err == nil
		}
		startConv := func(start mongoauth.SASLStart) ([]byte, bool) {
			user, err := mongoauth.ClientFirstUser(start.Payload)
			if err != nil || start.Mechanism != mongoauth.ScramSHA256 {
				return nil, false
			}
			conv, err = mongoauth.NewServerConversation(mongoauth.ScramSHA256, s.material)
			if err != nil {
				panic(err)
			}
			first, err := conv.Step(start.Payload)
			if err != nil {
				conv = nil
				return nil, false
			}
			convUser, skip = user, start.SkipEmptyExchange && !s.ignoreSkip
			return first, true
		}
		switch cmd := msg.Command(); {
		case mongoauth.IsHello(cmd):
			hello, err := mongoauth.DecodeHello(msg.Body)
			if err != nil {
				s.fail(err)
				return
			}
			s.mu.Lock()
			s.hellos = append(s.hellos, hello)
			s.mu.Unlock()
			var extra [][]byte
			if hello.MechsUser != "" {
				_, user, _ := strings.Cut(hello.MechsUser, ".")
				mechs := s.mechs[user]
				if mechs == nil {
					mechs = []string{mongoauth.ScramSHA256}
				}
				extra = append(extra, barr("saslSupportedMechs", mechs...))
			}
			if hello.Speculative != nil {
				if first, ok := startConv(*hello.Speculative); ok {
					extra = append(extra, bsub("speculativeAuthenticate", mongoauth.SpeculativeReply(1, false, first)))
				}
			}
			if !reply(s.helloReply(replyID, extra...)) {
				return
			}
		case cmd == "saslStart":
			start, err := mongoauth.DecodeSASLStart(msg.Body)
			if err != nil {
				s.fail(err)
				return
			}
			first, ok := startConv(start)
			if !ok {
				if !reply(mongoauth.AuthenticationFailed()) {
					return
				}
				continue
			}
			if !reply(mongoauth.SASLReply(1, false, first)) {
				return
			}
		case cmd == "saslContinue":
			_, payload, err := mongoauth.DecodeSASLContinue(msg.Body)
			if err != nil || conv == nil {
				s.fail(fmt.Errorf("saslContinue without a conversation: %v", err))
				return
			}
			if proven {
				if len(payload) != 0 {
					s.fail(errors.New("final exchange carried a payload"))
					return
				}
				conv, proven = nil, false
				if !reply(mongoauth.SASLReply(1, true, nil)) {
					return
				}
				continue
			}
			final, err := conv.Step(payload)
			if err != nil || !conv.Valid() {
				conv = nil
				if !reply(mongoauth.AuthenticationFailed()) {
					return
				}
				continue
			}
			s.mu.Lock()
			s.authenticated = append(s.authenticated, convUser)
			s.mu.Unlock()
			if skip {
				conv = nil
			} else {
				proven = true
			}
			if !reply(mongoauth.SASLReply(1, skip, final)) {
				return
			}
		case cmd == "authenticate":
			if s.x509User == "" || msg.Database() != "$external" {
				if !reply(mongoauth.AuthenticationFailed()) {
					return
				}
				continue
			}
			s.mu.Lock()
			s.authenticated = append(s.authenticated, s.x509User)
			s.mu.Unlock()
			if !reply(bdoc(bstr("user", s.x509User), bdouble("ok", 1))) {
				return
			}
		case cmd == "ping":
			if !reply(bdoc(bdouble("ok", 1))) {
				return
			}
		default:
			if !reply(mongoauth.Unauthorized(cmd)) {
				return
			}
		}
	}
}

// mongoLoginHarness wires a proxy.LoginConn to a fake client and a fake upstream.
type mongoLoginHarness struct {
	t        *testing.T
	client   net.Conn
	server   *mongoFakeServer
	conn     *proxy.LoginConn
	dialed   []*proxy.Credential
	inspects []struct {
		dir inspect.Direction
		pkt []byte
	}
	authCalls []string
	mu        sync.Mutex
}

// mongoSecret is what alice presents on the PLAIN client leg.
const mongoSecret = "alice-token"

func newMongoLoginHarness(t *testing.T, server *mongoFakeServer) *mongoLoginHarness {
	t.Helper()
	clientEnd, relayEnd := net.Pipe()
	h := &mongoLoginHarness{t: t, client: clientEnd, server: server}
	cred := proxy.Credential{Name: "svc-rw", User: "svc", Password: "svc-pw"}
	h.conn = &proxy.LoginConn{
		Client: relayEnd,
		Lane:   proxy.LaneInfo{Protocol: inspect.MongoDB, Name: "mongo", Network: "tcp", ClientTLS: true, UpstreamAddr: "db.internal:27017"},
		Dial: func(_ context.Context, cred *proxy.Credential) (net.Conn, error) {
			h.mu.Lock()
			h.dialed = append(h.dialed, cred)
			h.mu.Unlock()
			up, down := net.Pipe()
			go server.serve(down)
			return up, nil
		},
		Passthrough: func(user string) bool { return user == "legacy" },
		Authenticate: func(_ context.Context, user, secret string) (session.Identity, proxy.Credential, error) {
			h.mu.Lock()
			h.authCalls = append(h.authCalls, user+"/"+secret)
			h.mu.Unlock()
			if user != "alice" || secret != mongoSecret {
				return session.Identity{}, proxy.Credential{}, fmt.Errorf("%w: bad secret", proxy.ErrLoginRefused)
			}
			return session.Identity{Subject: "alice@example.com", Email: "alice@example.com"}, cred, nil
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
	t.Cleanup(func() {
		clientEnd.Close()
		relayEnd.Close()
		if h.conn.Upstream != nil {
			h.conn.Upstream.Close()
		}
	})
	return h
}

type mongoLoginOutcome struct {
	res proxy.LoginResult
	err error
}

func (h *mongoLoginHarness) run() <-chan mongoLoginOutcome {
	out := make(chan mongoLoginOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := loginMongoDB(ctx, h.conn)
		out <- mongoLoginOutcome{res, err}
	}()
	return out
}

func (h *mongoLoginHarness) wait(done <-chan mongoLoginOutcome) (proxy.LoginResult, error) {
	h.t.Helper()
	select {
	case r := <-done:
		return r.res, r.err
	case <-time.After(5 * time.Second):
		h.t.Fatal("login did not finish")
		return proxy.LoginResult{}, nil
	}
}

func (h *mongoLoginHarness) write(raw []byte) {
	h.t.Helper()
	h.client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := h.client.Write(raw); err != nil {
		h.t.Fatalf("client write: %v", err)
	}
}

func (h *mongoLoginHarness) read() mongoauth.Message {
	h.t.Helper()
	h.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	msg, err := mongoauth.ReadMessage(h.client)
	if err != nil {
		h.t.Fatalf("client read: %v", err)
	}
	return msg
}

// expectError reads a reply and checks it is a command error with code.
func (h *mongoLoginHarness) expectError(responseTo, code int32) string {
	h.t.Helper()
	msg := h.read()
	if msg.ResponseTo != responseTo {
		h.t.Fatalf("responseTo %d, want %d", msg.ResponseTo, responseTo)
	}
	resp, err := mongoauth.DecodeSASLResponse(msg.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	if resp.OK || resp.Code != code {
		h.t.Fatalf("reply %+v, want error code %d", resp, code)
	}
	return resp.ErrMsg
}

func (h *mongoLoginHarness) inspected() []struct {
	dir inspect.Direction
	pkt []byte
} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]struct {
		dir inspect.Direction
		pkt []byte
	}(nil), h.inspects...)
}

// expectBalancedInspects checks every client message the driver read went
// to the gate and was followed by the reply the client got, so the duplex
// codec's correlation map ends empty.
func (h *mongoLoginHarness) expectBalancedInspects(pairs int) {
	h.t.Helper()
	got := h.inspected()
	if len(got) != 2*pairs {
		h.t.Fatalf("%d inspected packets, want %d", len(got), 2*pairs)
	}
	for i := 0; i < len(got); i += 2 {
		req, err := mongoauth.Parse(got[i].pkt)
		if err != nil || got[i].dir != inspect.FromClient {
			h.t.Fatalf("inspect %d: %v %v, want a client message", i, got[i].dir, err)
		}
		rep, err := mongoauth.Parse(got[i+1].pkt)
		if err != nil || got[i+1].dir != inspect.FromServer {
			h.t.Fatalf("inspect %d: %v %v, want a server message", i+1, got[i+1].dir, err)
		}
		if rep.ResponseTo != req.RequestID {
			h.t.Fatalf("inspect %d answers %d, want %d", i+1, rep.ResponseTo, req.RequestID)
		}
	}
}

// scramClient is the test's SCRAM-SHA-256 client side.
type scramClient struct {
	conv interface {
		Step(string) (string, error)
		Done() bool
	}
}

func newScramClient(t *testing.T, user, password string) *scramClient {
	t.Helper()
	client, err := mongoauth.NewScramClient(mongoauth.ScramSHA256, user, password)
	if err != nil {
		t.Fatal(err)
	}
	return &scramClient{conv: client.NewConversation()}
}

func (c *scramClient) first() []byte {
	out, _ := c.conv.Step("")
	return []byte(out)
}

func (c *scramClient) step(t *testing.T, in []byte) []byte {
	t.Helper()
	out, err := c.conv.Step(string(in))
	if err != nil {
		t.Fatalf("scram client: %v", err)
	}
	return []byte(out)
}

func speculativeHello(payload []byte, skip bool, extra ...[]byte) []byte {
	elems := [][]byte{
		bint32("isMaster", 1),
		bbool("helloOk", true),
		bsub("speculativeAuthenticate", bdoc(
			bint32("saslStart", 1),
			bstr("mechanism", mongoauth.ScramSHA256),
			bbin("payload", payload),
			bstr("db", "admin"),
			bsub("options", bdoc(bbool("skipEmptyExchange", skip))),
		)),
		barr("compression", "zstd"),
		bsub("client", bdoc(bsub("driver", bdoc(bstr("name", "test"), bstr("version", "1"))))),
	}
	return bdoc(append(elems, extra...)...)
}

func plainHello() []byte {
	return bdoc(bint32("hello", 1), bbool("helloOk", true), bstr("$db", "admin"))
}

func saslStartDoc(payload []byte, skip bool) []byte {
	return bdoc(
		bint32("saslStart", 1),
		bstr("mechanism", mongoauth.ScramSHA256),
		bbin("payload", payload),
		bsub("options", bdoc(bbool("skipEmptyExchange", skip))),
		bstr("$db", "admin"),
	)
}

func saslContinueDoc(cid int32, payload []byte) []byte {
	return bdoc(
		bint32("saslContinue", 1),
		bint32("conversationId", cid),
		bbin("payload", payload),
		bstr("$db", "admin"),
	)
}

// plainStartDoc is the saslStart a driver sends with authMechanism=PLAIN:
// the user and the secret in one RFC 4616 payload, on $external.
func plainStartDoc(user, secret string) []byte {
	return plainStartPayload(append(append(append([]byte{0}, user...), 0), secret...))
}

func plainStartPayload(payload []byte) []byte {
	return bdoc(
		bint32("saslStart", 1),
		bstr("mechanism", "PLAIN"),
		bbin("payload", payload),
		bint32("autoAuthorize", 1),
		bstr("$db", "$external"),
	)
}

func TestMongoLoginPlainSwapsCredential(t *testing.T) {
	server := &mongoFakeServer{users: map[string]string{"svc": "svc-pw"}}
	h := newMongoLoginHarness(t, server)
	done := h.run()

	hello := bdoc(
		bint32("isMaster", 1),
		bbool("helloOk", true),
		bstr("saslSupportedMechs", "$external.alice"),
		barr("compression", "zstd"),
		bsub("client", bdoc(bsub("driver", bdoc(bstr("name", "test"), bstr("version", "1"))))),
	)
	h.write(mongoauth.EncodeOpQuery(11, "admin.$cmd", hello))

	reply := h.read()
	if reply.OpCode != mongoauth.OpReply || reply.ResponseTo != 11 {
		t.Fatalf("hello answered with opcode %d to %d", reply.OpCode, reply.ResponseTo)
	}
	if v, err := reply.Body.LookupErr("maxWireVersion"); err != nil || v.Int32() != 21 {
		t.Fatalf("maxWireVersion not copied from upstream: %v", reply.Body)
	}
	if mechs := mongoauth.SupportedMechs(reply.Body); len(mechs) != 1 || mechs[0] != "PLAIN" {
		t.Fatalf("saslSupportedMechs = %v", mechs)
	}
	if _, err := reply.Body.LookupErr("compression"); err == nil {
		t.Fatal("hello reply agrees to compression")
	}

	h.write(mongoauth.EncodeOpMsg(12, 0, plainStartDoc("alice", mongoSecret)))
	final := h.read()
	if final.OpCode != mongoauth.OpMsg || final.ResponseTo != 12 {
		t.Fatalf("saslStart answered with opcode %d to %d", final.OpCode, final.ResponseTo)
	}
	resp, err := mongoauth.DecodeSASLResponse(final.Body)
	if err != nil || !resp.OK || !resp.Done || resp.ConversationID != 1 {
		t.Fatalf("saslStart reply %+v, %v", resp, err)
	}

	res, err := h.wait(done)
	if err != nil {
		t.Fatal(err)
	}
	if res.Passthrough || res.Identity.Subject != "alice@example.com" || res.Credential.Name != "svc-rw" {
		t.Fatalf("result %+v", res)
	}
	if len(h.authCalls) != 1 || h.authCalls[0] != "alice/"+mongoSecret {
		t.Fatalf("authenticate called with %v", h.authCalls)
	}
	hellos, authenticated, serr := server.state()
	if serr != nil {
		t.Fatal(serr)
	}
	if len(authenticated) != 1 || authenticated[0] != "svc" {
		t.Fatalf("upstream authenticated %v, want svc", authenticated)
	}
	if len(hellos) != 1 || hellos[0].Speculative != nil || hellos[0].MechsUser != "" {
		t.Fatalf("upstream hello %+v carried the client's authentication", hellos)
	}
	if _, err := hellos[0].Doc.LookupErr("compression"); err == nil {
		t.Fatal("upstream hello offered compression")
	}
	if _, err := hellos[0].Doc.LookupErr("client"); err != nil {
		t.Fatal("upstream hello lost the client metadata")
	}
	if len(h.dialed) != 1 || h.dialed[0] != nil {
		t.Fatalf("dialed %v, want once without a credential", h.dialed)
	}
	// The secret went to the verifier and nowhere else: not upstream, not
	// into the gate.
	for _, in := range h.inspected() {
		if in.dir == inspect.FromServer && bytes.Contains(in.pkt, []byte(mongoSecret)) {
			t.Fatal("the secret reached a reply")
		}
	}
	h.expectBalancedInspects(2)
}

func TestMongoLoginWrongSecretIsRefused(t *testing.T) {
	server := &mongoFakeServer{users: map[string]string{"svc": "svc-pw"}}
	h := newMongoLoginHarness(t, server)
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(21, 0, plainHello()))
	h.read()
	h.write(mongoauth.EncodeOpMsg(22, 0, plainStartDoc("alice", "not-the-token")))
	msg := h.expectError(22, mongoauth.CodeAuthenticationFailed)
	if strings.Contains(msg, "secret") || strings.Contains(msg, "token") {
		t.Fatalf("refusal leaks the reason: %q", msg)
	}

	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) || res.Identity.Subject != "alice" {
		t.Fatalf("result %+v, %v", res, err)
	}
	if len(h.authCalls) != 1 {
		t.Fatalf("authenticate calls %v, want one", h.authCalls)
	}
	if _, authenticated, _ := server.state(); len(authenticated) != 0 {
		t.Fatalf("upstream authenticated %v on a refused login", authenticated)
	}
	h.expectBalancedInspects(2)
}

// A driver that speculates SCRAM for a user the lane terminates is told
// which mechanism to use, before any upstream is dialed.
func TestMongoLoginSpeculativeScramIsRefusedWithoutDialing(t *testing.T) {
	h := newMongoLoginHarness(t, &mongoFakeServer{})
	done := h.run()

	sc := newScramClient(t, "alice", "x")
	h.write(mongoauth.EncodeOpQuery(31, "admin.$cmd", speculativeHello(sc.first(), true)))
	msg := h.expectError(31, mongoauth.CodeAuthenticationFailed)
	if !strings.Contains(msg, mongoauth.ScramSHA256) || !strings.Contains(msg, "PLAIN") {
		t.Fatalf("errmsg %q does not name the mechanisms", msg)
	}

	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) || res.Identity.Subject != "alice" {
		t.Fatalf("result %+v, %v", res, err)
	}
	if len(h.dialed) != 0 || len(h.authCalls) != 0 {
		t.Fatalf("dialed %d, authenticated %v for a refused mechanism", len(h.dialed), h.authCalls)
	}
}

// A saslStart before any hello gets the relay's own hello upstream, and an
// upstream that runs the pre-4.4 three-step SCRAM is followed.
func TestMongoLoginSaslStartBeforeHello(t *testing.T) {
	server := &mongoFakeServer{users: map[string]string{"svc": "svc-pw"}, ignoreSkip: true}
	h := newMongoLoginHarness(t, server)
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(41, 0, plainStartDoc("alice", mongoSecret)))
	resp, err := mongoauth.DecodeSASLResponse(h.read().Body)
	if err != nil || !resp.OK || !resp.Done {
		t.Fatalf("saslStart reply %+v, %v", resp, err)
	}

	res, err := h.wait(done)
	if err != nil || res.Identity.Subject != "alice@example.com" {
		t.Fatalf("result %+v, %v", res, err)
	}
	hellos, authenticated, serr := server.state()
	if serr != nil || len(hellos) != 1 || len(authenticated) != 1 || authenticated[0] != "svc" {
		t.Fatalf("upstream hellos=%d authenticated=%v err=%v", len(hellos), authenticated, serr)
	}
	h.expectBalancedInspects(1)
}

// The secret travels in clear, so a leg a third party could read is
// refused before the verifier sees it.
func TestMongoLoginPlainTCPIsRefused(t *testing.T) {
	h := newMongoLoginHarness(t, &mongoFakeServer{})
	h.conn.Lane.ClientTLS = false
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(51, 0, plainHello()))
	h.read()
	h.write(mongoauth.EncodeOpMsg(52, 0, plainStartDoc("alice", mongoSecret)))
	h.expectError(52, mongoauth.CodeAuthenticationFailed)
	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) || res.Identity.Subject != "alice" || len(h.authCalls) != 0 {
		t.Fatalf("result %+v, %v, authenticated %v", res, err, h.authCalls)
	}
}

func TestMongoLoginMalformedPlainIsRefused(t *testing.T) {
	for name, payload := range map[string][]byte{
		"no separators":   []byte("alice"),
		"empty user":      {0, 0, 's'},
		"two fields":      []byte("alice\x00secret"),
		"authzid differs": []byte("bob\x00alice\x00secret"),
	} {
		t.Run(name, func(t *testing.T) {
			h := newMongoLoginHarness(t, &mongoFakeServer{})
			done := h.run()
			h.write(mongoauth.EncodeOpMsg(61, 0, plainHello()))
			h.read()
			h.write(mongoauth.EncodeOpMsg(62, 0, plainStartPayload(payload)))
			h.expectError(62, mongoauth.CodeAuthenticationFailed)
			if _, err := h.wait(done); !errors.Is(err, proxy.ErrLoginRefused) || len(h.authCalls) != 0 {
				t.Fatalf("err = %v, authenticated %v", err, h.authCalls)
			}
		})
	}
}

func TestMongoLoginPassthroughSpeculativeRelaysUpstreamConversation(t *testing.T) {
	server := &mongoFakeServer{
		users: map[string]string{"legacy": "legacy-pw"},
		mechs: map[string][]string{"legacy": {mongoauth.ScramSHA1, mongoauth.ScramSHA256}},
	}
	h := newMongoLoginHarness(t, server)
	done := h.run()

	sc := newScramClient(t, "legacy", "legacy-pw")
	h.write(mongoauth.EncodeOpQuery(51, "admin.$cmd", speculativeHello(sc.first(), true, bstr("saslSupportedMechs", "admin.legacy"))))
	reply := h.read()
	if mechs := mongoauth.SupportedMechs(reply.Body); len(mechs) != 2 || mechs[0] != mongoauth.ScramSHA1 {
		t.Fatalf("saslSupportedMechs = %v, want the upstream's", mechs)
	}
	spec, err := reply.Body.LookupErr("speculativeAuthenticate")
	if err != nil {
		t.Fatalf("no speculativeAuthenticate in %v", reply.Body)
	}
	resp, _ := mongoauth.DecodeSpeculativeResponse(spec.Document())

	res, err := h.wait(done)
	if err != nil || !res.Passthrough || res.Identity.Subject != "legacy" {
		t.Fatalf("result %+v, %v", res, err)
	}
	if len(h.authCalls) != 0 {
		t.Fatalf("authenticate called for a pass-through user: %v", h.authCalls)
	}
	hellos, _, _ := server.state()
	if len(hellos) != 1 || hellos[0].MechsUser != "admin.legacy" {
		t.Fatalf("upstream hello %+v, want the mechs question forwarded", hellos)
	}

	// The pumps take over here: the client's final message goes upstream
	// unchanged and the upstream, not the relay, judges the proof.
	up := h.conn.Upstream
	up.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := up.Write(mongoauth.EncodeOpMsg(52, 0, saslContinueDoc(resp.ConversationID, sc.step(t, resp.Payload)))); err != nil {
		t.Fatal(err)
	}
	final, err := mongoauth.ReadMessage(up)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = mongoauth.DecodeSASLResponse(final.Body)
	if err != nil || !resp.OK || !resp.Done {
		t.Fatalf("upstream final %+v, %v", resp, err)
	}
	sc.step(t, resp.Payload)
	if _, authenticated, _ := server.state(); len(authenticated) != 1 || authenticated[0] != "legacy" {
		t.Fatalf("upstream authenticated %v", authenticated)
	}
	h.expectBalancedInspects(1)
}

func TestMongoLoginPlainHelloForwardsMechsQuestionForPassthroughUser(t *testing.T) {
	server := &mongoFakeServer{
		users: map[string]string{"legacy": "legacy-pw"},
		mechs: map[string][]string{"legacy": {mongoauth.ScramSHA1}},
	}
	h := newMongoLoginHarness(t, server)
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(63, 0, bdoc(bint32("hello", 1), bstr("saslSupportedMechs", "admin.legacy"), bstr("$db", "admin"))))
	if mechs := mongoauth.SupportedMechs(h.read().Body); len(mechs) != 1 || mechs[0] != mongoauth.ScramSHA1 {
		t.Fatalf("saslSupportedMechs = %v, want the upstream's answer", mechs)
	}
	h.client.Close()
	h.wait(done)
	hellos, _, _ := server.state()
	if len(hellos) != 1 || hellos[0].MechsUser != "admin.legacy" {
		t.Fatalf("upstream hello %+v, want the question forwarded", hellos)
	}
}

func TestMongoLoginPassthroughSaslStartForwardsUnchanged(t *testing.T) {
	server := &mongoFakeServer{users: map[string]string{"legacy": "legacy-pw"}}
	h := newMongoLoginHarness(t, server)
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(61, 0, plainHello()))
	h.read()
	sc := newScramClient(t, "legacy", "legacy-pw")
	start := mongoauth.EncodeOpMsg(62, 0, saslStartDoc(sc.first(), true))
	h.write(start)
	reply := h.read()
	if reply.ResponseTo != 62 {
		t.Fatalf("saslStart answered to %d", reply.ResponseTo)
	}
	resp, err := mongoauth.DecodeSASLResponse(reply.Body)
	if err != nil || !resp.OK {
		t.Fatalf("saslStart reply %+v, %v", resp, err)
	}

	res, err := h.wait(done)
	if err != nil || !res.Passthrough || res.Identity.Subject != "legacy" {
		t.Fatalf("result %+v, %v", res, err)
	}
	got := h.inspected()
	if len(got) != 4 || !bytes.Equal(got[2].pkt, start) || !bytes.Equal(got[3].pkt, reply.Raw) {
		t.Fatalf("inspected %d packets; saslStart and its reply must pass unchanged", len(got))
	}
	h.expectBalancedInspects(2)
}

func TestMongoLoginMonitorConnectionCarriesNoIdentity(t *testing.T) {
	server := &mongoFakeServer{}
	h := newMongoLoginHarness(t, server)
	done := h.run()

	h.write(mongoauth.EncodeOpQuery(71, "admin.$cmd", bdoc(bint32("isMaster", 1), bbool("helloOk", true))))
	h.read()
	second := mongoauth.EncodeOpMsg(72, 0, bdoc(bint32("hello", 1), bint32("maxAwaitTimeMS", 10000), bstr("$db", "admin")))
	h.write(second)

	res, err := h.wait(done)
	if err != nil || !res.Passthrough || !res.Identity.IsAnonymous() {
		t.Fatalf("result %+v, %v", res, err)
	}
	// The second hello went upstream; its reply is the pumps' to relay.
	up := h.conn.Upstream
	up.SetDeadline(time.Now().Add(5 * time.Second))
	reply, err := mongoauth.ReadMessage(up)
	if err != nil || reply.ResponseTo != 72 {
		t.Fatalf("upstream reply %+v, %v", reply, err)
	}
	got := h.inspected()
	if len(got) != 3 || got[2].dir != inspect.FromClient || !bytes.Equal(got[2].pkt, second) {
		t.Fatalf("inspected %d packets; the forwarded hello must be the last", len(got))
	}
}

func TestMongoLoginCommandBeforeLoginIsUnauthorized(t *testing.T) {
	h := newMongoLoginHarness(t, &mongoFakeServer{})
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(81, 0, plainHello()))
	h.read()
	h.write(mongoauth.EncodeOpMsg(82, 0, bdoc(bstr("find", "users"), bstr("$db", "app"))))
	if msg := h.expectError(82, mongoauth.CodeUnauthorized); !strings.Contains(msg, "find") {
		t.Fatalf("errmsg %q", msg)
	}
	if _, err := h.wait(done); !errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err = %v", err)
	}
	// The gate never saw the command: judged as a statement, it would fix
	// the session's facts before the refusal is recorded under them.
	h.expectBalancedInspects(1)
}

func TestMongoLoginOtherMechanismIsRefusedByName(t *testing.T) {
	h := newMongoLoginHarness(t, &mongoFakeServer{})
	done := h.run()

	hello := bdoc(
		bint32("hello", 1),
		bsub("speculativeAuthenticate", bdoc(
			bint32("saslStart", 1),
			bstr("mechanism", mongoauth.ScramSHA1),
			bbin("payload", []byte("n,,n=alice,r=abc")),
			bstr("db", "admin"),
		)),
		bstr("$db", "admin"),
	)
	h.write(mongoauth.EncodeOpMsg(91, 0, hello))
	msg := h.expectError(91, mongoauth.CodeAuthenticationFailed)
	if !strings.Contains(msg, mongoauth.ScramSHA1) || !strings.Contains(msg, "PLAIN") {
		t.Fatalf("errmsg %q does not name the mechanisms", msg)
	}
	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) || res.Identity.Subject != "alice" {
		t.Fatalf("result %+v, %v", res, err)
	}
}

func TestMongoLoginCertificateCredentialRedialsWithIt(t *testing.T) {
	server := &mongoFakeServer{x509User: "CN=svc"}
	h := newMongoLoginHarness(t, server)
	cert := &tls.Certificate{Certificate: [][]byte{{1, 2, 3}}}
	h.conn.Authenticate = func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
		return session.Identity{Subject: "alice@example.com"}, proxy.Credential{Name: "svc-cert", User: "CN=svc", Cert: cert}, nil
	}
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(101, 0, plainHello()))
	h.read()
	h.write(mongoauth.EncodeOpMsg(102, 0, plainStartDoc("alice", mongoSecret)))
	resp, err := mongoauth.DecodeSASLResponse(h.read().Body)
	if err != nil || !resp.OK || !resp.Done {
		t.Fatalf("final reply %+v, %v", resp, err)
	}

	res, err := h.wait(done)
	if err != nil || res.Credential.Name != "svc-cert" {
		t.Fatalf("result %+v, %v", res, err)
	}
	if len(h.dialed) != 2 || h.dialed[0] != nil || h.dialed[1] == nil || h.dialed[1].Cert != cert {
		t.Fatalf("dialed %v, want once plain and once with the certificate", h.dialed)
	}
	hellos, authenticated, serr := server.state()
	if serr != nil || len(hellos) != 2 || len(authenticated) != 1 || authenticated[0] != "CN=svc" {
		t.Fatalf("upstream saw hellos=%d authenticated=%v err=%v", len(hellos), authenticated, serr)
	}
}

func TestMongoLoginUpstreamRefusalIsNotALoginRefusal(t *testing.T) {
	server := &mongoFakeServer{users: map[string]string{"svc": "other-pw"}}
	h := newMongoLoginHarness(t, server)
	released := 0
	h.conn.Authenticate = func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
		return session.Identity{Subject: "alice@example.com"}, proxy.Credential{Name: "svc-rw", User: "svc", Password: "stale",
			Release: func(context.Context) error { released++; return nil }}, nil
	}
	done := h.run()

	h.write(mongoauth.EncodeOpMsg(111, 0, plainHello()))
	h.read()
	h.write(mongoauth.EncodeOpMsg(112, 0, plainStartDoc("alice", mongoSecret)))
	if msg := h.expectError(112, mongoauth.CodeAuthenticationFailed); !strings.Contains(msg, "upstream") {
		t.Fatalf("errmsg %q", msg)
	}
	res, err := h.wait(done)
	if err == nil || errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err = %v, want an upstream error", err)
	}
	// The selected credential rides the result so the relay releases it.
	if res.Credential.Name != "svc-rw" || res.Credential.Release == nil || released != 0 {
		t.Fatalf("result %+v released %d", res, released)
	}
}

// FuzzMongoLogin feeds arbitrary bytes as the client's whole login stream,
// with a fake upstream that answers anything, and requires the driver to
// return rather than panic or hang.
func FuzzMongoLogin(f *testing.F) {
	sc, _ := mongoauth.NewScramClient(mongoauth.ScramSHA256, "legacy", "legacy-pw")
	first, _ := sc.NewConversation().Step("")
	f.Add(mongoauth.EncodeOpQuery(1, "admin.$cmd", speculativeHello([]byte(first), true)))
	f.Add(append(mongoauth.EncodeOpMsg(1, 0, plainHello()),
		mongoauth.EncodeOpMsg(2, 0, plainStartDoc("alice", mongoSecret))...))
	f.Add(append(mongoauth.EncodeOpMsg(1, 0, plainHello()),
		mongoauth.EncodeOpMsg(2, 0, saslStartDoc([]byte(first), false))...))
	f.Add(mongoauth.EncodeOpMsg(1, 0, plainStartPayload([]byte("\x00\x00"))))
	f.Add(append(mongoauth.EncodeOpMsg(1, 0, plainHello()),
		mongoauth.EncodeOpMsg(2, 0, saslContinueDoc(1, []byte("c=biws,r=x,p=AAAA")))...))
	f.Add(mongoauth.EncodeOpMsg(1, 0, bdoc(bstr("find", "x"))))
	f.Add([]byte{0xff, 0xff, 0xff, 0x7f, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		server := &mongoFakeServer{users: map[string]string{"svc": "svc-pw", "legacy": "legacy-pw"}}
		clientEnd, relayEnd := net.Pipe()
		var upstreams []net.Conn
		var mu sync.Mutex
		conn := &proxy.LoginConn{
			Client: relayEnd,
			Lane:   proxy.LaneInfo{Protocol: inspect.MongoDB, Name: "mongo", Network: "tcp", ClientTLS: true},
			Dial: func(context.Context, *proxy.Credential) (net.Conn, error) {
				up, down := net.Pipe()
				go server.serve(down)
				mu.Lock()
				upstreams = append(upstreams, up)
				mu.Unlock()
				return up, nil
			},
			Passthrough: func(user string) bool { return user == "legacy" },
			Authenticate: func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
				return session.Identity{Subject: "alice@example.com"}, proxy.Credential{User: "svc", Password: "svc-pw"}, nil
			},
			Inspect: func(inspect.Direction, []byte) error { return nil },
		}
		go io.Copy(io.Discard, clientEnd)
		go func() {
			clientEnd.Write(data)
			clientEnd.Close()
		}()
		done := make(chan struct{})
		go func() {
			defer close(done)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			loginMongoDB(ctx, conn)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("login did not return")
		}
		relayEnd.Close()
		mu.Lock()
		for _, up := range upstreams {
			up.Close()
		}
		mu.Unlock()
	})
}
