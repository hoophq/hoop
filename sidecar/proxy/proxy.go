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
	"crypto/rsa"
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

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
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

// statementDenyWriter renders protocols whose native error must be correlated
// with the denied request. Kept optional so existing DenyWriter implementations
// remain source-compatible.
type statementDenyWriter interface {
	DenyStatement(statement inspect.Statement, message string) []byte
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
	// MySQLAuthPrivateKey is the stable relay key that MySQL clients pin when
	// they send an RSA-encrypted password without first requesting a key.
	// It is used only on a MySQL lane with UpstreamTLS.
	MySQLAuthPrivateKey *rsa.PrivateKey

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
	// authenticated the user passes the subject through an mTLS peer cert
	// or a credential token, and this function extracts it. It runs at
	// accept, before a byte is read, so it cannot see the payload; a
	// subject carried in an HTTP header is IdentityHeader's job.
	IdentityFn func(net.Conn) session.Identity

	// IdentityHeader names the request header that carries the
	// authenticated subject on an http lane. The relay reads it from the
	// FIRST request on the connection before the session is created, so
	// the policy context and the session_start audit row both name the
	// principal; see peekHTTPIdentity for the ordering and the trust model.
	// When set it overrides the Subject IdentityFn returned. Refused on
	// any other protocol.
	IdentityHeader string

	// CodecFactory overrides how each connection's Gate builds its codecs.
	// Nil uses the registry. See gate.Config.CodecFactory: it exists so a
	// lane can turn on HTTP body capture, which the argument-free registry
	// factory cannot express.
	CodecFactory func() inspect.Codec

	// Metrics is handed to every connection's Gate. Optional. See
	// gate.Config.Metrics for the contract it must meet.
	Metrics gate.Metrics

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

// laneRules bundles the enforcement facts a connection captures at accept
// time, so a swap replaces them as one unit: a policy from one config
// generation must never run beside a masker from another.
type laneRules struct {
	policy policy.Evaluator
	masker gate.Masker
}

// Server accepts connections and relays them through a Gate.
type Server struct {
	cfg      Config
	log      *slog.Logger
	listener net.Listener

	// rules is what handle reads instead of cfg.Policy/cfg.Masker, so a
	// config reload can replace both without a restart. Loaded once per
	// accepted connection: the Gate keeps what it captured, which is what
	// makes a swap safe under live traffic.
	rules               atomic.Pointer[laneRules]
	mysqlAuth           *mysqlAuthBridge
	mysqlHandshakeSlots chan struct{}

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
	if cfg.MySQLAuthPrivateKey != nil &&
		(cfg.Protocol != inspect.MySQL || cfg.UpstreamTLS == nil) {
		return nil, errors.New(
			"sidecar/proxy: MySQL authentication key requires a MySQL lane with upstream TLS",
		)
	}
	if cfg.IdentityHeader != "" && cfg.Protocol != inspect.HTTP {
		return nil, fmt.Errorf(
			"sidecar/proxy: identity header is only read on an http lane, not %s", cfg.Protocol,
		)
	}
	var (
		mysqlAuth           *mysqlAuthBridge
		mysqlHandshakeSlots chan struct{}
	)
	if cfg.Protocol == inspect.MySQL && cfg.UpstreamTLS != nil {
		var err error
		mysqlAuth, err = newMySQLAuthBridge(cfg.MySQLAuthPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("sidecar/proxy: %w", err)
		}
		mysqlHandshakeSlots = make(chan struct{}, mysqlMaxConcurrentHandshakes)
	}
	cfg.MySQLAuthPrivateKey = nil
	s := &Server{
		cfg:                 cfg,
		log:                 cfg.Logger,
		conns:               map[net.Conn]struct{}{},
		mysqlAuth:           mysqlAuth,
		mysqlHandshakeSlots: mysqlHandshakeSlots,
	}
	s.rules.Store(&laneRules{policy: cfg.Policy, masker: cfg.Masker})
	return s, nil
}

func (s *Server) reserveMySQLHandshake() bool {
	if s.mysqlHandshakeSlots == nil {
		return true
	}
	select {
	case s.mysqlHandshakeSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseMySQLHandshake() {
	if s.mysqlHandshakeSlots != nil {
		<-s.mysqlHandshakeSlots
	}
}

// SwapRules replaces the policy evaluator and masker for every connection
// accepted from now on. Connections already open keep the Gate they
// captured at accept time and drain under the rules they started with;
// nothing rebinds and nothing closes.
//
// This is the seam a control-plane config reload swaps through. Both fields
// travel together on purpose: rules and masking come from one config
// document, and mixing generations would enforce a config nobody wrote.
func (s *Server) SwapRules(pol policy.Evaluator, masker gate.Masker) {
	s.rules.Store(&laneRules{policy: pol, masker: masker})
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

		if !s.reserveMySQLHandshake() {
			s.log.Warn("MySQL TLS handshake refused, at capacity",
				"max_handshakes", mysqlMaxConcurrentHandshakes)
			_ = conn.Close()
			continue
		}

		s.track(conn)
		// The rule generation is pinned HERE, at the accept boundary, not
		// in the asynchronously scheduled handler: a swap landing between
		// accept and the goroutine running must not blur which side of it
		// this connection is on.
		rules := s.rules.Load()
		go func() {
			defer s.untrack(conn)
			s.handle(ctx, conn, rules)
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

// handle relays one connection under rules, the immutable generation Serve
// pinned at the accept boundary.
func (s *Server) handle(ctx context.Context, client net.Conn, rules *laneRules) {
	releaseMySQLHandshake := s.mysqlHandshakeSlots != nil
	if releaseMySQLHandshake {
		defer func() {
			if releaseMySQLHandshake {
				s.releaseMySQLHandshake()
			}
		}()
	}

	identity := session.Identity{PeerAddr: client.RemoteAddr().String()}
	if s.cfg.IdentityFn != nil {
		identity = s.cfg.IdentityFn(client)
		if identity.PeerAddr == "" {
			identity.PeerAddr = client.RemoteAddr().String()
		}
	}
	if s.cfg.IdentityHeader != "" {
		// Before session.New on purpose: the gate freezes the policy
		// context and writes session_start from the identity it is handed,
		// so a subject learned later would never reach either. Also before
		// the upstream dial, unlike the pgwire startup peek, for the same
		// reason. A client that connects and never speaks closes at the
		// deadline with no session, as a client abandoning a handshake does.
		var (
			subject string
			err     error
		)
		client, subject, err = peekHTTPIdentity(client, s.cfg.IdentityHeader, s.cfg.DialTimeout)
		if err != nil {
			s.log.Debug("first request not read", "peer", identity.PeerAddr, "error", err)
			return
		}
		if subject != "" {
			identity.Subject = subject
		}
	}

	sess := session.New(s.cfg.Protocol, identity)
	sess.Connection = s.cfg.Connection
	sess.Upstream = s.cfg.Upstream

	// Rebuilt, not extended, when the principal changes. slog.With APPENDS,
	// so extending it with a second "principal" leaves both on every record
	// and the JSON handler writes the key twice.
	sessionLog := func() *slog.Logger {
		return s.log.With("session", string(sess.ID),
			"principal", sess.Identity.Principal())
	}
	log := sessionLog()

	g, err := gate.New(sess, gate.Config{
		Protocol:         s.cfg.Protocol,
		Policy:           rules.policy,
		Audit:            s.cfg.Audit,
		Masker:           rules.masker,
		FailOnAuditError: s.cfg.FailOnAuditError,
		CodecFactory:     s.cfg.CodecFactory,
		Metrics:          s.cfg.Metrics,
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
			if aerr := s.cfg.Audit.Write(ctx, audit.ErrorEvent(sess, err)); aerr != nil && s.cfg.Metrics != nil {
				// The one audit write that happens outside a Gate, so it
				// reports its own failure into the same counter.
				s.cfg.Metrics.AuditError()
			}
		}
		return
	}
	defer upstream.Close()
	if s.cfg.Protocol == inspect.MySQL && s.cfg.UpstreamTLS != nil {
		inspectPacket := func(dir inspect.Direction, data []byte) ([]byte, error) {
			var d gate.Decision
			if dir == inspect.FromClient {
				d = g.Request(ctx, data)
			} else {
				d = g.Response(ctx, data)
			}
			if d.Err != nil {
				log.Warn("inspection reported an error during MySQL authentication",
					"direction", string(dir), "error", d.Err)
			}
			if !d.Allowed {
				s.denied.Add(1)
				return nil, fmt.Errorf("MySQL authentication denied: %s", d.Message)
			}
			return d.Payload, nil
		}
		client, upstream, err = negotiateMySQLUpstreamTLS(
			client,
			upstream,
			s.cfg.Upstream,
			s.cfg.UpstreamTLS,
			s.cfg.DialTimeout,
			s.mysqlAuth,
			inspectPacket,
		)
		if err != nil {
			log.Debug("MySQL upstream TLS negotiation failed", "error", err)
			return
		}
		s.releaseMySQLHandshake()
		releaseMySQLHandshake = false
	}

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
		log = sessionLog()
	}

	log.Info("session opened", "upstream", s.cfg.Upstream)

	// connCtx ends with the connection, and its cause says which side ended
	// it. ctx is the listener's and outlives every connection, so a hold
	// waiting on a human under it would outlive the client too, and spend an
	// approval on a statement nobody is left to run.
	connCtx, endConn := context.WithCancelCause(ctx)
	defer endConn(nil)

	// Both directions run concurrently; the first to finish tears down the
	// other by closing its peer, which unblocks the pending Read.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer upstream.Close()
		s.pump(connCtx, endConn, g, client, upstream, inspect.FromClient, log)
	}()
	go func() {
		defer wg.Done()
		defer client.Close()
		s.pump(connCtx, endConn, g, upstream, client, inspect.FromServer, log)
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
	if err != nil || s.cfg.UpstreamTLS == nil || s.cfg.Protocol == inspect.MySQL {
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
//
// A read error ends the connection's context through end, naming the side
// that stopped, before anything is closed: whatever waits on the connection
// learns why before the teardown reaches it.
func (s *Server) pump(
	ctx context.Context,
	end context.CancelCauseFunc,
	g *gate.Gate,
	src, dst net.Conn,
	dir inspect.Direction,
	log *slog.Logger,
) {
	// A re-framing codec holds rows back until its result set ends. Release
	// them when the server stream ends normally. A response denial writes a
	// protocol error instead, so held rows must be discarded: appending them
	// after the error leaks denied data and corrupts the client stream.
	discardResponse := false
	if dir == inspect.FromServer {
		defer func() {
			if discardResponse {
				g.DiscardResponse()
				return
			}
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

	// The client side reads ahead: see readAhead.
	var read func() ([]byte, error)
	if dir == inspect.FromClient {
		next, stop := readAhead(src, s.cfg.IdleTimeout, end)
		defer stop()
		read = next
	} else {
		read = s.reader(src)
	}
	for {
		chunk, readErr := read()
		if len(chunk) > 0 {
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
				if dir == inspect.FromServer {
					discardResponse = true
				}
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
					var frame []byte
					if d.DeniedStatement != nil {
						if writer, ok := s.cfg.DenyWriter.(statementDenyWriter); ok {
							frame = writer.DenyStatement(*d.DeniedStatement, d.Message)
						}
					}
					if len(frame) == 0 {
						frame = s.cfg.DenyWriter.Deny(s.cfg.Protocol, dir, d.Message)
					}
					if len(frame) > 0 {
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
			end(endCause(dir, readErr))
			if readErr != io.EOF && !isClosed(readErr) {
				log.Debug("read ended", "direction", string(dir), "error", readErr)
			}
			return
		}
	}
}

// relayBufSize is one read's worth of bytes.
const relayBufSize = 32 * 1024

// reader returns a plain read of src, one buffer reused across calls. A chunk
// is valid until the next call.
func (s *Server) reader(src net.Conn) func() ([]byte, error) {
	buf := make([]byte, relayBufSize)
	return func() ([]byte, error) {
		if s.cfg.IdleTimeout > 0 {
			_ = src.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		}
		n, err := src.Read(buf)
		return buf[:n], err
	}
}

// readAhead reads src on its own goroutine, so a client that hangs up is seen
// while the pump is busy inside the gate.
//
// The pump blocks there for as long as a held statement waits on a human, and
// nothing else reads the client socket. Without this the hangup would go
// unseen until the wait ran out, and an approval landing in between would be
// spent on a statement with nobody left to run it for. A read error ends the
// connection's context the moment it happens, not when the pump gets to it.
//
// Two buffers alternate over an unbuffered channel. The reader fills one while
// the pump works on the other, and it hands the next over only when the pump
// asks for it, which is when the pump is done with the previous one. So a
// chunk is valid until the next call, as with reader, and the client is read
// at most one chunk ahead: a client that pipelined past a held statement is
// not seen hanging up until the pump drains it.
//
// stop releases the goroutine once the pump is gone. It may still be blocked
// in Read; closing src, which handle does, releases that.
func readAhead(src net.Conn, idle time.Duration, end context.CancelCauseFunc) (next func() ([]byte, error), stop func()) {
	type result struct {
		data []byte
		err  error
	}
	results := make(chan result)
	quit := make(chan struct{})
	go func() {
		bufs := [2][]byte{make([]byte, relayBufSize), make([]byte, relayBufSize)}
		for i := 0; ; i ^= 1 {
			if idle > 0 {
				_ = src.SetReadDeadline(time.Now().Add(idle))
			}
			n, err := src.Read(bufs[i])
			// A read that returned BYTES ends the connection through the
			// pump instead, once those bytes have been judged. A legal
			// final request arrives together with its io.EOF on a socket
			// the client half-closed, and ending the connection here would
			// deny that request before it was even filed. A read with
			// nothing to hand over ends it now: that is the hangup the pump
			// is waiting to hear about while it holds a statement.
			if err != nil && n == 0 {
				end(endCause(inspect.FromClient, err))
			}
			select {
			case results <- result{bufs[i][:n], err}:
			case <-quit:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	next = func() ([]byte, error) {
		r := <-results
		return r.data, r.err
	}
	return next, func() { close(quit) }
}

// endCause names the side of a connection that stopped, for whatever was
// waiting on the connection when it did. It reaches the audit record of a
// held statement, so it says what happened rather than which error type.
func endCause(dir inspect.Direction, err error) error {
	side := "the client"
	if dir == inspect.FromServer {
		side = "the upstream"
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("%s sent nothing for longer than the idle timeout", side)
	}
	return fmt.Errorf("%s closed the connection", side)
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
