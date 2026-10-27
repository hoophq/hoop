package login

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	codecoracle "github.com/hoophq/libhoop/v2/codec/oracle"
	oraauth "github.com/hoophq/libhoop/v2/codec/oracle/auth"
	"github.com/hoophq/libhoop/v2/codec/oracle/oranet"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// oracleRead is one socket read of a captured session: who sent it and the
// bytes. The fixtures under testdata/oracle are the libhoop captures of
// SQL*Plus 23 against XE 21c (classic login), SQL*Plus 23 against 23ai (fast
// authentication) and go-ora against 23ai, through the client's first
// authentication call.
type oracleRead struct {
	server bool
	data   []byte
}

func loadOracleReads(t testing.TB, name string) []oracleRead {
	t.Helper()
	f, err := os.Open("testdata/oracle/" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var reads []oracleRead
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		side, data, _ := strings.Cut(scanner.Text(), " ")
		b, err := hex.DecodeString(data)
		if err != nil {
			t.Fatal(err)
		}
		reads = append(reads, oracleRead{server: side == "S", data: b})
	}
	return reads
}

// oracleScript is a fake upstream that answers each client read of the
// capture with the server read that follows it, byte for byte, and records
// what the relay sent it.
type oracleScript struct {
	reads []oracleRead
	mu    sync.Mutex
	got   [][]byte
	err   error
}

func (s *oracleScript) serve(conn net.Conn) {
	defer conn.Close()
	for i := 0; i < len(s.reads); i++ {
		r := s.reads[i]
		if r.server {
			if _, err := conn.Write(r.data); err != nil {
				s.fail(err)
				return
			}
			continue
		}
		buf := make([]byte, len(r.data))
		if _, err := readFullConn(conn, buf); err != nil {
			s.fail(err)
			return
		}
		s.mu.Lock()
		s.got = append(s.got, buf)
		s.mu.Unlock()
		if !bytes.Equal(buf, r.data) {
			s.fail(errors.New("upstream received bytes that differ from the capture"))
			return
		}
	}
	// Hold the connection open: the relay decides what happens next.
	_, _ = readFullConn(conn, make([]byte, 1))
}

func readFullConn(conn net.Conn, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := conn.Read(buf[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (s *oracleScript) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *oracleScript) received() ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got, s.err
}

type oracleHarness struct {
	t        *testing.T
	client   net.Conn
	script   *oracleScript
	conn     *proxy.LoginConn
	mu       sync.Mutex
	dialed   int
	inspects []inspect.Direction
}

func newOracleHarness(t *testing.T, script *oracleScript, passthrough ...string) *oracleHarness {
	t.Helper()
	clientEnd, relayEnd := net.Pipe()
	h := &oracleHarness{t: t, client: clientEnd, script: script}
	h.conn = &proxy.LoginConn{
		Client: relayEnd,
		Lane:   proxy.LaneInfo{Protocol: inspect.Oracle, Name: "ora", Network: "tcp", UpstreamAddr: "db.internal:1521"},
		Dial: func(_ context.Context, cred *proxy.Credential) (net.Conn, error) {
			if cred != nil {
				t.Errorf("oracle dialed with a credential before any identity was verified")
			}
			h.mu.Lock()
			h.dialed++
			h.mu.Unlock()
			up, down := net.Pipe()
			go script.serve(down)
			return up, nil
		},
		Authenticate: func(context.Context, string, string) (session.Identity, proxy.Credential, error) {
			t.Errorf("Authenticate called: no Oracle secret is ever recoverable")
			return session.Identity{}, proxy.Credential{}, proxy.ErrLoginRefused
		},
		Passthrough: func(user string) bool {
			for _, p := range passthrough {
				if p == user {
					return true
				}
			}
			return false
		},
		Inspect: func(dir inspect.Direction, pkt []byte) error {
			h.mu.Lock()
			h.inspects = append(h.inspects, dir)
			h.mu.Unlock()
			return nil
		},
	}
	t.Cleanup(func() { clientEnd.Close(); relayEnd.Close() })
	return h
}

type oracleOutcome struct {
	res proxy.LoginResult
	err error
}

func (h *oracleHarness) run() <-chan oracleOutcome {
	out := make(chan oracleOutcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := loginOracle(ctx, h.conn)
		out <- oracleOutcome{res, err}
	}()
	return out
}

func (h *oracleHarness) wait(done <-chan oracleOutcome) (proxy.LoginResult, error) {
	h.t.Helper()
	select {
	case r := <-done:
		return r.res, r.err
	case <-time.After(5 * time.Second):
		h.t.Fatal("login did not finish")
		return proxy.LoginResult{}, nil
	}
}

// play sends the capture's client reads and expects the server reads back,
// byte for byte, up to and including read index last.
func (h *oracleHarness) play(reads []oracleRead, last int) {
	h.t.Helper()
	for i := 0; i <= last; i++ {
		r := reads[i]
		if r.server {
			h.expectBytes(r.data)
			continue
		}
		if _, err := h.client.Write(r.data); err != nil {
			h.t.Fatalf("client write %d: %v", i, err)
		}
	}
}

func (h *oracleHarness) expectBytes(want []byte) {
	h.t.Helper()
	got := make([]byte, len(want))
	h.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := readFullConn(h.client, got); err != nil {
		h.t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, want) {
		h.t.Fatalf("client received bytes that differ from the capture\n got %x\nwant %x", got, want)
	}
}

// readFrame reads one TNS packet from the client's end.
func (h *oracleHarness) readFrame(big bool) *oranet.Packet {
	h.t.Helper()
	h.client.SetReadDeadline(time.Now().Add(5 * time.Second))
	pkt, err := oranet.Decode(h.client, big)
	if err != nil {
		h.t.Fatalf("client read: %v", err)
	}
	return pkt
}

func (h *oracleHarness) expectRefusal(big bool) {
	h.t.Helper()
	pkt := h.readFrame(big)
	if pkt.Type() != oranet.PacketData {
		h.t.Fatalf("refusal is packet type %d, not DATA", pkt.Type())
	}
	ttc := pkt.Payload()[oranet.DataFlagSize:]
	if ttc[0] != oraauth.TTCMsgSummary || !bytes.Contains(ttc, []byte("ORA-01017: invalid username/password; logon denied\n")) {
		h.t.Fatalf("refusal is not an ORA-01017 summary: %x", ttc)
	}
}

// clientCall is the index of the first client read carrying the given
// authentication function: 0x76 is phase one, also as the fast
// authentication bundle (0x22) that embeds it; 0x73 is phase two.
func clientCall(t *testing.T, reads []oracleRead, fn byte) int {
	t.Helper()
	for i, r := range reads {
		if r.server || len(r.data) < 12 || r.data[4] != byte(oranet.PacketData) {
			continue
		}
		if r.data[10] == 0x03 && r.data[11] == fn || fn == 0x76 && r.data[10] == 0x22 {
			return i
		}
	}
	t.Fatalf("no client call 0x%02x in the capture", fn)
	return -1
}

func TestOracleLoginPassthroughRelaysHandshakeUnchanged(t *testing.T) {
	reads := loadOracleReads(t, "sqlplus23_xe21_login")
	phase1 := clientCall(t, reads, 0x76)
	script := &oracleScript{reads: reads[:phase1+1]}
	h := newOracleHarness(t, script, "APP")
	done := h.run()
	h.play(reads, phase1)
	res, err := h.wait(done)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !res.Passthrough || res.Identity.Subject != "app" {
		t.Fatalf("result %+v", res)
	}
	got, serr := script.received()
	if serr != nil {
		t.Fatalf("upstream: %v", serr)
	}
	// Two CONNECTs (the capture has a RESEND), ANO, PRO, DTY and phase one.
	if len(got) != 6 || !bytes.Equal(got[5], reads[phase1].data) {
		t.Fatalf("upstream received %d packets; last %x", len(got), got[len(got)-1])
	}
	if h.conn.Upstream == nil || h.dialed != 1 {
		t.Fatalf("upstream %v dialed %d times", h.conn.Upstream, h.dialed)
	}
	// Every packet went through Inspect, in the order it flowed.
	want := []inspect.Direction{}
	for _, r := range reads[:phase1+1] {
		if r.server {
			want = append(want, inspect.FromServer)
		} else {
			want = append(want, inspect.FromClient)
		}
	}
	if len(h.inspects) != len(want) {
		t.Fatalf("inspected %v, want %v", h.inspects, want)
	}
	for i := range want {
		if h.inspects[i] != want[i] {
			t.Fatalf("inspected %v, want %v", h.inspects, want)
		}
	}
}

func TestOracleLoginRefusesClassicLoginWithORA01017(t *testing.T) {
	reads := loadOracleReads(t, "sqlplus23_xe21_login")
	phase1 := clientCall(t, reads, 0x76)
	script := &oracleScript{reads: reads[:phase1]}
	h := newOracleHarness(t, script, "someone-else")
	done := h.run()
	h.play(reads, phase1)
	h.expectRefusal(true)
	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err %v, want proxy.ErrLoginRefused", err)
	}
	if res.Identity.Subject != "app" || res.Passthrough {
		t.Fatalf("result %+v", res)
	}
	got, serr := script.received()
	if serr != nil {
		t.Fatalf("upstream: %v", serr)
	}
	// The server saw the negotiation, never the user.
	if len(got) != 5 {
		t.Fatalf("upstream received %d packets, want 5", len(got))
	}
}

// go-ora negotiates thin: the refusal takes the thin summary layout.
func TestOracleLoginRefusesThinClientAtPhaseOne(t *testing.T) {
	reads := loadOracleReads(t, "goora23_negotiation")
	script := &oracleScript{reads: reads}
	h := newOracleHarness(t, script)
	done := h.run()
	h.play(reads, len(reads)-1)
	phase1 := oraauth.WrapDataPacket(oraauth.BuildAuthPhase1("SCOTT"), true)
	if _, err := h.client.Write(phase1); err != nil {
		t.Fatal(err)
	}
	h.expectRefusal(true)
	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) || res.Identity.Subject != "SCOTT" {
		t.Fatalf("err %v result %+v", err, res)
	}
}

// Fast authentication names the user before the server settles the
// format: the bundle and the server's reply are relayed, the client's
// credential call is answered with the refusal.
func TestOracleLoginRefusesFastAuthAfterServerReply(t *testing.T) {
	reads := loadOracleReads(t, "sqlplus23_fastauth")
	bundle := clientCall(t, reads, 0x76)
	phase2 := clientCall(t, reads, 0x73)
	if phase2 != len(reads)-1 || bundle != phase2-2 {
		t.Fatalf("capture shape: bundle %d, phase two %d of %d", bundle, phase2, len(reads))
	}
	script := &oracleScript{reads: reads[:phase2]}
	h := newOracleHarness(t, script)
	done := h.run()
	h.play(reads, phase2)
	h.expectRefusal(true)
	res, err := h.wait(done)
	if !errors.Is(err, proxy.ErrLoginRefused) || res.Identity.Subject != "app" {
		t.Fatalf("err %v result %+v", err, res)
	}
	got, serr := script.received()
	if serr != nil {
		t.Fatalf("upstream: %v", serr)
	}
	// The server saw the CONNECT, ANO and the bundle, never the credentials.
	if len(got) != 3 {
		t.Fatalf("upstream received %d packets, want 3", len(got))
	}
}

func TestOracleLoginPassthroughMatchesUpperCaseName(t *testing.T) {
	reads := loadOracleReads(t, "sqlplus23_fastauth")
	bundle := clientCall(t, reads, 0x76)
	script := &oracleScript{reads: reads[:bundle+1]}
	h := newOracleHarness(t, script, "APP")
	done := h.run()
	h.play(reads, bundle)
	res, err := h.wait(done)
	if err != nil || !res.Passthrough || res.Identity.Subject != "app" {
		t.Fatalf("err %v result %+v", err, res)
	}
}

// A RESEND goes back to the client, which sends its CONNECT again, as
// SQL*Plus 23 did against XE 21c.
func TestOracleLoginRelaysResend(t *testing.T) {
	reads := loadOracleReads(t, "goora23_negotiation")
	resend := []byte{0x00, 0x08, 0x00, 0x00, 0x0b, 0x00, 0x00, 0x00}
	scripted := append([]oracleRead{reads[0], {server: true, data: resend}}, reads...)
	script := &oracleScript{reads: scripted}
	h := newOracleHarness(t, script)
	done := h.run()
	h.play(scripted, len(scripted)-1)
	if _, err := h.client.Write(oraauth.WrapDataPacket(oraauth.BuildAuthPhase1("X"), true)); err != nil {
		t.Fatal(err)
	}
	h.expectRefusal(true)
	if _, err := h.wait(done); !errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err %v", err)
	}
}

func TestOracleLoginForwardsRefuseAndCloses(t *testing.T) {
	reads := loadOracleReads(t, "goora23_negotiation")
	refuse := []byte{0x00, 0x0c, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x02, 0x00, 0x00}
	script := &oracleScript{reads: []oracleRead{reads[0], {server: true, data: refuse}}}
	h := newOracleHarness(t, script)
	done := h.run()
	h.play(script.reads, 1)
	_, err := h.wait(done)
	if err == nil || errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err %v, want an upstream error that is not a login refusal", err)
	}
}

func TestOracleLoginDoesNotRelayRedirect(t *testing.T) {
	reads := loadOracleReads(t, "goora23_negotiation")
	redirect := []byte{0x00, 0x0a, 0x00, 0x00, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00}
	script := &oracleScript{reads: []oracleRead{reads[0], {server: true, data: redirect}}}
	h := newOracleHarness(t, script)
	done := h.run()
	if _, err := h.client.Write(reads[0].data); err != nil {
		t.Fatal(err)
	}
	_, err := h.wait(done)
	if err == nil || errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err %v", err)
	}
	h.client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if n, _ := h.client.Read(make([]byte, 1)); n != 0 {
		t.Fatal("the client was shown the REDIRECT")
	}
}

func TestOracleLoginRefusesNonConnectFirstPacket(t *testing.T) {
	script := &oracleScript{}
	h := newOracleHarness(t, script)
	done := h.run()
	if _, err := h.client.Write(oraauth.WrapDataPacket([]byte{0x03, 0x76}, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.wait(done); err == nil || errors.Is(err, proxy.ErrLoginRefused) {
		t.Fatalf("err %v", err)
	}
}

func TestOracleConnectContinues(t *testing.T) {
	frame := make([]byte, 40)
	binary.BigEndian.PutUint16(frame[24:], 100) // descriptor length
	binary.BigEndian.PutUint16(frame[26:], 40)  // starts past this packet
	if !oracleConnectContinues(frame) {
		t.Fatal("descriptor past the packet not detected")
	}
	binary.BigEndian.PutUint16(frame[24:], 6)
	binary.BigEndian.PutUint16(frame[26:], 34)
	if oracleConnectContinues(frame) {
		t.Fatal("descriptor inside the packet reported as continuing")
	}
	if oracleConnectContinues(frame[:20]) {
		t.Fatal("short header reported as continuing")
	}
}

// FuzzOracleLogin feeds arbitrary bytes to the decoders the driver relies
// on: the O5LOGON challenge parser and splicer, the auth-message reader and
// the codec's login tracking. They must return an error, never panic, and a
// parsed challenge must hold a session key the re-key functions accept.
func FuzzOracleLogin(f *testing.F) {
	reads := loadOracleReads(f, "sqlplus23_xe21_login")
	for _, r := range reads[len(reads)-3:] { // phase one, the challenge, phase two
		f.Add(r.data[oranet.HeaderSize:], r.server)
	}
	f.Add(append([]byte{0, 0}, oraauth.BuildAuthPhase1("SCOTT")...), false)
	f.Add([]byte{0, 0, 0x08, 0x01, 0x0c, 0x0c, 'A', 'U', 'T', 'H', '_', 'S', 'E', 'S', 'S', 'K', 'E', 'Y', 0x01, 0x40, 0xfe}, true)
	f.Fuzz(func(t *testing.T, payload []byte, thick bool) {
		if ch, err := oraauth.ParseChallenge(payload, thick); err == nil {
			if n := len(ch.EServerSessKey); n != 64 && n != 96 {
				t.Fatalf("challenge accepted with AUTH_SESSKEY of %d hex chars", n)
			}
			oraauth.SpliceChallengeServerSessKey(payload, ch.EServerSessKey, thick)
			oraauth.ReEncryptServerSessKey(ch, "real", "placeholder")
			oraauth.RekeyCredentials(ch, "real", "placeholder", ch.EServerSessKey)
		}
		oraauth.AuthUser(payload)
		oraauth.Phase2Value(payload, "AUTH_SESSKEY")
		oraauth.SplicePhase1Username(payload, "x")
		oraauth.SplicePhase2(payload, "x", map[string]string{"AUTH_SESSKEY": "AB"})
		c := codecoracle.New(codecoracle.Options{})
		c.Decode(inspect.FromClient, oraauth.WrapDataPacket(payload, true))
		c.LoginUser()
		c.ErrorFrame(oraLogonDenied, oraLogonDeniedText)
	})
}
