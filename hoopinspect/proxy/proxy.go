// Package proxy is a TCP relay that inspects both directions through a Gate.
//
// It is the thin transport shell around the library: accept a connection,
// dial the upstream, and pump bytes through a Gate in each direction. The
// interesting behavior all lives in gate/, codec/ and policy/ — this file
// exists so a deployment gets a process instead of an integration project.
//
// # What it deliberately is not
//
// Not a load balancer, not a router, not a TLS terminator for the downstream.
// One listener, one upstream, one protocol. A deployment that needs routing
// puts Envoy in front, which is the topology this whole library assumes:
// Envoy owns the network path and hoop-inspect owns the payload.
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/gate"
	"github.com/hoophq/hoopinspect/policy"
	"github.com/hoophq/hoopinspect/session"
)

// DenyWriter renders a policy denial in the wire protocol's own error frame.
//
// Without one, a denial is a dropped connection and the user files a support
// ticket. With one they read "destructive statements are not permitted on
// appdb" in their psql session and fix it themselves. That difference is a
// product feature, not a nicety, which is why it is a first-class hook.
//
// Write returns the bytes to send to the client before closing. Returning nil
// closes without explanation.
type DenyWriter interface {
	// Deny renders message for the given protocol and direction.
	Deny(proto hoopinspect.Protocol, dir hoopinspect.Direction, message string) []byte
}

// Config configures a Server.
type Config struct {
	// Listen is the address to accept on ("0.0.0.0:15432", or a path when
	// Network is "unix").
	Listen string

	// Network is "tcp" (default) or "unix". A unix socket is the option for
	// a sandbox with no network egress, where filesystem permissions gate
	// who can reach the proxy at all.
	Network string

	// Upstream is the address to forward to.
	Upstream string

	// UpstreamTLS, when non-nil, wraps the upstream connection.
	UpstreamTLS *tls.Config

	// Protocol selects the codec.
	Protocol hoopinspect.Protocol

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
	// Optional; the default records only the peer address, which produces an
	// anonymous session.
	//
	// This is the seam for per-user deployments: an Envoy sidecar that has
	// already authenticated the user passes the subject through a header,
	// mTLS peer cert, or a credential token, and this function extracts it.
	IdentityFn func(net.Conn) session.Identity

	// DialTimeout bounds the upstream connect. Default 10s.
	DialTimeout time.Duration

	// IdleTimeout closes a connection with no traffic in either direction.
	// Zero disables it. Interactive sessions idle between keystrokes, so a
	// short value here breaks psql; the default is off for that reason.
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
		return nil, errors.New("hoopinspect/proxy: no listen address")
	}
	if cfg.Upstream == "" {
		return nil, errors.New("hoopinspect/proxy: no upstream address")
	}
	if cfg.Protocol == "" {
		return nil, errors.New("hoopinspect/proxy: no protocol")
	}
	// Fail at construction rather than on the first connection: an
	// unsupported protocol is a config error and must surface at startup.
	if _, err := hoopinspect.New(cfg.Protocol); err != nil {
		return nil, fmt.Errorf("hoopinspect/proxy: %w", err)
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

// Serve listens and accepts until ctx is cancelled or Close is called.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen(s.cfg.Network, s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("hoopinspect/proxy: listen %s %s: %w",
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
			return fmt.Errorf("hoopinspect/proxy: accept: %w", err)
		}

		if s.cfg.MaxConns > 0 && int(s.active.Load()) >= s.cfg.MaxConns {
			// Refuse rather than queue: an unbounded accept queue turns a
			// connection flood into memory exhaustion, and the client gets a
			// faster, clearer failure from a closed connection.
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

	log.Info("session opened", "upstream", s.cfg.Upstream)

	// Both directions run concurrently; the first to finish tears down the
	// other by closing its peer, which unblocks the pending Read.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer upstream.Close()
		s.pump(ctx, g, client, upstream, hoopinspect.FromClient, log)
	}()
	go func() {
		defer wg.Done()
		defer client.Close()
		s.pump(ctx, g, upstream, client, hoopinspect.FromServer, log)
	}()

	wg.Wait()
}

func (s *Server) dialUpstream(ctx context.Context) (net.Conn, error) {
	d := &net.Dialer{Timeout: s.cfg.DialTimeout}
	if s.cfg.UpstreamTLS != nil {
		return tls.DialWithDialer(d, "tcp", s.cfg.Upstream, s.cfg.UpstreamTLS)
	}
	return d.DialContext(ctx, "tcp", s.cfg.Upstream)
}

// pump copies src -> dst, running every chunk through the gate first.
//
// On a denial it writes the in-protocol error (when a DenyWriter is
// configured) and returns, which closes both halves via the deferred closes
// in handle. Nothing is forwarded.
func (s *Server) pump(
	ctx context.Context,
	g *gate.Gate,
	src, dst net.Conn,
	dir hoopinspect.Direction,
	log *slog.Logger,
) {
	// A re-framing codec holds rows back until their result set ends. Every
	// exit from this loop must release them, or the client silently loses
	// the tail of its output — which reads as a truncated result, not as a
	// masking bug. Only the server direction can hold anything.
	if dir == hoopinspect.FromServer {
		defer func() {
			if tail := g.FlushResponse(); len(tail) > 0 {
				_, _ = dst.Write(tail)
			}
		}()
	}

	buf := make([]byte, 32*1024)
	for {
		if s.cfg.IdleTimeout > 0 {
			_ = src.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			var d gate.Decision
			if dir == hoopinspect.FromClient {
				d = g.Request(ctx, buf[:n])
			} else {
				d = g.Response(ctx, buf[:n])
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
				// travel toward the client, so the client is who needs to
				// know why the connection ended.
				if s.cfg.DenyWriter != nil {
					target := dst
					if dir == hoopinspect.FromClient {
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
