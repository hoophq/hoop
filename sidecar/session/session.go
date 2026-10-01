// Package session models one inspected connection and the identity behind it.
//
// A Session is the unit an audit trail is keyed on and the unit a policy sees
// as "who". It carries the facts that hold for the whole connection (user,
// connection name, upstream, protocol) so a per-statement Event does not have
// to repeat them.
//
// # Identity belongs to the transport
//
// A codec turns bytes into statements. It has no idea who is on the other end
// of the socket, and it must not: the same Postgres parser serves a per-user
// sidecar, a shared gateway, and an offline replay of a captured stream. The
// identity arrives out of band (an injected header from Envoy, a credential
// token, a mTLS peer certificate) so it belongs in a type the transport
// owns.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// ID is a session identifier. It is opaque; callers must not parse it.
type ID string

// NewID returns a random 128-bit session id, hex encoded.
//
// crypto/rand is used rather than a counter or a timestamp because session
// ids appear in audit records that may be correlated across systems, and a
// guessable id lets an attacker probe for another user's trail.
func NewID() ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unrecoverable; a predictable session id is
		// worse than a crash because the audit trail silently becomes
		// forgeable.
		panic("sidecar/session: crypto/rand unavailable: " + err.Error())
	}
	return ID(hex.EncodeToString(b[:]))
}

// Identity is who is on the other end of a connection.
//
// Every field is optional because the available facts differ by deployment: a
// per-user Envoy sidecar knows Subject from a verified JWT; a shared gateway
// knows it from a credential lookup; a raw TCP relay may know only PeerAddr.
// A policy that requires a field it did not get must fail closed itself.
// This package will not invent one.
type Identity struct {
	// Subject is the authenticated principal: an email, a JWT sub, a
	// service-account name. This is the field a DBA needs when a bad query
	// shows up and they have to know whose access to revoke.
	Subject string `json:"subject,omitempty"`

	// Email, when the identity provider supplies one distinct from Subject.
	Email string `json:"email,omitempty"`

	// Groups the principal belongs to, for group-based policy.
	Groups []string `json:"groups,omitempty"`

	// PeerAddr is the network address the connection came from. Present even
	// when nothing else is.
	PeerAddr string `json:"peer_addr,omitempty"`

	// Attributes carries deployment-specific claims a policy may want
	// (department, cost center, on-call status).
	Attributes map[string]string `json:"attributes,omitempty"`

	// Method says how Subject and Email were established, so a reader of a
	// review can weigh the name: a Google token checked with Google is not
	// a login name a client typed before authenticating.
	//
	// Empty means only PeerAddr is known, or the constructor did not say.
	// It is set where a name is established and nowhere else: by each
	// identity constructor, and by gate.Adopt for a claimed pgwire user.
	//
	// It is deliberately NOT in PolicyContext: OPA input and the audit
	// trail stay what they were, and a Rego rule cannot start keying on a
	// label that describes trust without enforcing it. The gate's
	// sameIdentity ignores it too, so a rotated caller is the same caller
	// whatever the method says.
	Method IdentityMethod `json:"method,omitempty"`
}

// IdentityMethod names how an Identity's principal was established.
//
// The set is open: a consumer that meets a value it does not know must show
// it as unknown, never as trusted.
type IdentityMethod string

const (
	// MethodGoogleIdentity: a Google OAuth2 token the sidecar checked with
	// Google's tokeninfo. It names the token holder; the audience is not
	// checked (see identity/google).
	MethodGoogleIdentity IdentityMethod = "google_identity"

	// MethodIdentityHeader: the value of a header an authenticating proxy
	// is expected to set. Any client that reaches the listener directly can
	// set it too, unless something in front strips it.
	MethodIdentityHeader IdentityMethod = "identity_header"

	// MethodDatabaseUser: the user a pgwire StartupMessage claimed. It is a
	// claim until the server answers AuthenticationOk, and a statement
	// pipelined before that is judged under it.
	MethodDatabaseUser IdentityMethod = "database_user"

	// MethodSSHCertificate: a user certificate signed by the listener's
	// trusted CA, checked by libhoop before the connection opens.
	MethodSSHCertificate IdentityMethod = "ssh_certificate"

	// MethodTLSClientCertificate: a name read from a client TLS
	// certificate on a grpc lane. The gRPC server requests no client
	// certificate today, so nothing verified it.
	MethodTLSClientCertificate IdentityMethod = "tls_client_certificate"
)

// identityKey is the context key ContextWithIdentity stores under. Unexported
// and a distinct type, so no other package's key can collide with it and the
// value is reached only through the two functions below.
type identityKey struct{}

// ContextWithIdentity returns a child of ctx that carries id.
//
// It is how a statement's typed caller reaches code that only receives a
// context, such as the daemon's review filing behind analyzer.Reviewer: the
// policy and analyzer packages pass the context through without importing
// this one. A child of ctx keeps its cancellation, its cause and every value
// already on it.
func ContextWithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFromContext returns the identity ContextWithIdentity stored, and
// false for a nil ctx or one that carries none.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	if ctx == nil {
		return Identity{}, false
	}
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// IsAnonymous reports whether any principal was established. A deployment
// that requires authentication should refuse anonymous sessions rather than
// recording an audit trail you cannot act on.
func (i Identity) IsAnonymous() bool {
	return i.Subject == "" && i.Email == ""
}

// AnonymousPrincipal is what Principal returns when no identity is known.
//
// Exported because consumers have to distinguish "nobody has authenticated"
// from a real name, and comparing against the empty string does not do it:
// Principal never returns "". A store folding events into a session row uses
// this to let a principal learned mid-session replace the placeholder.
const AnonymousPrincipal = "anonymous"

// Principal returns the best available name for the identity, preferring
// Subject. Returns AnonymousPrincipal when neither is set, so audit output
// always has something in the actor column.
func (i Identity) Principal() string {
	switch {
	case i.Subject != "":
		return i.Subject
	case i.Email != "":
		return i.Email
	}
	return AnonymousPrincipal
}

// Session is one inspected connection.
//
// It is created when a connection is accepted and closed when it ends. The
// zero value is not usable; use New.
type Session struct {
	// ID uniquely identifies this session.
	ID ID `json:"id"`

	// Identity is who opened it.
	Identity Identity `json:"identity"`

	// Protocol being inspected.
	Protocol inspect.Protocol `json:"protocol"`

	// Connection is the operator-facing name of the resource being reached
	// ("appdb", "internal-api"). It is what a policy and an audit query key
	// on, as distinct from the physical Upstream address which may change.
	Connection string `json:"connection,omitempty"`

	// Upstream is the address bytes are forwarded to.
	Upstream string `json:"upstream,omitempty"`

	// CorrelationID ties this session to an external workflow (a ticket, a
	// CI run, an agent task) when the caller supplies one.
	CorrelationID string `json:"correlation_id,omitempty"`

	// StartedAt is when the connection was accepted.
	StartedAt time.Time `json:"started_at"`

	// EndedAt is when it closed. Zero while the session is live.
	EndedAt time.Time `json:"ended_at,omitzero"`

	// Metadata carries deployment-specific session facts.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// New starts a session with a fresh id and StartedAt set to now.
func New(proto inspect.Protocol, id Identity) *Session {
	return &Session{
		ID:        NewID(),
		Identity:  id,
		Protocol:  proto,
		StartedAt: time.Now().UTC(),
	}
}

// End marks the session closed. Idempotent: a second call does not move
// EndedAt, so a double-close in a defer chain cannot corrupt the duration.
func (s *Session) End() {
	if s.EndedAt.IsZero() {
		s.EndedAt = time.Now().UTC()
	}
}

// Duration returns how long the session ran, or how long it has been running
// when still open.
func (s *Session) Duration() time.Duration {
	if s.EndedAt.IsZero() {
		return time.Since(s.StartedAt)
	}
	return s.EndedAt.Sub(s.StartedAt)
}

// IsOpen reports whether the session is still running.
func (s *Session) IsOpen() bool { return s.EndedAt.IsZero() }

// PolicyContext renders the session as the flat string map the OPA client
// sends as `input.context`, so a Rego policy can reference the actor without
// the caller assembling it by hand.
//
// Groups are joined with "," rather than sent as a list because the context
// map is typed map[string]string; a policy needing structured groups should
// read them from a richer input the caller supplies.
func (s *Session) PolicyContext() map[string]string {
	ctx := map[string]string{
		"session_id": string(s.ID),
		"principal":  s.Identity.Principal(),
	}
	if s.Identity.Subject != "" {
		ctx["subject"] = s.Identity.Subject
	}
	if s.Identity.Email != "" {
		ctx["email"] = s.Identity.Email
	}
	if s.Identity.PeerAddr != "" {
		ctx["peer_addr"] = s.Identity.PeerAddr
	}
	if s.Connection != "" {
		ctx["connection"] = s.Connection
	}
	if s.Upstream != "" {
		ctx["upstream"] = s.Upstream
	}
	if s.CorrelationID != "" {
		ctx["correlation_id"] = s.CorrelationID
	}
	if len(s.Identity.Groups) > 0 {
		ctx["groups"] = joinComma(s.Identity.Groups)
	}
	for k, v := range s.Identity.Attributes {
		ctx[k] = v
	}
	for k, v := range s.Metadata {
		ctx[k] = v
	}
	return ctx
}

// ReservedContextKey reports whether PolicyContext writes key itself.
//
// PolicyContext copies Metadata over its own keys, so a metadata key spelled
// `principal` would replace the actor a Rego policy reads. That is harmless
// while every metadata key is a fact the operator wrote. It is not once a
// lane lifts metadata VALUES from a client-sent packet: the key stays the
// operator's, but a key that collides hands the client the actor column. A
// lane that records client-supplied metadata must refuse these keys at load.
func ReservedContextKey(key string) bool {
	switch key {
	case "session_id", "principal", "subject", "email", "peer_addr",
		"connection", "upstream", "correlation_id", "groups":
		return true
	}
	return false
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
