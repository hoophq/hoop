package proxy_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// seenRequest is what an HTTP/1 upstream observed of one request.
type seenRequest struct {
	method, proto, host, uri string
	header                   http.Header
	// remote is the relay's side of the upstream connection: one per
	// relay session.
	remote string
}

// h1Upstream is a real HTTP/1.1 server, standing in for the API server
// behind the relay. /watch writes one line, flushes, and holds the response
// open until release, the shape of `kubectl get -w` and `logs -f`.
type h1Upstream struct {
	srv         *httptest.Server
	mu          sync.Mutex
	seen        []seenRequest
	releaseCh   chan struct{}
	releaseOnce sync.Once
}

func newH1Upstream(t *testing.T) *h1Upstream {
	t.Helper()
	u := &h1Upstream{releaseCh: make(chan struct{})}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.seen = append(u.seen, seenRequest{
			method: r.Method, proto: r.Proto, host: r.Host, uri: r.RequestURI, header: r.Header.Clone(),
			remote: r.RemoteAddr,
		})
		u.mu.Unlock()
		if r.URL.Path != "/watch" {
			_, _ = io.WriteString(w, "ok "+r.URL.Path)
			return
		}
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		select {
		case <-u.releaseCh:
			_, _ = io.WriteString(w, "last\n")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { u.release(); u.srv.Close() })
	return u
}

func (u *h1Upstream) addr() string { return strings.TrimPrefix(u.srv.URL, "http://") }

func (u *h1Upstream) release() { u.releaseOnce.Do(func() { close(u.releaseCh) }) }

func (u *h1Upstream) requests() []seenRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seenRequest(nil), u.seen...)
}

// splitFirstWrite delivers the first write in two segments with a pause
// between, so the relay's first read returns a fragment of the h2 preface.
type splitFirstWrite struct {
	net.Conn
	once sync.Once
}

func (c *splitFirstWrite) Write(b []byte) (int, error) {
	split := false
	c.once.Do(func() { split = len(b) > 8 })
	if !split {
		return c.Conn.Write(b)
	}
	n, err := c.Conn.Write(b[:8])
	if err != nil {
		return n, err
	}
	time.Sleep(30 * time.Millisecond)
	m, err := c.Conn.Write(b[8:])
	return n + m, err
}

// h2cClient speaks HTTP/2 with prior knowledge, the way a Go client with
// Protocols{UnencryptedHTTP2} does. dials counts TCP connections, so a
// test can prove two requests shared one h2 connection.
func h2cClient(t *testing.T, dials *atomic.Int32, split bool) *http.Client {
	t.Helper()
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	tr := &http.Transport{
		Protocols: p,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dials != nil {
				dials.Add(1)
			}
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil || !split {
				return c, err
			}
			return &splitFirstWrite{Conn: c}, nil
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func relayCert(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "relay.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
		MinVersion:   tls.VersionTLS12,
	}, pool
}

func get(t *testing.T, c *http.Client, url, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return string(b)
}

// A prior-knowledge h2c client must reach an HTTP/1.1 upstream as HTTP/1.1,
// with its :authority as the Host the upstream routes on, and with nothing
// the client did not send. The preface arrives split across two segments,
// so the sniff has to keep reading rather than guess from a fragment.
func TestH2CPriorKnowledgeReachesUpstreamAsHTTP1(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP})

	c := h2cClient(t, nil, true)
	resp := get(t, c, "http://"+srv.Addr().String()+"/api/v1/pods?labelSelector=app%3Dweb&watch=1",
		"connectgateway.example")
	if resp.ProtoMajor != 2 {
		t.Fatalf("client spoke %s, want HTTP/2", resp.Proto)
	}
	if body := readAll(t, resp.Body); resp.StatusCode != http.StatusOK || body != "ok /api/v1/pods" {
		t.Fatalf("got %d %q, want 200 %q", resp.StatusCode, body, "ok /api/v1/pods")
	}

	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(reqs))
	}
	got := reqs[0]
	if got.proto != "HTTP/1.1" {
		t.Errorf("upstream saw %s, want HTTP/1.1", got.proto)
	}
	if got.host != "connectgateway.example" {
		t.Errorf("upstream Host = %q, want the client's :authority", got.host)
	}
	if got.uri != "/api/v1/pods?labelSelector=app%3Dweb&watch=1" {
		t.Errorf("upstream request URI = %q", got.uri)
	}
	for name := range got.header {
		if strings.HasPrefix(name, "X-Forwarded-") || name == "Forwarded" {
			t.Errorf("the bridge added %s: %q", name, got.header[name])
		}
	}
}

// One TLS lane serves both kinds of client: ALPN h2 through the bridge, and
// ALPN http/1.1 through the plain relay, both against the same upstream.
// The operator's tls.Config must come out untouched, since the relay owns
// the ALPN list and the config may be shared.
func TestTLSLaneNegotiatesH2AndHTTP1ByALPN(t *testing.T) {
	up := newH1Upstream(t)
	serverTLS, roots := relayCert(t)
	srv := startServer(t, proxy.Config{
		Upstream: up.addr(), Protocol: inspect.HTTP, DownstreamTLS: serverTLS,
	})
	if serverTLS.NextProtos != nil {
		t.Errorf("the relay mutated the operator's tls.Config: NextProtos = %q", serverTLS.NextProtos)
	}
	url := "https://" + srv.Addr().String()

	// A client that offers no ALPN at all gets HTTP/1, as it would from
	// any server: h2 over TLS is only ever negotiated, never assumed.
	for _, tc := range []struct {
		name      string
		protocols func(*http.Protocols)
		offer     []string
		wantALPN  string
		wantMajor int
	}{
		{"h2", func(p *http.Protocols) { p.SetHTTP2(true) }, nil, "h2", 2},
		{"http1", func(p *http.Protocols) { p.SetHTTP1(true) }, []string{"http/1.1"}, "http/1.1", 1},
		{"no-alpn", func(p *http.Protocols) { p.SetHTTP1(true) }, nil, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := new(http.Protocols)
			tc.protocols(p)
			tr := &http.Transport{Protocols: p, TLSClientConfig: &tls.Config{RootCAs: roots, NextProtos: tc.offer}}
			t.Cleanup(tr.CloseIdleConnections)
			resp := get(t, &http.Client{Transport: tr, Timeout: 10 * time.Second}, url+"/"+tc.name, "api.example")
			if resp.TLS == nil || resp.TLS.NegotiatedProtocol != tc.wantALPN {
				t.Fatalf("negotiated %+v, want ALPN %q", resp.TLS, tc.wantALPN)
			}
			if resp.ProtoMajor != tc.wantMajor {
				t.Fatalf("client spoke %s", resp.Proto)
			}
			if body := readAll(t, resp.Body); body != "ok /"+tc.name {
				t.Fatalf("body = %q", body)
			}
		})
	}

	reqs := up.requests()
	if len(reqs) != 3 {
		t.Fatalf("upstream saw %d requests, want 3", len(reqs))
	}
	for _, r := range reqs {
		if r.proto != "HTTP/1.1" || r.host != "api.example" {
			t.Errorf("upstream saw %s Host %q, want HTTP/1.1 Host api.example", r.proto, r.host)
		}
	}
}

// A denial on one h2 stream is the relay's own 403, and it ends only that
// stream's relay connection: a sibling stream in flight on the same client
// connection keeps streaming and finishes. The denied request never
// reaches the upstream.
func TestH2DenialEndsOnlyTheDeniedStream(t *testing.T) {
	up := newH1Upstream(t)
	rule := policy.Rule{
		Name:    "no-admin",
		Type:    policy.MatchHTTPResource,
		Message: "admin endpoints are closed on this lane",
	}
	rule.Resources = []string{"/admin/**"}
	rules, err := policy.NewRules([]policy.Rule{rule})
	if err != nil {
		t.Fatal(err)
	}
	srv := startServer(t, proxy.Config{
		Upstream: up.addr(), Protocol: inspect.HTTP,
		Policy: rules, DenyWriter: proxy.ProtocolDenyWriter{},
	})
	var dials atomic.Int32
	c := h2cClient(t, &dials, false)
	base := "http://" + srv.Addr().String()

	watch := get(t, c, base+"/watch", "api.example")
	br := bufio.NewReader(watch.Body)
	if line, err := br.ReadString('\n'); err != nil || line != "first\n" {
		t.Fatalf("watch first line = %q, %v", line, err)
	}

	denied := get(t, c, base+"/admin/secrets", "api.example")
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("denied stream got %d, want 403", denied.StatusCode)
	}
	if body := readAll(t, denied.Body); !strings.Contains(body, "admin endpoints are closed on this lane") {
		t.Fatalf("denied body = %q, want the rule's message", body)
	}

	up.release()
	if rest := readAll(t, br); rest != "last\n" {
		t.Fatalf("sibling stream ended with %q, want %q", rest, "last\n")
	}
	if n := dials.Load(); n != 1 {
		t.Fatalf("client dialled %d connections, want both streams on 1", n)
	}
	for _, r := range up.requests() {
		if strings.HasPrefix(r.uri, "/admin") {
			t.Fatalf("the denied request reached the upstream: %s %s", r.method, r.uri)
		}
	}
}

// Two h2 clients never share a relay connection. The relay pins a session's
// identity to its connection, so a pooled connection that carried one
// client's request and then another's would audit and judge the second
// client as the first.
func TestH2ClientsNeverShareARelayConnection(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP})
	base := "http://" + srv.Addr().String()

	for _, path := range []string{"/a", "/b"} {
		resp := get(t, h2cClient(t, nil, false), base+path, "api.example")
		if body := readAll(t, resp.Body); body != "ok "+path {
			t.Fatalf("body = %q", body)
		}
	}
	reqs := up.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream saw %d requests, want 2", len(reqs))
	}
	if reqs[0].remote == reqs[1].remote {
		t.Fatalf("both clients' requests arrived on one relay connection (%s)", reqs[0].remote)
	}
}

// When an h2 client hangs up, the relay sessions it opened end with it,
// including the idle one its pool was keeping for the next request.
// Otherwise every departed kubectl would leave a session and an upstream
// socket open until the lane's IdleTimeout, which is off by default.
func TestH2ClientHangupEndsItsRelaySessions(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP})

	// The client's own connection, so the hangup is a close rather than
	// a pool eviction that waits for the stream bookkeeping to settle.
	conns := make(chan net.Conn, 1)
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	tr := &http.Transport{Protocols: p, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil {
			conns <- c
		}
		return c, err
	}}
	t.Cleanup(tr.CloseIdleConnections)
	resp := get(t, &http.Client{Transport: tr, Timeout: 10 * time.Second},
		"http://"+srv.Addr().String()+"/a", "api.example")
	if body := readAll(t, resp.Body); body != "ok /a" {
		t.Fatalf("body = %q", body)
	}
	if active, _, _ := srv.Stats(); active != 2 {
		t.Fatalf("active = %d, want the client connection and its pooled relay session", active)
	}
	(<-conns).Close()
	waitActive(t, srv, 0)
}

func waitActive(t *testing.T, srv *proxy.Server, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		active, _, _ := srv.Stats()
		if active == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("active = %d, want %d", active, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// CONNECT over h2 is answered 501 by the bridge and never becomes an
// HTTP/1 CONNECT to the upstream: the relay leg it would need is not HTTP.
func TestH2ConnectIsRefused(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP})

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Scheme: "http", Host: srv.Addr().String()},
		Host:   "backend.example:443",
		Header: http.Header{},
	}
	resp, err := h2cClient(t, nil, false).Do(req)
	if err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("CONNECT got %d, want 501", resp.StatusCode)
	}
	if reqs := up.requests(); len(reqs) != 0 {
		t.Fatalf("the upstream saw %+v", reqs)
	}
}

// A streamed response reaches the h2 client as it is written. Without
// flushing, `kubectl logs -f` would print nothing until the pod exits.
func TestH2StreamsTheResponseAsItArrives(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP})

	resp := get(t, h2cClient(t, nil, false), "http://"+srv.Addr().String()+"/watch", "api.example")
	br := bufio.NewReader(resp.Body)
	got := make(chan string, 1)
	go func() {
		line, _ := br.ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "first\n" {
			t.Fatalf("first chunk = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first chunk did not arrive while the upstream held the response open")
	}
	up.release()
	if rest := readAll(t, br); rest != "last\n" {
		t.Fatalf("rest = %q", rest)
	}
}

// An HTTP/1 client on a plaintext lane goes through the relay as before,
// including PROPFIND, whose first two bytes are also the h2 preface's and
// which here arrive on their own.
func TestHTTP1ClientOnPlaintextLaneIsUnaffected(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP})

	conn, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "PR"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := io.WriteString(conn, "OPFIND /dav HTTP/1.1\r\nHost: api.example\r\nContent-Length: 0\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	defer resp.Body.Close()
	if body := readAll(t, resp.Body); resp.ProtoMajor != 1 || body != "ok /dav" {
		t.Fatalf("got %s %q", resp.Proto, body)
	}
	reqs := up.requests()
	if len(reqs) != 1 || reqs[0].method != "PROPFIND" || reqs[0].proto != "HTTP/1.1" {
		t.Fatalf("upstream saw %+v, want one PROPFIND over HTTP/1.1", reqs)
	}
}

// The streams of an admitted h2 connection do not count against MaxConns:
// one kubectl holding two watches open must not turn away the next client.
// The next CONNECTION past the limit is still refused.
func TestH2StreamsDoNotCountTowardMaxConns(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP, MaxConns: 2})
	base := "http://" + srv.Addr().String()

	c := h2cClient(t, nil, false)
	for range 2 {
		resp := get(t, c, base+"/watch", "api.example")
		if line, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || line != "first\n" {
			t.Fatalf("h2 watch first line = %q, %v", line, err)
		}
	}

	// A second client connection is admitted: the lane holds one.
	second, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(second, "GET /watch HTTP/1.1\r\nHost: api.example\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.ReadResponse(bufio.NewReader(second), nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("second connection was not admitted: %v", err)
	}

	// A third is past MaxConns and closed unanswered.
	third, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	_ = third.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(third, "GET /x HTTP/1.1\r\nHost: api.example\r\n\r\n")
	if _, err := http.ReadResponse(bufio.NewReader(third), nil); err == nil {
		t.Fatal("a connection past MaxConns was answered")
	}
}

// Close ends an h2 stream the bridge is carrying, and leaves no bridged
// relay connection behind.
func TestCloseEndsBridgedH2Streams(t *testing.T) {
	up := newH1Upstream(t)
	srv := startServer(t, proxy.Config{Upstream: up.addr(), Protocol: inspect.HTTP})

	resp := get(t, h2cClient(t, nil, false), "http://"+srv.Addr().String()+"/watch", "api.example")
	br := bufio.NewReader(resp.Body)
	if line, err := br.ReadString('\n'); err != nil || line != "first\n" {
		t.Fatalf("first line = %q, %v", line, err)
	}
	if active, _, _ := srv.Stats(); active != 2 {
		t.Fatalf("active = %d, want the client connection and its one bridged stream", active)
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if rest, err := io.ReadAll(br); err == nil || strings.Contains(string(rest), "last") {
		t.Fatalf("the stream survived Close: %q, %v", rest, err)
	}
	waitActive(t, srv, 0)
}

// downstream_tls is accepted where the relay terminates it and refused
// where nothing would ever offer the certificate.
func TestDownstreamTLSIsRefusedWhereNothingTerminatesIt(t *testing.T) {
	serverTLS, _ := relayCert(t)
	base := proxy.Config{Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1", DownstreamTLS: serverTLS}

	for _, p := range []inspect.Protocol{inspect.HTTP, inspect.Postgres, inspect.ClickHouse} {
		cfg := base
		cfg.Protocol = p
		if _, err := proxy.NewServer(cfg); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	cfg := base
	cfg.Protocol = inspect.MySQL
	if _, err := proxy.NewServer(cfg); err == nil {
		t.Error("mysql: downstream TLS accepted on a lane that never terminates it")
	}
}
