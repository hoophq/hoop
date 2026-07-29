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
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/hoophq/hoopinspect"
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
		panic("hoopinspect/session: crypto/rand unavailable: " + err.Error())
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
}

// IsAnonymous reports whether any principal was established. A deployment
// that requires authentication should refuse anonymous sessions rather than
// recording an audit trail you cannot act on.
func (i Identity) IsAnonymous() bool {
	return i.Subject == "" && i.Email == ""
}

// Principal returns the best available name for the identity, preferring
// Subject. Returns "anonymous" when neither is set, so audit output always
// has something in the actor column.
func (i Identity) Principal() string {
	switch {
	case i.Subject != "":
		return i.Subject
	case i.Email != "":
		return i.Email
	}
	return "anonymous"
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
	Protocol hoopinspect.Protocol `json:"protocol"`

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
func New(proto hoopinspect.Protocol, id Identity) *Session {
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
