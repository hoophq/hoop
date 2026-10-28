package login

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	mongoauth "github.com/hoophq/libhoop/v2/codec/mongodb/auth"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

func init() { proxy.RegisterLogin(inspect.MongoDB, loginMongoDB) }

// mongoConversationID is the id the relay gives the one SASL conversation
// a login runs. mongod numbers them from 1 too.
const mongoConversationID = 1

// mongoPlain is the SASL mechanism the relay terminates: the user and the
// secret in one message (RFC 4616), as a driver sends with
// authMechanism=PLAIN. It is the one mechanism that hands the relay the
// secret itself, which the verifier needs.
const mongoPlain = "PLAIN"

// loginMongoDB terminates a MongoDB login on the relay.
//
// Client leg: the first hello is answered from the upstream's own reply,
// with PLAIN as the one mechanism. The saslStart names the user and
// carries the secret in clear, which the relay allows only when the leg
// cannot be read by a third party (unix socket or TLS the relay
// terminated). A pass-through user's SCRAM exchange goes upstream
// unchanged. A connection that sends a second hello instead of a login is
// a driver's monitor: it carries no identity and stays unauthenticated
// upstream, where the server refuses every command that needs a user.
//
// Upstream leg: the client's hello without its authentication, then
// SCRAM-SHA-256 with the credential's user and password on admin, or
// authenticate with MONGODB-X509 on a connection dialed again with the
// credential's certificate.
func loginMongoDB(ctx context.Context, c *proxy.LoginConn) (proxy.LoginResult, error) {
	l := &mongoLogin{ctx: ctx, c: c, log: c.Log}
	if l.log == nil {
		l.log = slog.New(slog.DiscardHandler)
	}
	return l.run()
}

type mongoLogin struct {
	ctx context.Context
	c   *proxy.LoginConn
	log *slog.Logger

	up *mongoauth.Conn
	// hello is what the relay sent upstream, kept for a second dial.
	helloOp  int32
	helloNS  string
	hello    []byte
	upReply  []byte
	replyIDs int32
}

func (l *mongoLogin) run() (proxy.LoginResult, error) {
	first, err := l.readLogin()
	if err != nil {
		return proxy.LoginResult{}, err
	}
	if first.Command() == "saslStart" {
		if err := l.connect(mongoauth.OpMsg, "", mongoauth.DefaultHello()); err != nil {
			return proxy.LoginResult{}, err
		}
		return l.saslStart(first)
	}
	hello, err := mongoauth.DecodeHello(first.Body)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mongodb login: %w", err)
	}
	if hello.Speculative != nil {
		return l.speculative(first, hello)
	}
	keep := l.mechsPassThrough(hello)
	if err := l.connect(first.OpCode, first.Namespace, mongoauth.UpstreamHello(hello, keep)); err != nil {
		return proxy.LoginResult{}, err
	}
	if err := l.reply(first, mongoauth.HelloReply(l.upReply, l.mechs(hello, keep), nil)); err != nil {
		return proxy.LoginResult{}, err
	}
	next, err := l.readLogin()
	if err != nil {
		return proxy.LoginResult{}, err
	}
	if next.Command() == "saslStart" {
		return l.saslStart(next)
	}
	return l.monitor(next)
}

// speculative handles a hello that carries the client's saslStart. Only a
// pass-through user's SCRAM is speculative here: drivers never fold PLAIN
// into a hello, and nothing else is terminated on this lane.
func (l *mongoLogin) speculative(first mongoauth.Message, hello mongoauth.Hello) (proxy.LoginResult, error) {
	start := *hello.Speculative
	user, uerr := mongoauth.ClientFirstUser(start.Payload)
	if isScram(start.Mechanism) && uerr == nil && l.passthrough(user) {
		return l.passthroughHello(first, hello, start, user)
	}
	return l.refuseMechanism(first, user, start.Mechanism)
}

// saslStart handles a standalone saslStart after the hello was answered:
// a pass-through user's SCRAM goes upstream unchanged, anyone else logs in
// with PLAIN.
func (l *mongoLogin) saslStart(msg mongoauth.Message) (proxy.LoginResult, error) {
	start, err := mongoauth.DecodeSASLStart(msg.Body)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mongodb login: %w", err)
	}
	if isScram(start.Mechanism) {
		if user, err := mongoauth.ClientFirstUser(start.Payload); err == nil && l.passthrough(user) {
			return l.passthroughCommand(msg, user)
		}
	}
	if start.Mechanism != mongoPlain {
		return l.refuseMechanism(msg, "", start.Mechanism)
	}
	user, secret, err := plainCredentials(start.Payload)
	if err != nil {
		return l.refuse(msg, user, err.Error())
	}
	if l.c.Lane.Network != "unix" && !l.c.Lane.ClientTLS {
		return l.refuse(msg, user, "no password method is safe on a plain tcp lane")
	}
	if l.c.Authenticate == nil {
		return l.refuse(msg, user, "lane has no authenticator")
	}
	identity, cred, err := l.c.Authenticate(l.ctx, user, secret)
	if err != nil {
		if errors.Is(err, proxy.ErrLoginRefused) {
			return l.refuse(msg, user, err.Error())
		}
		_ = l.reply(msg, mongoauth.AuthenticationFailed())
		return proxy.LoginResult{}, fmt.Errorf("mongodb login: authenticate: %w", err)
	}
	if identity.Subject == "" {
		identity.Subject = user
	}
	// From here the credential is the relay's to release on failure.
	fail := func(err error) (proxy.LoginResult, error) {
		return proxy.LoginResult{Identity: identity, Credential: cred}, err
	}
	if err := l.upstreamLogin(cred); err != nil {
		l.log.Warn("mongodb upstream login failed", "credential", cred.Name, "upstream_user", cred.User, "error", err)
		_ = l.reply(msg, mongoauth.CommandError(mongoauth.CodeAuthenticationFailed,
			"AuthenticationFailed", "upstream authentication failed"))
		return fail(fmt.Errorf("mongodb login: upstream: %w", err))
	}
	if err := l.reply(msg, mongoauth.SASLReply(mongoConversationID, true, nil)); err != nil {
		return fail(err)
	}
	return proxy.LoginResult{Identity: identity, Credential: cred}, nil
}

// plainCredentials reads a PLAIN payload: authzid NUL authcid NUL passwd.
// An authorization identity other than the authentication one is refused,
// as mongod refuses it. The error names the shape, never the secret.
func plainCredentials(payload []byte) (user, secret string, err error) {
	parts := bytes.Split(payload, []byte{0})
	if len(parts) != 3 || len(parts[1]) == 0 {
		return "", "", errors.New("malformed PLAIN payload")
	}
	user = string(parts[1])
	if len(parts[0]) != 0 && !bytes.Equal(parts[0], parts[1]) {
		return user, "", errors.New("PLAIN authorization identity must match the authentication identity")
	}
	return user, string(parts[2]), nil
}

// upstreamLogin authenticates the upstream leg with cred. A password
// credential is a user of admin: the client's own authSource names where
// the client's user lived, which says nothing about the credential's. A
// certificate needs a new TLS handshake, so the connection is dialed again
// with it.
func (l *mongoLogin) upstreamLogin(cred proxy.Credential) error {
	if cred.Cert == nil {
		return mongoauth.ScramLogin(l.up, mongoauth.ScramSHA256, "admin", cred.User, cred.Password)
	}
	_ = l.c.Upstream.Close()
	l.c.Upstream = nil
	up, err := l.c.Dial(l.ctx, &cred)
	if err != nil {
		return err
	}
	l.c.Upstream = up
	l.up = &mongoauth.Conn{RW: up}
	if _, err := l.up.Hello(l.helloOp, l.helloNS, l.hello); err != nil {
		return err
	}
	return mongoauth.X509Login(l.up)
}

// passthroughHello relays a pass-through user's speculative login: the
// client's saslStart runs upstream as a command and its reply rides in the
// hello reply, where the client expects it. The rest of the conversation
// flows through the pumps.
func (l *mongoLogin) passthroughHello(first mongoauth.Message, hello mongoauth.Hello, start mongoauth.SASLStart, user string) (proxy.LoginResult, error) {
	if err := l.connect(first.OpCode, first.Namespace, mongoauth.UpstreamHello(hello, true)); err != nil {
		return proxy.LoginResult{}, err
	}
	mechs := l.mechs(hello, true)
	if start.DB == "" {
		start.DB = "admin"
	}
	reply, err := l.up.Command(mongoauth.SASLStartCommand(start))
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mongodb login: upstream saslStart: %w", err)
	}
	// A server never reports a failed speculative attempt: it omits the
	// field and the driver starts a plain conversation, which fails on
	// its own terms.
	var spec []byte
	if resp, err := mongoauth.DecodeSASLResponse(reply); err == nil && resp.OK {
		spec = mongoauth.SpeculativeReply(resp.ConversationID, resp.Done, resp.Payload)
	}
	if err := l.reply(first, mongoauth.HelloReply(l.upReply, mechs, spec)); err != nil {
		return proxy.LoginResult{}, err
	}
	l.log.Debug("mongodb login passed through", "user", user)
	return proxy.LoginResult{Identity: session.Identity{Subject: user}, Passthrough: true}, nil
}

// passthroughCommand relays a pass-through user's saslStart unchanged and
// leaves the rest of the conversation to the pumps.
func (l *mongoLogin) passthroughCommand(msg mongoauth.Message, user string) (proxy.LoginResult, error) {
	if _, err := l.c.Upstream.Write(msg.Raw); err != nil {
		return proxy.LoginResult{}, err
	}
	reply, err := mongoauth.ReadMessage(l.c.Upstream)
	if err != nil {
		return proxy.LoginResult{}, fmt.Errorf("mongodb login: upstream saslStart: %w", err)
	}
	if err := l.send(reply.Raw); err != nil {
		return proxy.LoginResult{}, err
	}
	l.log.Debug("mongodb login passed through", "user", user)
	return proxy.LoginResult{Identity: session.Identity{Subject: user}, Passthrough: true}, nil
}

// monitor hands a connection that only sends hellos to the pumps. The
// hello already went through Inspect; its reply, possibly a stream of
// them, correlates in the gate like any other.
func (l *mongoLogin) monitor(hello mongoauth.Message) (proxy.LoginResult, error) {
	if _, err := l.c.Upstream.Write(hello.Raw); err != nil {
		return proxy.LoginResult{}, err
	}
	l.log.Debug("mongodb connection carries no login; monitoring only")
	return proxy.LoginResult{Passthrough: true}, nil
}

// connect dials the upstream and runs the hello on it, keeping the reply
// the client's own hello reply is built from.
func (l *mongoLogin) connect(opcode int32, namespace string, hello []byte) error {
	up, err := l.c.Dial(l.ctx, nil)
	if err != nil {
		return err
	}
	l.c.Upstream = up
	l.up = &mongoauth.Conn{RW: up}
	if namespace == "" {
		namespace = "admin.$cmd"
	}
	l.helloOp, l.helloNS, l.hello = opcode, namespace, hello
	reply, err := l.up.Hello(opcode, namespace, hello)
	if err != nil {
		return fmt.Errorf("mongodb login: upstream hello: %w", err)
	}
	if err := mongoauth.OK(reply); err != nil {
		return fmt.Errorf("mongodb login: upstream hello: %w", err)
	}
	l.upReply = reply
	return nil
}

// mechsPassThrough reports a hello whose saslSupportedMechs question names
// a pass-through user: the question is forwarded and the upstream answers.
func (l *mongoLogin) mechsPassThrough(hello mongoauth.Hello) bool {
	_, user, ok := strings.Cut(hello.MechsUser, ".")
	return ok && l.passthrough(user)
}

// mechs answers a hello's saslSupportedMechs question once the upstream
// replied: nothing when the client did not ask, the upstream's own list
// for a pass-through user, the relay's one mechanism otherwise.
func (l *mongoLogin) mechs(hello mongoauth.Hello, passThrough bool) []string {
	switch {
	case hello.MechsUser == "":
		return nil
	case passThrough:
		if mechs := mongoauth.SupportedMechs(l.upReply); mechs != nil {
			return mechs
		}
		return []string{}
	default:
		return []string{mongoPlain}
	}
}

func (l *mongoLogin) passthrough(user string) bool {
	return l.c.Passthrough != nil && l.c.Passthrough(user)
}

// read takes the client's next message. The caller decides whether the
// gate sees it: a login message is inspected so the codec's correlation
// holds; a command in place of a login is not, since the gate would judge
// it as a statement of a session that never starts.
func (l *mongoLogin) read() (mongoauth.Message, error) {
	msg, err := mongoauth.ReadMessage(l.c.Client)
	if err != nil {
		return msg, fmt.Errorf("mongodb login: %w", err)
	}
	return msg, nil
}

func (l *mongoLogin) inspect(msg mongoauth.Message) error {
	return l.c.Inspect(inspect.FromClient, msg.Raw)
}

// readLogin reads a hello or saslStart and refuses any other command the
// way the server refuses one before authentication: on this lane there is
// no anonymous session.
func (l *mongoLogin) readLogin() (mongoauth.Message, error) {
	msg, err := l.read()
	if err != nil {
		return msg, err
	}
	cmd := msg.Command()
	if !mongoauth.IsHello(cmd) && cmd != "saslStart" {
		l.log.Debug("mongodb login refused", "reason", "command before login", "command", cmd)
		l.answer(msg, mongoauth.Unauthorized(cmd))
		return msg, fmt.Errorf("%w: command %q before login", proxy.ErrLoginRefused, cmd)
	}
	if err := l.inspect(msg); err != nil {
		return msg, err
	}
	return msg, nil
}

// answer writes a reply the gate never sees, for a request it never saw.
func (l *mongoLogin) answer(req mongoauth.Message, doc []byte) {
	l.replyIDs++
	_, _ = l.c.Client.Write(mongoauth.ReplyTo(req, l.replyIDs, doc))
}

// reply answers req with doc, framed as the request was, through Inspect.
func (l *mongoLogin) reply(req mongoauth.Message, doc []byte) error {
	l.replyIDs++
	return l.send(mongoauth.ReplyTo(req, l.replyIDs, doc))
}

func (l *mongoLogin) send(raw []byte) error {
	if err := l.c.Inspect(inspect.FromServer, raw); err != nil {
		return err
	}
	_, err := l.c.Client.Write(raw)
	return err
}

// refuse answers req with the server's own AuthenticationFailed, the same
// for every reason so a client cannot tell an unknown user from a wrong
// key, and reports the refusal under the claimed user.
func (l *mongoLogin) refuse(req mongoauth.Message, user, reason string) (proxy.LoginResult, error) {
	l.log.Debug("mongodb login refused", "user", user, "reason", reason)
	_ = l.reply(req, mongoauth.AuthenticationFailed())
	return proxy.LoginResult{Identity: session.Identity{Subject: user}},
		fmt.Errorf("%w: %s", proxy.ErrLoginRefused, reason)
}

// refuseMechanism names the mechanism, which the client chose and can
// change; nothing about the user leaks.
func (l *mongoLogin) refuseMechanism(req mongoauth.Message, user, mechanism string) (proxy.LoginResult, error) {
	msg := fmt.Sprintf("mechanism %s is not supported on this lane; use %s", mechanism, mongoPlain)
	l.log.Debug("mongodb login refused", "user", user, "reason", msg)
	_ = l.reply(req, mongoauth.CommandError(mongoauth.CodeAuthenticationFailed, "AuthenticationFailed", msg))
	return proxy.LoginResult{Identity: session.Identity{Subject: user}},
		fmt.Errorf("%w: %s", proxy.ErrLoginRefused, msg)
}

func isScram(mechanism string) bool {
	return mechanism == mongoauth.ScramSHA1 || mechanism == mongoauth.ScramSHA256
}
