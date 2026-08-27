// Package proxy is a TCP relay that inspects both directions through a Gate.
//
// It is the thin transport shell around the library: accept a connection,
// dial the upstream, and pump bytes through a Gate in each direction. The
// interesting behavior lives in gate/, codec/ and policy/. This file exists
// so a deployment gets a process instead of an integration project.
//
// # Scope
//
// One listener, one upstream, one protocol. It balances no load, routes
// nothing, and terminates no downstream TLS. A deployment that needs routing
// puts Envoy in front, the topology this library assumes throughout: Envoy
// owns the network path and hoop-inspect owns the payload.
//
// Upstream TLS IS supported, because a proxy that can only talk plaintext to
// the database is unusable in the environments that care about any of this.
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
)

// DenyWriter renders a policy denial in the wire protocol's own error frame.
//
// Without one, a denial drops the connection and the user files a support
// ticket. With one they read "destructive statements are not permitted on
// appdb" in their psql session and fix it themselves. That is why the hook is
// first-class.
//
// Deny returns the bytes to send to the client before closing. Returning nil
// closes without explanation.
type DenyWriter interface {
	// Deny renders message for the given protocol and direction.
	Deny(proto inspect.Protocol, dir inspect.Direction, message string) []byte
}

// Config configures a Server.
type Config struct {
	// Listen is the address to accept on ("0.0.0.0:15432", or a path when
	// Network is "unix").
	Listen string

	// Network is "tcp" (default) or "unix". Pick a unix socket for a sandbox
	// with no network egress, where filesystem permissions gate who can reach
	// the proxy at all.
	Network string

	// Upstream is the address to forward to.
	Upstream string

	// UpstreamTLS, when non-nil, wraps the upstream connection.
	UpstreamTLS *tls.Config

	// DownstreamTLS, when non-nil, lets the relay terminate the CLIENT's TLS.
	//
	// Only pgwire uses it today, and only because pgwire leaves no one else
	// able to: its TLS is negotiated in-band with an 8-byte SSLRequest, so a
	// plain TLS listener in front cannot terminate it, and Envoy's own
	// postgres filter is contrib-only, marked work-in-progress, and gives up
	// permanently the moment a client asks for GSS encryption.
	//
	// Leaving it nil keeps the documented posture — the relay terminates no
	// downstream TLS and something in front owns that leg. Setting it moves
	// that boundary here for one lane.
	DownstreamTLS *tls.Config

	// Protocol selects the codec.
	Protocol inspect.Protocol

	// Connection is the operator-facing resource name recorded in audit.
	Connection string

	// Policy, Audit, Masker are passed through to each connection's Gate.
	Policy policy.Evaluator
	Audit  audit.Sink
	Masker gate.Masker

	// FailOnAuditError makes a failed audit write deny the statement.
	FailOnAuditError bool

	// DenyWriter renders denials in-protocol. Optional.
	DenyWriter DenyWriter

	// IdentityFn derives the caller's identity from the accepted connection.
	// Optional; the default records only the peer address, producing an
	// anonymous session.
	//
	// Per-user deployments hook in here: an Envoy sidecar that has already
	// authenticated the user passes the subject through a header, mTLS peer
	// cert, or a credential token, and this function extracts it.
	IdentityFn func(net.Conn) session.Identity

	// CodecFactory overrides how each connection's Gate builds its codecs.
	// Nil uses the registry. See gate.Config.CodecFactory: it exists so a
	// lane can turn on HTTP body capture, which the argument-free registry
	// factory cannot express.
	CodecFactory func() inspect.Codec

	// DialTimeout bounds the upstream connect. Default 10s.
	DialTimeout time.Duration

	// IdleTimeout closes a connection with no traffic in either direction.
	// Zero disables it. Interactive sessions idle between keystrokes, so a
	// short value here breaks psql; that is why the default is off.
	IdleTimeout time.Duration

	// MaxConns bounds concurrent connections. Zero means unlimited.
	MaxConns int

	// Logger receives operational events. Defaults to slog.Default().
	Logger *slog.Logger
}

// Server accepts connections and relays them through a Gate.
type Server struct {
	cfg      Config
	log      *slog.Logger
	listener net.Listener

	mu      sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool

	active atomic.Int64
	total  atomic.Int64
	denied atomic.Int64
}

// NewServer validates the config and returns a Server. It does not listen
// until Serve is called.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Listen == "" {
		return nil, errors.New("sidecar/proxy: no listen address")
	}
	if cfg.Upstream == "" {
		return nil, errors.New("sidecar/proxy: no upstream address")
	}
	if cfg.Protocol == "" {
		return nil, errors.New("sidecar/proxy: no protocol")
	}
	// Fail at construction rather than on the first connection: an
	// unsupported protocol is a config error and must surface at startup.
	if _, err := inspect.New(cfg.Protocol); err != nil {
		return nil, fmt.Errorf("sidecar/proxy: %w", err)
	}
	if cfg.Network == "" {
		cfg.Network = "tcp"
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{
		cfg:   cfg,
		log:   cfg.Logger,
		conns: map[net.Conn]struct{}{},
	}, nil
}

// reclaimStaleSocket removes a leftover unix socket file so a restart can
// bind.
//
// Go unlinks the socket when the listener closes, so an orderly shutdown
// leaves nothing behind. A SIGKILL, an OOM kill or `docker kill` skips that,
// and the file outlives the process: every later start then fails with
// "bind: address already in use" and the relay never comes back without
// someone deleting a file by hand. That is a bad way to spend an outage.
//
// It only unlinks a socket nothing answers on. A successful dial means a live
// process owns this path, so the file stays and net.Listen reports the
// conflict, which is the correct outcome: two relays sharing one socket would
// split a client's connections between them at random.
func (s *Server) reclaimStaleSocket() error {
	if s.cfg.Network != "unix" {
		return nil
	}
	if _, err := os.Stat(s.cfg.Listen); err != nil {
		return nil // nothing there, or unreadable; let net.Listen report it
	}

	// A short timeout, because this runs on the startup path against a local
	// filesystem socket: it either answers immediately or it is dead.
	if c, err := net.DialTimeout("unix", s.cfg.Listen, 100*time.Millisecond); err == nil {
		c.Close()
		return fmt.Errorf("sidecar/proxy: %s is a live socket; another relay is already listening on it",
			s.cfg.Listen)
	}

	if err := os.Remove(s.cfg.Listen); err != nil {
		return fmt.Errorf("sidecar/proxy: removing stale socket %s: %w", s.cfg.Listen, err)
	}
	s.log.Warn("removed a stale socket file left by an unclean shutdown",
		"listen", s.cfg.Listen)
	return nil
}

// Serve listens and accepts until ctx is cancelled or Close is called.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.reclaimStaleSocket(); err != nil {
		return err
	}

	ln, err := net.Listen(s.cfg.Network, s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("sidecar/proxy: listen %s %s: %w",
			s.cfg.Network, s.cfg.Listen, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	s.log.Info("hoop-inspect listening",
		"network", s.cfg.Network,
		"listen", s.cfg.Listen,
		"upstream", s.cfg.Upstream,
		"protocol", string(s.cfg.Protocol),
		"connection", s.cfg.Connection)

	// Close the listener on context cancellation so Accept unblocks.
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closing := s.closing
			s.mu.Unlock()
			if closing || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("sidecar/proxy: accept: %w", err)
		}

		if s.cfg.MaxConns > 0 && int(s.active.Load()) >= s.cfg.MaxConns {
			// Refuse rather than queue: an unbounded accept queue turns a
			// connection flood into memory exhaustion, and a closed connection
			// gives the client a faster, clearer failure.
			s.log.Warn("connection refused, at capacity", "max_conns", s.cfg.MaxConns)
			_ = conn.Close()
			continue
		}

		s.track(conn)
		go func() {
			defer s.untrack(conn)
			s.handle(ctx, conn)
		}()
	}
}

// Addr returns the bound address, or nil before Serve has listened.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Stats reports counters for a health or metrics endpoint.
func (s *Server) Stats() (active, total, denied int64) {
	return s.active.Load(), s.total.Load(), s.denied.Load()
}

// Close stops accepting and closes every live connection. Idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	ln := s.listener
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	return nil
}

func (s *Server) track(c net.Conn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	s.active.Add(1)
	s.total.Add(1)
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	s.active.Add(-1)
	_ = c.Close()
}

// handle relays one connection.
func (s *Server) handle(ctx context.Context, client net.Conn) {
	identity := session.Identity{PeerAddr: client.RemoteAddr().String()}
	if s.cfg.IdentityFn != nil {
		identity = s.cfg.IdentityFn(client)
		if identity.PeerAddr == "" {
			identity.PeerAddr = client.RemoteAddr().String()
		}
	}

	sess := session.New(s.cfg.Protocol, identity)
	sess.Connection = s.cfg.Connection
	sess.Upstream = s.cfg.Upstream

	log := s.log.With("session", string(sess.ID), "principal", identity.Principal())

	g, err := gate.New(sess, gate.Config{
		Protocol:         s.cfg.Protocol,
		Policy:           s.cfg.Policy,
		Audit:            s.cfg.Audit,
		Masker:           s.cfg.Masker,
		FailOnAuditError: s.cfg.FailOnAuditError,
		CodecFactory:     s.cfg.CodecFactory,
	})
	if err != nil {
		log.Error("gate setup failed", "error", err)
		return
	}
	if err := g.Start(ctx); err != nil {
		log.Warn("session start not recorded", "error", err)
	}
	defer func() {
		if err := g.Close(ctx); err != nil {
			log.Warn("session end not recorded", "error", err)
		}
		stmts, denied := g.Stats()
		log.Info("session closed",
			"statements", stmts, "denied", denied,
			"duration", sess.Duration().String())
	}()

	upstream, err := s.dialUpstream(ctx)
	if err != nil {
		log.Error("upstream dial failed", "upstream", s.cfg.Upstream, "error", err)
		if s.cfg.Audit != nil {
			_ = s.cfg.Audit.Write(ctx, audit.ErrorEvent(sess, err))
		}
		return
	}
	defer upstream.Close()

	// Answer the pgwire pre-startup exchange before the gate sees a byte. It
	// decides whether this session is inspectable at all: a client asking for
	// GSS encryption is refused here, or the gate would spend the connection
	// reading ciphertext and reporting no statements.
	//
	// AFTER the upstream dial, deliberately. pgwire has no server greeting, so
	// this blocks until the client speaks — and an upstream that is down
	// should be discovered and recorded even when the client never does.
	//
	// A negotiation failure is neither a policy denial nor a protocol error
	// worth an audit event: it is a connection that never became a session.
	// It closes quietly, the same as a client hanging up mid-handshake.
	client, claimedUser, negErr := negotiateDownstream(
		client, s.cfg.Protocol, s.cfg.DownstreamTLS, s.cfg.DialTimeout)
	if negErr != nil {
		log.Debug("downstream negotiation failed", "error", negErr)
		return
	}

	// pgwire names its user in cleartext in the StartupMessage, so this lane
	// can fill the actor column that every other one leaves anonymous. MSSQL
	// cannot: under integrated auth the name lives inside the encrypted
	// ticket, and reading it would mean implementing Kerberos.
	//
	// Written here, before the pumps start, so the two pump goroutines only
	// ever read it. An IdentityFn the operator supplied wins, because it saw
	// a verified subject from the fronting proxy and this is a client claim.
	if claimedUser != "" && sess.Identity.Subject == "" {
		sess.Identity.Subject = claimedUser
		log = log.With("principal", claimedUser)
	}

	log.Info("session opened", "upstream", s.cfg.Upstream)

	// Both directions run concurrently; the first to finish tears down the
	// other by closing its peer, which unblocks the pending Read.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer upstream.Close()
		s.pump(ctx, g, client, upstream, inspect.FromClient, log)
	}()
	go func() {
		defer wg.Done()
		defer client.Close()
		s.pump(ctx, g, upstream, client, inspect.FromServer, log)
	}()

	wg.Wait()
}

// dialUpstream connects to the backend, negotiating TLS when configured.
//
// The TLS handshake is protocol-aware: see startTLS. On any TLS failure the
// raw connection is closed before returning, so a refused handshake does not
// leak a socket per attempt.
func (s *Server) dialUpstream(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: s.cfg.DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", s.cfg.Upstream)
	if err != nil || s.cfg.UpstreamTLS == nil {
		return conn, err
	}

	// The dial timeout has to cover the negotiation too: without a deadline a
	// server that accepts the TCP connection and then says nothing would hang
	// this goroutine for as long as the client waits.
	if s.cfg.DialTimeout > 0 {
		if derr := conn.SetDeadline(time.Now().Add(s.cfg.DialTimeout)); derr != nil {
			conn.Close()
			return nil, derr
		}
	}

	tc, err := startTLS(conn, s.cfg.Upstream, s.cfg.Protocol, s.cfg.UpstreamTLS)
	if err != nil {
		conn.Close()
		return nil, err
	}
	// Clear the handshake deadline: the relay's own IdleTimeout governs the
	// session from here, and leaving this set would kill a long query.
	if err := tc.SetDeadline(time.Time{}); err != nil {
		tc.Close()
		return nil, err
	}
	return tc, nil
}

// pump copies src -> dst, running every chunk through the gate first.
//
// On a denial it writes the in-protocol error (when a DenyWriter is
// configured) and returns, which closes both halves via the deferred closes
// in handle. It forwards nothing.
func (s *Server) pump(
	ctx context.Context,
	g *gate.Gate,
	src, dst net.Conn,
	dir inspect.Direction,
	log *slog.Logger,
) {
	// A re-framing codec holds rows back until their result set ends. Every
	// exit from this loop must release them, or the client silently loses the
	// tail of its output, which looks like a truncated result rather than a
	// masking bug. Only the server direction can hold anything.
	if dir == inspect.FromServer {
		defer func() {
			if tail := g.FlushResponse(); len(tail) > 0 {
				_, _ = dst.Write(tail)
			}
		}()
	}
	// Reassembly of the server's SASL offer is needed only where the relay
	// terminated the upstream TLS the offer would have bound to. Every other
	// listener keeps the copy path untouched: nil here means pump never
	// looks at a frame boundary.
	var sasl *saslReassembler
	if dir == inspect.FromServer &&
		s.cfg.Protocol == inspect.Postgres &&
		s.cfg.UpstreamTLS != nil {
		sasl = &saslReassembler{}
	}

	buf := make([]byte, 32*1024)
	for {
		if s.cfg.IdleTimeout > 0 {
			_ = src.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			// The server negotiated TLS with US, not with the client, so it
			// may offer channel binding the client cannot satisfy. Drop that
			// mechanism before anything else looks at the bytes; see
			// stripChannelBinding for why relaying it fails the connection.
			//
			// sasl holds a partial offer rather than forwarding it: the
			// rewrite needs a whole frame and a Read boundary can land
			// anywhere. It retires itself after the first complete
			// authentication message, so nothing past login is buffered.
			if sasl != nil {
				stripped, changed := sasl.feed(chunk)
				if changed {
					log.Debug("removed SCRAM channel binding from the server's SASL offer",
						"reason", "upstream TLS terminates here, so the client cannot bind to it")
				}
				if len(stripped) == 0 {
					if readErr != nil {
						// Held bytes go nowhere: an incomplete frame is not
						// safe to rewrite and not useful to forward.
						log.Debug("server ended mid-authentication",
							"error", readErr, "held", len(chunk))
						return
					}
					continue // a partial frame; wait for the rest
				}
				chunk = stripped
			}

			var d gate.Decision
			if dir == inspect.FromClient {
				d = g.Request(ctx, chunk)
			} else {
				d = g.Response(ctx, chunk)
			}

			if d.Err != nil {
				log.Warn("inspection reported an error", "direction", string(dir), "error", d.Err)
			}

			if !d.Allowed {
				s.denied.Add(1)
				log.Info("statement denied",
					"direction", string(dir), "rule", d.Rule, "message", d.Message)

				// Deliver the reason to the CLIENT, whichever direction the
				// denial came from: on a response denial the offending bytes
				// travel toward the client, so the client needs to know why
				// the connection ended.
				if s.cfg.DenyWriter != nil {
					target := dst
					if dir == inspect.FromClient {
						target = src // the client is the source of a request
					}
					if frame := s.cfg.DenyWriter.Deny(s.cfg.Protocol, dir, d.Message); len(frame) > 0 {
						_ = target.SetWriteDeadline(time.Now().Add(5 * time.Second))
						_, _ = target.Write(frame)
					}
				}
				return
			}

			if len(d.Payload) > 0 {
				if _, err := dst.Write(d.Payload); err != nil {
					if !isClosed(err) {
						log.Debug("forward failed", "direction", string(dir), "error", err)
					}
					return
				}
			}
		}

		if readErr != nil {
			if readErr != io.EOF && !isClosed(readErr) {
				log.Debug("read ended", "direction", string(dir), "error", readErr)
			}
			return
		}
	}
}

// isClosed suppresses the routine teardown races between the two pump
// goroutines closing each other's peer.
func isClosed(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}
