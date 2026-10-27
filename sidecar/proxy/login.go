package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	codectypes "github.com/hoophq/libhoop/v2/codec/types"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// ErrLoginRefused is the sentinel a refused client login wraps. The relay
// tells it from an I/O error: a refusal is audited under the claimed
// identity, an I/O error only closes.
var ErrLoginRefused = codectypes.ErrLoginRefused

// Credential is what the relay presents upstream for one session. A source
// fills everything but Name; the daemon sets Name from its pool entry. It
// is never logged.
type Credential struct {
	Name, User, Password string
	// Cert is offered in the upstream TLS handshake when non-nil.
	Cert *tls.Certificate
	// GSS produces the tokens of an integrated (Kerberos) upstream login.
	GSS codectypes.GSSInitiator
	// Release returns the credential to its source at session end.
	// Optional.
	Release func(context.Context) error
}

// PropagateInfo says what of the verified identity the upstream login
// carries. ApplicationName is a text/template rendered into the postgres
// application_name startup parameter; its fields are .Principal, .Email,
// .Subject and .Context (the lane's context map).
type PropagateInfo struct {
	ApplicationName string

	tmpl *template.Template
}

// compile parses ApplicationName once so a bad template is a startup error
// and Render costs no parse per login.
func (p *PropagateInfo) compile() error {
	if p.ApplicationName == "" {
		return nil
	}
	t, err := template.New("application_name").
		Option("missingkey=zero").Parse(p.ApplicationName)
	if err != nil {
		return fmt.Errorf("propagate.application_name: %w", err)
	}
	p.tmpl = t
	return nil
}

// Render fills ApplicationName for id. It returns "" when nothing is
// configured or the template fails, so a driver sends no parameter rather
// than a broken one.
func (p PropagateInfo) Render(id session.Identity, ctx map[string]string) string {
	t := p.tmpl
	if t == nil {
		if p.ApplicationName == "" {
			return ""
		}
		var err error
		t, err = template.New("application_name").
			Option("missingkey=zero").Parse(p.ApplicationName)
		if err != nil {
			return ""
		}
	}
	if ctx == nil {
		ctx = map[string]string{}
	}
	var b strings.Builder
	err := t.Execute(&b, struct {
		Principal, Email, Subject string
		Context                   map[string]string
	}{id.Principal(), id.Email, id.Subject, ctx})
	if err != nil {
		return ""
	}
	return b.String()
}

// LaneInfo is what a login driver may know about its lane.
type LaneInfo struct {
	Protocol     inspect.Protocol
	Name         string
	Network      string // "tcp" or "unix"
	ClientTLS    bool   // the client leg is TLS the relay terminated
	UpstreamAddr string
}

// LoginConn is one connection in its login phase, handed to a LoginFunc.
type LoginConn struct {
	// Client is the client leg after negotiateDownstream: on postgres a
	// bufferedConn whose StartupMessage is still unread.
	Client net.Conn
	// Upstream is nil until the driver dials it. The relay pumps whatever
	// Client and Upstream hold when the driver returns, so a driver that
	// wrapped either in TLS leaves the wrapped conn here.
	Upstream net.Conn
	// Dial connects upstream. Where TLS precedes the login (postgres,
	// clickhouse, http) it is negotiated here, offering cred.Cert when
	// non-nil; MySQL and MSSQL start TLS inside their handshake, so Dial
	// returns the raw conn and the driver wraps it with UpstreamTLS.
	Dial func(ctx context.Context, cred *Credential) (net.Conn, error)
	// UpstreamTLS and DownstreamTLS are the lane's configs, nil when the
	// leg is plain. For a driver that negotiates TLS in-band; see
	// tlsClientConfig for offering cred.Cert.
	UpstreamTLS   *tls.Config
	DownstreamTLS *tls.Config
	// MySQLAuth is the relay's RSA key pair for a MySQL client leg; nil on
	// every other protocol.
	MySQLAuth *mysqlAuthBridge

	Lane         LaneInfo
	Authenticate func(ctx context.Context, user, secret string) (session.Identity, Credential, error)
	// AuthenticateProven is Authenticate for a login the driver proved by
	// SCRAM against the material SCRAM returned: hash is the material's
	// Hash. Set with SCRAM.
	AuthenticateProven func(ctx context.Context, user, hash string) (session.Identity, Credential, error)
	// SCRAM returns the client's SCRAM-SHA-256 verifier; nil when the lane
	// offers no SCRAM on the client leg.
	SCRAM func(user string) (codectypes.SCRAMMaterial, bool)
	// Passthrough reports a startup user that keeps today's pass-through
	// login, unchanged bytes and a claimed identity.
	Passthrough func(user string) bool
	Propagate   PropagateInfo
	Context     map[string]string
	// Inspect feeds a login packet to the gate so the codec sees the
	// phases advance. Rewrites the gate makes are not returned: a driver
	// forwards the bytes it built.
	Inspect func(dir inspect.Direction, pkt []byte) error
	Log     *slog.Logger
}

// LoginResult is what a successful login (or a refused one) reports.
type LoginResult struct {
	// Identity is the verified identity; on ErrLoginRefused, the identity
	// the client claimed (Subject = the login user), so the refusal is
	// audited under it.
	Identity session.Identity
	// Credential names what went upstream; Release runs at session end.
	// A driver that fails after selecting one returns it here too, and
	// the relay releases it at once.
	Credential Credential
	// Passthrough marks an identity that is claimed, not verified.
	Passthrough bool
	// Cancel is the postgres CancelRequest mapping; nil elsewhere.
	Cancel *CancelMapping
}

// LoginFunc terminates a client login on the relay, between the client's
// pre-login negotiation and the pumps.
//
// It reads the login from c.Client, verifies it through c.Authenticate (or
// c.SCRAM then c.AuthenticateProven, or lets it through when c.Passthrough
// says so), calls c.Dial with the credential it was given, logs in
// upstream with it, and leaves both legs positioned at the first
// post-login message. It stores the upstream it dialed in c.Upstream.
// Every login packet it forwards or synthesizes goes through c.Inspect
// first so the codec keeps its phase.
//
// On success the relay adopts the identity, writes session_start and pumps.
// On a refusal it returns an error wrapping ErrLoginRefused with
// LoginResult.Identity set to the claimed user, AFTER writing the
// protocol's own login-failure frame to c.Client: the relay writes none,
// it only audits the refusal and closes. Any other error closes quietly,
// after releasing the credential the result carries.
//
// One driver per protocol, registered from init() with RegisterLogin.
type LoginFunc func(ctx context.Context, c *LoginConn) (LoginResult, error)

// CancelMapping pairs the postgres BackendKeyData the client was shown with
// the one the upstream issued.
type CancelMapping struct{ ClientPID, ClientKey, UpstreamPID, UpstreamKey int32 }

var (
	loginMu    sync.RWMutex
	loginFuncs = map[inspect.Protocol]LoginFunc{}
)

// RegisterLogin installs the login driver for p. It panics on a second
// driver for one protocol, like inspect.Register: two drivers for one lane
// is a build defect, not a runtime condition.
func RegisterLogin(p inspect.Protocol, fn LoginFunc) {
	if fn == nil {
		panic("sidecar/proxy: RegisterLogin with a nil LoginFunc")
	}
	loginMu.Lock()
	defer loginMu.Unlock()
	if _, dup := loginFuncs[p]; dup {
		panic(fmt.Sprintf("sidecar/proxy: RegisterLogin called twice for %q", p))
	}
	loginFuncs[p] = fn
}

func loginFor(p inspect.Protocol) (LoginFunc, bool) {
	loginMu.RLock()
	defer loginMu.RUnlock()
	fn, ok := loginFuncs[p]
	return fn, ok
}

// checkLoginConfig refuses an Authenticate on a lane nothing can terminate.
// http and grpc swap per request inside their codecs instead.
func checkLoginConfig(cfg *Config) error {
	if cfg.Authenticate == nil {
		if cfg.SCRAMVerifier != nil || cfg.AuthenticateProven != nil || len(cfg.PassthroughUsers) > 0 {
			return errors.New("sidecar/proxy: SCRAMVerifier, AuthenticateProven and PassthroughUsers need Authenticate")
		}
		return nil
	}
	if cfg.SCRAMVerifier != nil && cfg.AuthenticateProven == nil {
		return errors.New("sidecar/proxy: SCRAMVerifier needs AuthenticateProven")
	}
	switch cfg.Protocol {
	case inspect.HTTP, inspect.GRPC, inspect.ClickHouse:
	default:
		if _, ok := loginFor(cfg.Protocol); !ok {
			return fmt.Errorf("sidecar/proxy: Authenticate is set but no login driver terminates a %s login", cfg.Protocol)
		}
	}
	return cfg.Propagate.compile()
}

// tlsClientConfig returns cfg offering cert in the handshake, or cfg itself
// when there is no certificate to offer. cfg is never mutated: it is shared
// by every connection of the lane.
func tlsClientConfig(cfg *tls.Config, cert *tls.Certificate) *tls.Config {
	if cfg == nil || cert == nil {
		return cfg
	}
	c := cfg.Clone()
	c.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return cert, nil
	}
	return c
}

// cancelKey is the BackendKeyData a client holds.
type cancelKey struct{ pid, key int32 }

// cancelTarget is where that client's CancelRequest must go.
type cancelTarget struct {
	dial     func(context.Context) (net.Conn, error)
	pid, key int32
}

// cancelRegistry maps the (pid, key) a login driver showed a client to the
// upstream's own, process-wide: a CancelRequest arrives on a NEW connection
// and names nothing but that pair.
var cancelRegistry sync.Map

// registerCancel records m until the returned func runs.
func registerCancel(m *CancelMapping, dial func(context.Context) (net.Conn, error)) func() {
	k := cancelKey{m.ClientPID, m.ClientKey}
	cancelRegistry.Store(k, cancelTarget{dial: dial, pid: m.UpstreamPID, key: m.UpstreamKey})
	return func() { cancelRegistry.Delete(k) }
}

func lookupCancel(pid, key int32) (cancelTarget, bool) {
	v, ok := cancelRegistry.Load(cancelKey{pid, key})
	if !ok {
		return cancelTarget{}, false
	}
	return v.(cancelTarget), true
}

// relayCancel opens a connection to the recorded upstream and sends the
// upstream's CancelRequest for (pid, key). false when the pair is unknown.
func relayCancel(ctx context.Context, pid, key int32) (bool, error) {
	t, ok := lookupCancel(pid, key)
	if !ok {
		return false, nil
	}
	conn, err := t.dial(ctx)
	if err != nil {
		return true, err
	}
	if _, err := conn.Write(pgCancelRequest(t.pid, t.key)); err != nil {
		_ = conn.Close()
		return true, err
	}
	return true, conn.Close()
}

// newLoginConn builds the LoginConn for one connection. client is the leg
// negotiateDownstream returned.
func (s *Server) newLoginConn(ctx context.Context, client net.Conn, g *gate.Gate, log *slog.Logger) *LoginConn {
	proto := s.cfg.Protocol
	return &LoginConn{
		Client: client,
		Dial: func(ctx context.Context, cred *Credential) (net.Conn, error) {
			tlsCfg := s.cfg.UpstreamTLS
			// TLS inside the handshake belongs to the driver.
			if proto == inspect.MySQL || proto == inspect.MSSQL {
				tlsCfg = nil
			} else if cred != nil {
				tlsCfg = tlsClientConfig(tlsCfg, cred.Cert)
			}
			up, err := s.dialUpstreamTLS(ctx, tlsCfg)
			if err != nil {
				return nil, err
			}
			// The login runs under the dial timeout on both legs; runLogin
			// clears it once the driver is done.
			if s.cfg.DialTimeout > 0 {
				if err := up.SetDeadline(time.Now().UTC().Add(s.cfg.DialTimeout)); err != nil {
					up.Close()
					return nil, err
				}
			}
			return up, nil
		},
		UpstreamTLS:   s.cfg.UpstreamTLS,
		DownstreamTLS: s.cfg.DownstreamTLS,
		MySQLAuth:     s.mysqlAuth,
		Lane: LaneInfo{
			Protocol:     proto,
			Name:         s.cfg.Connection,
			Network:      s.cfg.Network,
			ClientTLS:    isTLS(client),
			UpstreamAddr: s.cfg.Upstream,
		},
		Authenticate:       s.cfg.Authenticate,
		AuthenticateProven: s.cfg.AuthenticateProven,
		SCRAM:              s.cfg.SCRAMVerifier,
		Passthrough: func(user string) bool {
			return slices.Contains(s.cfg.PassthroughUsers, user)
		},
		Propagate: s.cfg.Propagate,
		Context:   s.cfg.LaneContext,
		Inspect: func(dir inspect.Direction, pkt []byte) error {
			var d gate.Decision
			if dir == inspect.FromClient {
				d = g.Request(ctx, pkt)
			} else {
				d = g.Response(ctx, pkt)
			}
			if d.Err != nil {
				log.Warn("inspection reported an error during login",
					"direction", string(dir), "error", d.Err)
			}
			if !d.Allowed {
				s.denied.Add(1)
				return fmt.Errorf("login packet denied: %s", d.Message)
			}
			return nil
		},
		Log: log,
	}
}

// runLogin drives fn under the dial timeout: a client or an upstream that
// stalls mid-login does not hold the goroutine. The deadline is cleared on
// the legs the driver left, which are what the pumps read.
func (s *Server) runLogin(ctx context.Context, fn LoginFunc, lc *LoginConn) (LoginResult, error) {
	if s.cfg.DialTimeout > 0 {
		if err := lc.Client.SetDeadline(time.Now().UTC().Add(s.cfg.DialTimeout)); err != nil {
			return LoginResult{}, err
		}
	}
	result, err := fn(ctx, lc)
	if err != nil {
		return result, err
	}
	if lc.Upstream == nil {
		return result, errors.New("login driver returned without an upstream")
	}
	if cerr := lc.Client.SetDeadline(time.Time{}); cerr != nil {
		return result, cerr
	}
	return result, lc.Upstream.SetDeadline(time.Time{})
}

// loginMetadata is what a terminated login records on the session.
// verified is false for a refusal and for a pass-through user.
func loginMetadata(r LoginResult, ok bool) map[string]string {
	md := map[string]string{"verified": "false"}
	if ok && !r.Passthrough {
		md["verified"] = "true"
	}
	if r.Credential.Name != "" {
		md["credential"] = r.Credential.Name
	}
	if r.Credential.User != "" {
		md["upstream_user"] = r.Credential.User
	}
	if t := r.Identity.Attributes["identity_type"]; t != "" {
		md["identity_type"] = t
	}
	return md
}

// isTLS reports whether c, or the connection a bufferedConn wraps, is a
// TLS connection the relay terminated.
func isTLS(c net.Conn) bool {
	if bc, ok := c.(*bufferedConn); ok {
		c = bc.Conn
	}
	_, ok := c.(*tls.Conn)
	return ok
}
