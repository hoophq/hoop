// Package wire is the message vocabulary spoken between the control plane
// and a sidecar.
//
// # Status: proposal
//
// EVL-230 lists this contract as needing explicit sign-off before the
// components build against it. It is Go rather than prose so the four
// component workstreams share one definition instead of four compatible
// guesses, but it is not yet ratified. Change it here, once.
//
// # Shape
//
// One connection per sidecar, always opened by the sidecar. Every message is
// a JSON object with the same envelope:
//
//	{"v":1,"type":"config.apply","id":"01J...","re":"01J...","payload":{}}
//
// This package defines no transport. It is pure types, so the control plane
// server and any future sidecar client can share it without either one
// dictating WebSocket, SSE or anything else to the other.
package wire

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// Version is the envelope version, not the product version.
//
// It changes only if the envelope itself changes shape, which is expected
// never. Message-level evolution happens by adding a Type, because that path
// degrades correctly against an old peer: it answers TypeUnsupported. A
// version bump does not degrade at all.
const Version = 1

// Type names a message. Adding one is the supported way to extend the
// protocol; changing what an existing one means is not.
type Type string

const (
	// TypeHello is sent up as the first message on every connection. It
	// carries the credential, the sidecar's version, and the generation it
	// is currently running.
	TypeHello Type = "hello"
	// TypeHelloOK is sent down to accept a connection. No other message
	// flows in either direction before it.
	TypeHelloOK Type = "hello.ok"
	// TypeHelloReject is sent down to refuse a connection, with a reason.
	TypeHelloReject Type = "hello.reject"

	// TypeConfigApply is sent down carrying a whole config document.
	TypeConfigApply Type = "config.apply"
	// TypeConfigAck is sent up once a config is applied and running.
	TypeConfigAck Type = "config.ack"
	// TypeConfigNack is sent up when a config is refused. The sidecar keeps
	// running the previous one.
	TypeConfigNack Type = "config.nack"

	// TypeStatus is the heartbeat, sent up on an interval the control plane
	// sets in HelloOKPayload.
	TypeStatus Type = "status"

	// TypeUnsupported is sent by either side on receiving a Type it does not
	// know, naming the offending message in Envelope.Re.
	TypeUnsupported Type = "unsupported"
)

// Reserved types are not implemented and must not be reused for anything
// else. They are listed so a later component does not spend a naming
// discussion on a name that is already spoken for.
const (
	TypeApprovalRequest Type = "approval.request"
	TypeApprovalResult  Type = "approval.result"
	TypeAuditBatch      Type = "audit.batch"
)

// Envelope wraps every message.
type Envelope struct {
	// V is the envelope version. Always Version on send.
	V int `json:"v"`
	// Type names the message.
	Type Type `json:"type"`
	// ID identifies this message uniquely within the connection. The
	// generator is the sender's choice; it only has to be unique per
	// connection and stable for the lifetime of a reply.
	ID string `json:"id"`
	// Re is the ID being answered, present only on replies. It is what makes
	// a NACK attributable to the exact config that caused it rather than to
	// whichever push happened most recently.
	Re string `json:"re,omitempty"`
	// Payload is the type-specific body, left raw so a receiver can route on
	// Type before committing to a shape. This is also what lets an unknown
	// Type be answered rather than rejected at the parse step.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// HelloPayload is sent up with TypeHello.
type HelloPayload struct {
	// Name is the sidecar's configured name. It is an assertion, not an
	// identity: the credential decides who this is. Trusting Name for
	// authorization lets any sidecar claim any other's config.
	Name string `json:"name"`
	// Credential proves the sidecar is who it claims. Its format is owned by
	// EVL-234 and deliberately opaque here.
	Credential string `json:"credential"`
	// Version is the sidecar build, used for the fleet view and for
	// capability decisions.
	Version string `json:"version"`
	// Generation is the config generation currently running, or zero if the
	// sidecar has never applied one.
	//
	// This is what makes reconnect a catch-up rather than a replay: the
	// control plane compares it and sends the current config only if it
	// differs. Nothing is queued while a sidecar is away.
	Generation int64 `json:"generation"`
}

// LogValue omits Credential, so logging a HelloPayload cannot leak it.
//
// "never log a credential" is written in three places in this repository and
// enforced in none of them. This enforces it: slog calls LogValue for any
// value passed as an attribute, so log.Info("hello", "payload", p) prints the
// redacted form whether or not the caller remembered.
func (p HelloPayload) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", p.Name),
		slog.String("version", p.Version),
		slog.Int64("generation", p.Generation),
	)
}

// HelloOKPayload is sent down with TypeHelloOK.
type HelloOKPayload struct {
	// SessionID identifies this connection in the control plane's logs. It
	// gives an operator one token to grep for across both sides.
	SessionID string `json:"session_id"`
	// HeartbeatSeconds is how often the sidecar should send TypeStatus.
	//
	// The control plane sets it rather than the sidecar so the timeout and
	// the interval cannot drift apart across versions. A sidecar that is
	// gone still looks connected until a heartbeat fails to arrive, because
	// TCP will hold a half-open socket for far longer than an operator will
	// wait.
	HeartbeatSeconds int `json:"heartbeat_seconds"`
}

// DefaultHeartbeatSeconds is the interval the control plane sends unless it
// has a reason not to.
//
// Named here because two components need the same number: EVL-234 writes it
// into HelloOKPayload and EVL-232 multiplies it to decide when a sidecar is
// stale. Two constants that must agree are two constants that will not.
const DefaultHeartbeatSeconds = 30

// RejectCode classifies a refused connection so the sidecar can decide
// whether retrying could ever work.
type RejectCode string

const (
	// RejectUnauthenticated means the credential was absent, malformed or
	// wrong. Retrying with the same credential will not help.
	RejectUnauthenticated RejectCode = "unauthenticated"
	// RejectRevoked means the credential was valid and has been withdrawn.
	RejectRevoked RejectCode = "revoked"
	// RejectUnknownSidecar means the credential verified but names a sidecar
	// the control plane has no record of.
	RejectUnknownSidecar RejectCode = "unknown_sidecar"
	// RejectUnsupportedVersion means the sidecar is too old to be managed.
	RejectUnsupportedVersion RejectCode = "unsupported_version"
	// RejectUnavailable means the control plane cannot serve right now.
	// This is the only code that justifies a retry with backoff.
	RejectUnavailable RejectCode = "unavailable"
)

// HelloRejectPayload is sent down with TypeHelloReject.
type HelloRejectPayload struct {
	Code RejectCode `json:"code"`
	// Reason is for a human reading a log. Never branch on it; that is what
	// Code is for.
	Reason string `json:"reason"`
}

// ConfigApplyPayload is sent down with TypeConfigApply.
type ConfigApplyPayload struct {
	// Generation is monotonic per sidecar and never reused.
	//
	// It lives here, in the envelope's payload, and NOT inside Config. That
	// is not a style choice. The sidecar's LoadConfigBytes calls
	// json.Decoder.DisallowUnknownFields (sidecar/daemon/config.go:334)
	// so that a typo cannot silently disable a control. Any key the sidecar
	// does not recognise makes the whole document fail to parse, which means
	// adding "generation" to the config would break every push.
	Generation int64 `json:"generation"`

	// Config is a whole sidecar config document, as JSON.
	//
	// Whole, never a delta. A delta stream needs both ends to agree on the
	// base, and after a reconnect they do not.
	//
	// Raw rather than a typed field because importing
	// sidecar/daemon would make this module depend on the sidecar module,
	// which depends on the private libhoop, which would mean the control
	// plane cannot be built without credentials for another repository. The
	// component that owns validation (EVL-231) decides whether to pay that
	// cost; the envelope does not need to.
	Config json.RawMessage `json:"config"`
}

// ConfigAckPayload is sent up with TypeConfigAck. Envelope.Re names the
// TypeConfigApply being acknowledged.
type ConfigAckPayload struct {
	// Generation is the generation now running. The control plane records
	// what it is told here and in TypeHello, and nowhere else. Inferring
	// "we sent N so it runs N" is exactly the assumption TypeConfigNack
	// exists to break, and it makes the fleet view report a rule as enforced
	// when the sidecar refused it.
	Generation int64 `json:"generation"`
}

// ConfigNackPayload is sent up with TypeConfigNack. Envelope.Re names the
// TypeConfigApply being refused.
type ConfigNackPayload struct {
	// Generation is the generation that was refused.
	Generation int64 `json:"generation"`
	// RunningGeneration is what the sidecar is still enforcing. A NACK never
	// means "enforcing nothing": the previous config stays in force.
	RunningGeneration int64 `json:"running_generation"`
	// Reason is why it was refused, and is the highest-value field in this
	// package. It is the only signal that tells an operator a config is
	// broken and what about it is broken. It belongs on screen in the fleet
	// view, not only in a log line.
	Reason string `json:"reason"`
}

// StatusPayload is the heartbeat body, sent up with TypeStatus.
type StatusPayload struct {
	// Generation is the config currently enforced.
	Generation int64 `json:"generation"`
	// UptimeSeconds is how long this sidecar process has been running,
	// which distinguishes a reconnect from a restart.
	UptimeSeconds int64 `json:"uptime_seconds"`
	// Counters is left open so a sidecar can report what it has without a
	// control plane release to name each field first. Anything promoted to a
	// product decision should graduate to a typed field.
	Counters map[string]int64 `json:"counters,omitempty"`
}

// UnsupportedPayload is sent with TypeUnsupported by either side.
type UnsupportedPayload struct {
	// Type is the message type that was not understood.
	//
	// Answering rather than closing the connection is what makes version
	// skew survivable. Customers do not upgrade a fleet in lockstep, so the
	// peer is routinely older than you, and a silent drop turns that into a
	// debugging session.
	Type Type `json:"type"`
}

// idEncoding produces an ID with no padding and no mixed case, so it survives
// being pasted into a log query, a URL and a shell without quoting.
var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewID returns a message ID.
//
// Provided so four components do not each choose a UUID or ULID library for
// sixteen random bytes. Uniqueness is only required within one connection;
// 128 bits is far past that and needs no coordination.
func NewID() string {
	var b [16]byte
	// crypto/rand.Read has been documented never to fail since Go 1.24, and
	// panics internally if the OS source is broken. There is nothing to
	// return and nothing to recover from.
	_, _ = rand.Read(b[:])
	return strings.ToLower(idEncoding.EncodeToString(b[:]))
}

// New builds an envelope carrying payload, marshalling it to JSON.
//
// A nil payload is encoded as an absent field rather than as JSON null, which
// keeps messages that have no body (there are none today, but there will be)
// from carrying a body that decodes to a zero struct.
func New(id string, t Type, payload any) (*Envelope, error) {
	e := &Envelope{V: Version, Type: t, ID: id}
	if payload == nil {
		return e, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed encoding %s payload, reason=%v", t, err)
	}
	e.Payload = raw
	return e, nil
}

// Reply builds an envelope answering the message with ID re.
//
// re is a required string rather than an optional *Envelope. A reply with no
// Re is not a reply: Re is the field that makes a NACK attributable to the
// config that caused it rather than to whichever push happened most recently,
// and a nil-tolerant signature makes losing it a runtime accident instead of
// a compile error.
func Reply(id string, t Type, re string, payload any) (*Envelope, error) {
	if re == "" {
		return nil, fmt.Errorf("cannot build a %s reply with no re", t)
	}
	e, err := New(id, t, payload)
	if err != nil {
		return nil, err
	}
	e.Re = re
	return e, nil
}

// Decode parses one message off the wire.
//
// The three checks it makes are the ones every reader would otherwise write
// for itself, differently. An envelope with the wrong V, no Type or no ID is
// rejected here so no handler has to decide what a message with no ID means.
//
// An unknown Type is not an error. It decodes, and the caller answers
// TypeUnsupported naming it, which is what makes permanent version skew
// survivable.
func Decode(b []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("failed decoding envelope, reason=%v", err)
	}
	if e.V != Version {
		return nil, fmt.Errorf("unsupported envelope version %d, want %d", e.V, Version)
	}
	if e.Type == "" {
		return nil, fmt.Errorf("envelope has no type")
	}
	if e.ID == "" {
		return nil, fmt.Errorf("envelope %s has no id", e.Type)
	}
	return &e, nil
}

// DecodePayload unmarshals an envelope's payload into v.
//
// Unknown fields are ignored, which is the deliberate other half of the
// sidecar's config parsing being strict. A config document is authored by an
// operator, so a typo there is a mistake worth failing on. A protocol
// message is authored by a peer that may be a newer build, so a field you do
// not know is an upgrade, not an error.
func DecodePayload(e *Envelope, v any) error {
	if e == nil {
		return fmt.Errorf("cannot decode payload of a nil envelope")
	}
	if len(e.Payload) == 0 {
		return fmt.Errorf("message %s has no payload", e.Type)
	}
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("failed decoding %s payload, reason=%v", e.Type, err)
	}
	return nil
}
