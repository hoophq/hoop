package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"
)

// HTTP/2 on an http lane is terminated here and bridged into the HTTP/1
// relay, so that everything the relay enforces — policy, deny frames,
// masking, audit, identity — applies to it unchanged.
//
// # Why a bridge, not a second inspection path
//
// The HTTP codec decodes HTTP/1.x only. An h2 client that reached handle
// would be refused by the codec as malformed, which is correct and useless:
// kubectl speaks h2 to anything that offers it over ALPN, and a GKE Connect
// Gateway client behind a transparent MITM has no way to be told not to.
// Teaching the codec HPACK and stream framing would duplicate the relay's
// enforcement for a second wire format, and every masking or deny bug would
// then have to be found twice. The grpc lane already made the other choice
// for the same reason (ADR-0013): terminate HTTP/2 in-process with net/http.
//
// So each h2 stream is re-serialized as one HTTP/1.1 request by a
// ReverseProxy whose Transport dials net.Pipe, and the far end of that pipe
// is an ordinary relay connection: s.handle, its own session, its own Gate.
// The relay cannot tell a bridged connection from a client that spoke
// HTTP/1.1 itself, which is the point.
//
// # What is not bridged
//
// HTTP/1.1 Upgrade cannot be expressed on h2 (the Connection header is
// forbidden there), and extended CONNECT (RFC 8441) would need a relay leg
// that is not HTTP/1 at all, so CONNECT is answered 501. kubectl exec,
// attach and port-forward negotiate HTTP/1.1 upgrades, which a client only
// attempts on an HTTP/1.1 connection, and those take the plain relay path.

// h2Preface is the connection preface every HTTP/2 client sends before
// anything else (RFC 9113, section 3.4). On a plaintext lane it is the only
// thing that tells a prior-knowledge h2c client from an HTTP/1 one.
var h2Preface = []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")

// maxH2HeaderBytes matches the HTTP codec's head limit (maxRequestHead), so
// the h2 side refuses no header block the relay would have accepted.
const maxH2HeaderBytes = maxRequestHead

// serveHTTPLane decides, for one accepted connection on an http lane,
// whether it speaks HTTP/2 or HTTP/1, and hands it to the h2 bridge or to
// handle accordingly. It returns when the connection is done; Serve's
// untrack closes the raw conn after that.
//
// With DownstreamTLS the decision is ALPN: the relay offers h2 and
// http/1.1 and the client picks. Without it, the only signal is the
// prior-knowledge preface. HTTP/1.1's own Upgrade: h2c is not honoured;
// such a request goes through the relay like any other.
//
// Both ways block on the client's first bytes under DialTimeout, before the
// upstream is dialled. HTTP has no server greeting, so that is the
// protocol's own ordering, and a client that connects and says nothing
// closes at the deadline with no session, as it would under identity_header.
func (s *Server) serveHTTPLane(ctx context.Context, conn net.Conn, rules *laneRules) {
	peer := conn.RemoteAddr().String()
	if s.h2.tlsCfg != nil {
		tc := tls.Server(conn, s.h2.tlsCfg)
		if err := handshakeDownstream(ctx, tc, s.cfg.DialTimeout); err != nil {
			// A failed handshake never became a session, so it is not an
			// audit event; it closes quietly, like any other negotiation
			// failure on this relay.
			s.log.Debug("downstream TLS handshake failed", "peer", peer, "error", err)
			return
		}
		if tc.ConnectionState().NegotiatedProtocol == "h2" {
			s.h2.run(ctx, tc)
			return
		}
		// handle's negotiateDownstream passes http through untouched, so
		// the relay reads the plaintext inside this TLS session.
		s.handle(ctx, tc, rules)
		return
	}

	c, isH2, err := sniffH2Preface(conn, s.cfg.DialTimeout)
	if err != nil {
		s.log.Debug("first bytes not read", "peer", peer, "error", err)
		return
	}
	if isH2 {
		s.h2.run(ctx, c)
		return
	}
	s.handle(ctx, c, rules)
}

// handshakeDownstream runs the server TLS handshake under the dial timeout
// and clears the deadline after, so the relay's own IdleTimeout governs the
// session from there, as dialUpstream does for the other leg.
func handshakeDownstream(ctx context.Context, tc *tls.Conn, timeout time.Duration) error {
	if timeout > 0 {
		if err := tc.SetDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
	}
	if err := tc.HandshakeContext(ctx); err != nil {
		return err
	}
	return tc.SetDeadline(time.Time{})
}

// sniffH2Preface reads the start of the connection WITHOUT consuming it and
// reports whether it is the HTTP/2 client preface.
//
// A read shorter than the preface is not a verdict: while every byte so far
// agrees with the preface it keeps reading, and it stops the moment one
// byte disagrees. An HTTP/1 request therefore usually decides on its first
// byte, and PROPFIND, the one method that shares the preface's "PR", on its
// third.
//
// It reads up to initialHeadBuffer in one go rather than exactly the 24
// bytes it needs, and replays everything it read. The first Read the gate
// sees is then whatever the first socket read returned, the same chunk it
// would have seen without the sniff, rather than a 24-byte fragment of a
// request head.
func sniffH2Preface(conn net.Conn, timeout time.Duration) (net.Conn, bool, error) {
	if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, false, err
		}
	}
	buf := make([]byte, 0, initialHeadBuffer)
	var isH2 bool
	for {
		n, err := conn.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		k := min(len(buf), len(h2Preface))
		if !bytes.Equal(buf[:k], h2Preface[:k]) {
			break // decided: not h2
		}
		if k == len(h2Preface) {
			isH2 = true
			break
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, false, fmt.Errorf("client closed before sending a request: %w", err)
			}
			return nil, false, fmt.Errorf("reading the client's first bytes: %w", err)
		}
	}
	// A read that returned the deciding bytes together with an error is
	// decided anyway: the error comes back on the relay's next Read, which
	// is where the relay expects to see it.
	if timeout > 0 {
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return nil, false, err
		}
	}
	return &prefixConn{Conn: conn, prefix: buf}, isH2, nil
}

// h2Lane is the lane-wide HTTP/2 server. One per http lane: it holds no
// per-connection state itself, and every accepted h2 connection carries its
// own h2Bridge into it.
type h2Lane struct {
	s      *Server
	srv    *http.Server
	ln     *memListener
	tlsCfg *tls.Config // nil on a plaintext lane

	// bridges maps each connection the lane's server is serving to its
	// bridge, for ConnContext and ConnState. It is a map and not a wrapper
	// type around the connection because net/http up to Go 1.26 serves h2
	// only on a connection whose dynamic type IS *tls.Conn: a wrapper
	// embedding one is read as plaintext HTTP/1, which this server refuses,
	// so every TLS h2 client got its connection closed.
	mu      sync.Mutex
	bridges map[net.Conn]*h2Bridge
}

// h2BridgeKey is the connection-context key for the h2Bridge that owns a
// request, set in ConnContext and read by bridgeTransport.
type h2BridgeKey struct{}

func newH2Lane(s *Server) *h2Lane {
	l := &h2Lane{s: s, ln: newMemListener(), bridges: map[net.Conn]*h2Bridge{}}
	protocols := new(http.Protocols)
	if s.cfg.DownstreamTLS != nil {
		// Cloned so the operator's config is not mutated: the relay owns
		// the ALPN list on this lane, and the config may be shared.
		l.tlsCfg = s.cfg.DownstreamTLS.Clone()
		l.tlsCfg.NextProtos = []string{"h2", "http/1.1"}
		protocols.SetHTTP2(true)
	} else {
		protocols.SetUnencryptedHTTP2(true)
	}
	// Only confirmed h2 reaches this server, so HTTP1 stays off: an
	// HTTP/1 request on it would bypass the relay entirely.

	// net/http's own complaints (a client's protocol error, a reset
	// stream) are the client's business, not an operator's.
	errLog := slog.NewLogLogger(s.log.Handler(), slog.LevelDebug)
	rp := &httputil.ReverseProxy{
		Rewrite:   rewriteBridged,
		Transport: bridgeTransport{},
		// -1 flushes after every write. kubectl watch and logs -f stream
		// for as long as the command runs; a buffered response would
		// arrive when the stream ends, which is never.
		FlushInterval: -1,
		ErrorLog:      errLog,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			level := slog.LevelWarn
			if errors.Is(err, context.Canceled) {
				level = slog.LevelDebug // the client reset the stream
			}
			s.log.Log(r.Context(), level, "h2 request not relayed",
				"peer", r.RemoteAddr, "method", r.Method, "authority", r.Host, "error", err)
			http.Error(w, "hoop-inspect: the request could not be relayed", http.StatusBadGateway)
		},
	}
	l.srv = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				http.Error(w, "hoop-inspect: CONNECT over HTTP/2 is not supported; use HTTP/1.1",
					http.StatusNotImplemented)
				return
			}
			rp.ServeHTTP(w, r)
		}),
		Protocols:         protocols,
		ReadHeaderTimeout: s.cfg.DialTimeout,
		// The lane's idle knob, applied where HTTP/2 can see idleness: a
		// connection with no open stream. Each bridged relay connection
		// also applies it to itself in handle.
		IdleTimeout:    s.cfg.IdleTimeout,
		MaxHeaderBytes: maxH2HeaderBytes,
		ErrorLog:       errLog,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if b := l.bridge(c, false); b != nil {
				return context.WithValue(ctx, h2BridgeKey{}, b)
			}
			return ctx
		},
		// net/http reports StateClosed once per served connection, h2
		// included, which is the one place the end of a client
		// connection is observable from outside the server.
		ConnState: func(c net.Conn, st http.ConnState) {
			if st != http.StateClosed && st != http.StateHijacked {
				return
			}
			if b := l.bridge(c, true); b != nil {
				b.finish()
			}
		},
	}
	return l
}

// serve runs the lane's HTTP/2 server until close. An unexpected return
// closes the listener, so every later h2 client is refused at once instead
// of queueing behind a server that is gone.
func (l *h2Lane) serve() {
	err := l.srv.Serve(l.ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		l.s.log.Error("h2 server stopped; h2 clients on this lane are refused", "error", err)
	}
	_ = l.ln.Close()
}

// close stops the h2 server and every connection it serves. The listener is
// closed explicitly too, because Serve may not have registered it yet.
func (l *h2Lane) close() {
	_ = l.ln.Close()
	_ = l.srv.Close()
}

// bridge returns the bridge registered for c, nil when none; forget drops
// the registration, for a connection the server is done with.
func (l *h2Lane) bridge(c net.Conn, forget bool) *h2Bridge {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.bridges[c]
	if forget {
		delete(l.bridges, c)
	}
	return b
}

// run hands one h2 connection to the lane's server and waits for it to end.
// conn goes to the server as it is, a *tls.Conn on a TLS lane, for the
// reason on h2Lane.bridges.
func (l *h2Lane) run(ctx context.Context, conn net.Conn) {
	b := newH2Bridge(ctx, l.s, conn)
	l.mu.Lock()
	l.bridges[conn] = b
	l.mu.Unlock()
	l.s.log.Debug("h2 connection bridged", "peer", conn.RemoteAddr().String())
	if err := l.ln.push(conn); err != nil {
		l.bridge(conn, true)
		b.finish()
		return
	}
	select {
	case <-b.done:
	case <-l.ln.closed:
	}
}

// rewriteBridged makes the HTTP/1.1 request the relay sees the one the
// client sent, as far as HTTP/1.1 can express it.
//
// The client's :authority stays the Host, because the upstream (a GKE
// Connect Gateway, an API server) routes on it and a policy reads it. No
// X-Forwarded-* and no Via are added: this is one relay, not two, and the
// relay stamps its own Via further in, where a second one from here would
// look like a loop to its own detector. Rewrite strips the client's
// forwarding headers and query parameters it cannot parse before it runs;
// both are put back, because the HTTP/1 path forwards them untouched and a
// request must not change meaning with the protocol it arrived on.
func rewriteBridged(pr *httputil.ProxyRequest) {
	pr.Out.URL.Scheme = "http"
	pr.Out.URL.Host = pr.In.Host
	pr.Out.Host = pr.In.Host
	pr.Out.URL.RawQuery = pr.In.URL.RawQuery
	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if v, ok := pr.In.Header[h]; ok {
			pr.Out.Header[h] = v
		}
	}
}

// bridgeTransport routes each request to the Transport of the h2
// connection it arrived on. The ReverseProxy is lane-wide; the pool is not.
type bridgeTransport struct{}

func (bridgeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b, ok := req.Context().Value(h2BridgeKey{}).(*h2Bridge)
	if !ok {
		return nil, errors.New("sidecar/proxy: h2 request carries no bridge")
	}
	return b.transport.RoundTrip(req)
}

// h2Bridge is one accepted h2 connection's way into the relay.
//
// # One pool per client connection
//
// Its Transport pools the bridged relay connections, and it belongs to this
// client connection alone. A lane-wide pool would let a relay connection
// opened for one client carry the next request of another: the relay pins
// a session's identity to its connection, so the second client's request
// would be audited, and judged, as the first client's.
//
// # Counting
//
// Every bridged relay connection is tracked like an accepted one, so Close
// tears it down and Stats reports it active, and counted in s.bridged, so
// the MaxConns check at accept leaves it out. The client connection was
// admitted against MaxConns once; its streams are bounded by HTTP/2's
// concurrent-stream limit (net/http's default, 250), and refusing a
// kubectl its second stream because the lane is full would read as a hung
// request rather than a refused connection.
//
// # Rules
//
// Each bridged relay connection loads the lane's rules when it is dialled,
// not when the client connected. It is its own session with its own Gate,
// which is the unit SwapRules is defined on; one Gate never mixes
// generations either way.
type h2Bridge struct {
	s *Server
	// ctx is Serve's. A pooled relay connection outlives the request that
	// dialled it, so it must not end with that request's context.
	ctx           context.Context
	remote, local net.Addr
	transport     *http.Transport

	mu     sync.Mutex
	pipes  map[*bridgePipe]struct{}
	closed bool

	once sync.Once
	done chan struct{}
}

func newH2Bridge(ctx context.Context, s *Server, client net.Conn) *h2Bridge {
	b := &h2Bridge{
		s:      s,
		ctx:    ctx,
		remote: client.RemoteAddr(),
		local:  client.LocalAddr(),
		pipes:  map[*bridgePipe]struct{}{},
		done:   make(chan struct{}),
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	b.transport = &http.Transport{
		// Proxy is deliberately nil: the dial goes to a pipe, and an
		// HTTP(S)_PROXY in the environment must not redirect it.
		DialContext: b.dial,
		Protocols:   protocols,
		// The client asked for whatever encoding it asked for. A
		// transparently gunzipped response would reach it with the
		// Content-Encoding stripped, and the relay would inspect a body
		// the client never requested.
		DisableCompression: true,
	}
	return b
}

// errBridgeClosed refuses a dial after the client connection ended.
var errBridgeClosed = errors.New("sidecar/proxy: h2 connection closed")

// dial opens one relay connection for this client: a net.Pipe whose far
// end is served by handle exactly as an accepted HTTP/1 connection is.
func (b *h2Bridge) dial(context.Context, string, string) (net.Conn, error) {
	near, far := net.Pipe()
	relayEnd := &bridgedConn{Conn: far, remote: b.remote, local: b.local}
	pipe := &bridgePipe{Conn: near, b: b}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		near.Close()
		far.Close()
		return nil, errBridgeClosed
	}
	b.pipes[pipe] = struct{}{}
	b.mu.Unlock()

	if !b.s.trackBridged(relayEnd) {
		pipe.Close()
		far.Close()
		return nil, net.ErrClosed
	}
	go func() {
		defer b.s.untrackBridged(relayEnd)
		b.s.handle(b.ctx, relayEnd, b.s.rules.Load())
	}()
	return pipe, nil
}

// finish ends every relay connection this client opened. Called once the
// client connection is gone: an in-flight response has nowhere left to go,
// and an idle pooled one would otherwise hold a session and an upstream
// socket open until the relay's IdleTimeout, which is off by default.
func (b *h2Bridge) finish() {
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		pipes := make([]*bridgePipe, 0, len(b.pipes))
		for p := range b.pipes {
			pipes = append(pipes, p)
		}
		b.mu.Unlock()
		for _, p := range pipes {
			_ = p.Close()
		}
		b.transport.CloseIdleConnections()
		close(b.done)
	})
}

// trackBridged tracks a bridged relay connection. It refuses once Close has
// started, because a connection added after Close took its snapshot would
// never be closed.
//
// bridged moves before active on the way in and after it on the way out,
// so a concurrent MaxConns check sees at worst one connection fewer, never
// one more, than there are client connections.
func (s *Server) trackBridged(c net.Conn) bool {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return false
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	s.bridged.Add(1)
	s.active.Add(1)
	s.total.Add(1)
	return true
}

func (s *Server) untrackBridged(c net.Conn) {
	s.untrack(c)
	s.bridged.Add(-1)
}

// bridgePipe is the Transport's end of a bridged relay connection. It
// forgets itself in its bridge when the Transport closes it.
type bridgePipe struct {
	net.Conn
	b *h2Bridge
}

func (p *bridgePipe) Close() error {
	p.b.mu.Lock()
	delete(p.b.pipes, p)
	p.b.mu.Unlock()
	return p.Conn.Close()
}

// bridgedConn is the relay's end of a bridged connection. It reports the h2
// client's addresses instead of net.Pipe's, so the session's PeerAddr and
// every log line name the real peer. An IdentityFn sees this connection,
// not the client's *tls.Conn.
type bridgedConn struct {
	net.Conn
	remote, local net.Addr
}

func (c *bridgedConn) RemoteAddr() net.Addr { return c.remote }
func (c *bridgedConn) LocalAddr() net.Addr  { return c.local }

// memListener is the in-process listener the lane's h2 server accepts
// from. The relay's own listener stays the only socket: serveHTTPLane
// pushes the connections it identified as h2.
type memListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *memListener) push(c net.Conn) error {
	select {
	case l.conns <- c:
		return nil
	case <-l.closed:
		return net.ErrClosed
	}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr{} }

type memAddr struct{}

func (memAddr) Network() string { return "memory" }
func (memAddr) String() string  { return "h2-bridge" }
