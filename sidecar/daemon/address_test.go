package daemon

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func httpURLLane(listen, upstream, extra string) string {
	return fmt.Sprintf(`{"listeners":[{"name":"web","protocol":"http","listen":%q,"upstream":%q%s}]}`,
		listen, upstream, extra)
}

// The forms Caddy and nginx take: the scheme decides the default port and
// whether the hop is TLS, and Host names the origin as a browser would.
func TestAnHTTPListenerReadsURLs(t *testing.T) {
	for _, tc := range []struct {
		listen, upstream string
		bind, dial, host string
		tls              bool
	}{
		{"http://127.0.0.1:19999", "https://hoop.dev", "127.0.0.1:19999", "hoop.dev:443", "hoop.dev", true},
		{"http://localhost", "http://mat-m.com/", "localhost:80", "mat-m.com:80", "mat-m.com", false},
		{"0.0.0.0:8080", "https://api.example.com:8443", "0.0.0.0:8080", "api.example.com:8443", "api.example.com:8443", true},
		{"http://[::1]:8080", "https://[::1]", "[::1]:8080", "[::1]:443", "[::1]", true},
		{"127.0.0.1:1", "http://10.0.0.5:80", "127.0.0.1:1", "10.0.0.5:80", "10.0.0.5", false},
		{"http://127.0.0.1:0008080", "https://h:00443", "127.0.0.1:8080", "h:443", "h", true},
	} {
		if err := CheckConfigBytes([]byte(httpURLLane(tc.listen, tc.upstream, ""))); err != nil {
			t.Errorf("%s -> %s refused: %v", tc.listen, tc.upstream, err)
			continue
		}
		l := ListenerConfig{Protocol: "http", Listen: tc.listen, Upstream: tc.upstream}
		if got := l.listenAddress(); got != tc.bind {
			t.Errorf("%s binds %q, want %q", tc.listen, got, tc.bind)
		}
		if got := l.upstreamAddress(); got != tc.dial {
			t.Errorf("%s dials %q, want %q", tc.upstream, got, tc.dial)
		}
		if o := l.origin(); o == nil || o.Host != tc.host || o.Proto != "http" {
			t.Errorf("%s: origin %+v, want Host %q", tc.upstream, o, tc.host)
		}
		if got := l.upstreamTLS() != nil; got != tc.tls {
			t.Errorf("%s: upstream TLS %v, want %v", tc.upstream, got, tc.tls)
		}
	}
}

// A host:port lane keeps today's behaviour: dialed as written, Host passed
// through, TLS only when upstream_tls says.
func TestAHostPortUpstreamIsNotAnOrigin(t *testing.T) {
	l := ListenerConfig{Protocol: "http", Listen: "127.0.0.1:1", Upstream: "hoop.dev:443"}
	if l.origin() != nil || l.upstreamTLS() != nil || l.upstreamAddress() != "hoop.dev:443" {
		t.Errorf("a host:port lane changed: origin %+v, tls %v, dial %q",
			l.origin(), l.upstreamTLS(), l.upstreamAddress())
	}
	if l.usesAddressURL() {
		t.Error("a host:port lane reads as a URL lane")
	}
}

// What the scheme says over TLS must agree with the TLS blocks, the lane on
// https:// behind a client's TLS reports https to the origin, and an
// explicit upstream_tls wins over the empty block https implies.
func TestURLSchemesAgreeWithTheTLSBlocks(t *testing.T) {
	tlsLane := ListenerConfig{Protocol: "http", Listen: "https://0.0.0.0:8443", Upstream: "https://h",
		DownstreamTLS: &TLSConfig{CertFile: "c", KeyFile: "k"}, UpstreamTLS: &TLSConfig{ServerName: "sni"}}
	if o := tlsLane.origin(); o == nil || o.Proto != "https" {
		t.Errorf("a TLS listener forwards proto %+v, want https", o)
	}
	if got := tlsLane.upstreamTLS(); got == nil || got.ServerName != "sni" {
		t.Errorf("upstream_tls was replaced: %+v", got)
	}

	cert := `,"downstream_tls":{"cert_file":"c","key_file":"k"}`
	if err := CheckConfigBytes([]byte(httpURLLane("https://0.0.0.0:8443", "https://h", cert))); err != nil {
		t.Errorf("https listen with downstream_tls refused: %v", err)
	}
	if err := CheckConfigBytes([]byte(httpURLLane("http://0.0.0.0:8080", "https://h", `,"upstream_tls":{"ca_file":"ca.pem"}`))); err != nil {
		t.Errorf("https upstream with upstream_tls refused: %v", err)
	}
	// A socket path takes no URL, but the upstream it proxies to may.
	if err := CheckConfigBytes([]byte(httpURLLane("/run/web.sock", "https://h", `,"network":"unix"`))); err != nil {
		t.Errorf("a unix listener in front of a URL upstream refused: %v", err)
	}
}

// Each refusal names the listener and says what to write instead.
func TestAddressURLRefusals(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{
		{"path", httpURLLane("127.0.0.1:1", "https://h/api", ""), "write the origin only"},
		{"query", httpURLLane("127.0.0.1:1", "https://h?x=1", ""), "write the origin only"},
		{"credentials", httpURLLane("127.0.0.1:1", "https://user:pw@h", ""), "write the origin only"},
		{"scheme", httpURLLane("127.0.0.1:1", "ws://h", ""), "scheme must be http or https"},
		{"no host", httpURLLane("127.0.0.1:1", "https://", ""), "names no host"},
		{"port", httpURLLane("http://127.0.0.1:0", "https://h:99999", ""), "out of range"},
		{"https listen without a certificate", httpURLLane("https://0.0.0.0:8443", "https://h", ""), "needs downstream_tls"},
		{"http listen over TLS", httpURLLane("http://0.0.0.0:8080", "https://h",
			`,"downstream_tls":{"cert_file":"c","key_file":"k"}`), "write https://"},
		{"http upstream over TLS", httpURLLane("127.0.0.1:1", "http://h", `,"upstream_tls":{}`), "drop upstream_tls"},
		{"unix", httpURLLane("http://127.0.0.1:1", "https://h", `,"network":"unix"`), "socket path"},
		{"not http", `{"listeners":[{"name":"db","protocol":"postgres","listen":"127.0.0.1:1","upstream":"https://db"}]}`,
			"on an http listener only"},
		{"same bind address", `{"listeners":[
			{"name":"a","protocol":"http","listen":"http://127.0.0.1:80","upstream":"h:1"},
			{"name":"b","protocol":"http","listen":"127.0.0.1:80","upstream":"h:1"}]}`, "duplicate listen address"},
	} {
		err := CheckConfigBytes([]byte(tc.doc))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want a refusal containing %q", tc.name, err, tc.want)
		}
	}
}

// An older sidecar decodes a URL and dials it verbatim, so the plane serves
// a URL lane only to a build that reads the form.
func TestAURLLaneNeedsTheCapability(t *testing.T) {
	cfg := Config{Listeners: []ListenerConfig{{Name: "web", Protocol: "http",
		Listen: "127.0.0.1:1", Upstream: "https://hoop.dev"}}}
	if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("this build was refused a URL lane: %v", err)
	}
	old := Handshake{Version: "1.220.0", Capabilities: []string{"protocol:http", "rule:operation"}}
	err := CheckServable(cfg, old)
	if err == nil || !strings.Contains(err.Error(), `listener "web" writes listen or upstream as a URL`) ||
		!strings.Contains(err.Error(), CapabilityAddressURL) {
		t.Errorf("an older build was served a URL lane: %v", err)
	}
	cfg.Listeners[0].Upstream = "hoop.dev:443"
	if err := CheckServable(cfg, old); err != nil {
		t.Errorf("a host:port lane was refused to an older build: %v", err)
	}
}

// An older sidecar drops pii.ner and then refuses the rule naming PERSON, so
// the plane serves the block only to a build whose detector reads it.
func TestPIINERNeedsTheCapability(t *testing.T) {
	cfg := Config{PII: json.RawMessage(`{"entities":["PERSON"],"ner":{"backend":"go"}}`)}
	if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
		t.Errorf("this build was refused pii.ner: %v", err)
	}
	old := Handshake{Version: "1.230.0", Capabilities: []string{"protocol:postgres", "rule:pii"}}
	err := CheckServable(cfg, old)
	if err == nil || !strings.Contains(err.Error(), "sets pii.ner") || !strings.Contains(err.Error(), CapabilityPIINER) {
		t.Errorf("an older build was served pii.ner: %v", err)
	}
	for _, pii := range []string{`{"entities":["US_SSN"]}`, `{"ner":null}`, ``} {
		cfg.PII = json.RawMessage(pii)
		if err := CheckServable(cfg, old); err != nil {
			t.Errorf("pii %q was refused to an older build: %v", pii, err)
		}
	}
}

// Through a lane built the way Run builds it: a browser-style request names
// the sidecar, and the origin receives it over TLS under its own name, with
// the browser's name in X-Forwarded-Host. A WebSocket handshake gets the
// same treatment and its frames pass both ways.
func TestAURLLaneProxiesToTheOriginByName(t *testing.T) {
	type seen struct{ host, forwardedHost, proto string }
	got := make(chan seen, 2)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Host, r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto")}
		if r.Header.Get("Upgrade") != "websocket" {
			_, _ = io.WriteString(w, "hello from the origin")
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:])); err != nil {
			t.Errorf("write the 101: %v", err)
			return
		}
		if err := rw.Flush(); err != nil {
			t.Errorf("flush the 101: %v", err)
			return
		}
		// One masked client text frame in, its payload echoed unmasked.
		hdr := make([]byte, 6)
		if _, err := io.ReadFull(rw, hdr); err != nil {
			return
		}
		payload := make([]byte, hdr[1]&0x7f)
		if _, err := io.ReadFull(rw, payload); err != nil {
			return
		}
		for i := range payload {
			payload[i] ^= hdr[2+i%4]
		}
		_, _ = rw.Write(append([]byte{0x81, byte(len(payload))}, payload...))
		_ = rw.Flush()
	}))
	defer origin.Close()

	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	upstream := "https://" + origin.Listener.Addr().String()
	ln := lane{name: "web", trustRoots: roots, cfg: ListenerConfig{Name: "web", Protocol: "http",
		Listen: "http://127.0.0.1:0", Upstream: upstream}}
	ln.codecFactory = laneCodecFactory(ln.cfg, nil)
	srv, err := buildServer(ln, AuditConfig{}, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	addr := waitForAddr(t, srv)
	want := seen{origin.Listener.Addr().String(), addr, "http"}

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read the body: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "hello from the origin" {
		t.Errorf("status %d body %q", resp.StatusCode, body)
	}
	if s := <-got; s != want {
		t.Errorf("the origin saw %+v, want %+v", s, want)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", addr); err != nil {
		t.Fatalf("write the handshake: %v", err)
	}
	br := bufio.NewReader(conn)
	wsResp, err := http.ReadResponse(br, nil)
	if err != nil || wsResp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %v %v", wsResp, err)
	}
	if s := <-got; s != want {
		t.Errorf("the origin saw the handshake as %+v, want %+v", s, want)
	}
	mask := []byte{1, 2, 3, 4}
	frame := append([]byte{0x81, 0x80 | 5}, mask...)
	for i, b := range []byte("hello") {
		frame = append(frame, b^mask[i%4])
	}
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	echo := make([]byte, 7)
	if _, err := io.ReadFull(br, echo); err != nil || string(echo) != "\x81\x05hello" {
		t.Errorf("echo %q, %v", echo, err)
	}
}
