// Package license verifies the signed license a sidecar runs under. It
// reimplements common/license over the stdlib, because importing the
// gateway's module would end this module's one-dependency rule.
// client/licensecompat pins the two copies to the same key and bytes.
package license

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// EnvVar names the environment variable a license may arrive in.
//
// Not HOOP_SIDECAR_LICENSE: the same document is valid for the gateway, and a
// deployment setting it twice under two names will set one of them wrong.
const EnvVar = "HOOP_LICENSE"

// Support is where every message here sends an operator who cannot fix the
// problem themselves.
const Support = "https://help.hoop.dev"

// License types. Only Enterprise grants anything. An oss license verifies and
// changes nothing, which is what the control plane does with the same value.
const (
	OSSType        = "oss"
	EnterpriseType = "enterprise"
)

// The feature keys this module reads. common/license holds the full catalog;
// the keys missing here gate no sidecar capability.
const (
	FeatureGuardrails  = "guardrails"
	FeatureDataMasking = "data-masking"
)

// trustedKeyPEM is the public key a license must carry a signature from,
// from `openssl rsa -in ./license.key -pubout`. A var so the round-trip test
// can sign with a generated key. Assign it outside a test and the build
// honours licenses Hoop never issued.
var trustedKeyPEM = []byte(`
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAuMaf59LDDC5t06jYtXJB
xDM3+e1POErhDzV1KcATYN0PS39yeqZ4VYxOr/0b8iqoPmYfReoj1GBiXKkMrO5D
BOCCFwSUGnEAPVBUsGhcbtPmEW8iJvMCdiG35GpWgBbn8Q5TAMdEweGQSBo0CPRz
xaOLeCgMv5qx10KpnP/8SRaDmM0vvOksRwJAMmwMaSkQEKOrs97jkDgnBY1mz1TI
zmo40K3nFT6WHgqETIrl3t/fC1Fv25MDrPLE4M3htqBKLKDR99pPHX0gxB3dvwi6
p8mG+hifq6xb6bTDH7ilIhFf30v+jjSfLyZUl56xitSiqF92uJTOZ5Q9xqISo7Sq
yQIDAQAB
-----END PUBLIC KEY-----
`)

// ErrExpired reports a license that verified and whose term has ended. The
// sidecar drops to the free tier on this one and refuses to start on the
// rest, so callers have to tell them apart. See Status.
var ErrExpired = errors.New("license expired")

// Payload is the signed half of a license. The JSON tags match
// common/license.Payload, so each package decodes the other's documents.
type Payload struct {
	Type     string `json:"type"`
	IssuedAt int64  `json:"issued_at"`
	ExpireAt int64  `json:"expire_at"`
	// AllowedHosts is signed here and enforced nowhere. The gateway checks
	// it against the API hostname it serves; a sidecar's only candidate is
	// a scheduler-generated pod name, and checking that refuses every real
	// deployment.
	AllowedHosts []string `json:"allowed_hosts"`
	Description  string   `json:"description"`
	// Features this license enables. Empty or absent enables all of them.
	Features []string `json:"features,omitempty"`
}

// License is the document an operator pastes into a config file or a secret.
type License struct {
	Payload   Payload `json:"payload"`
	KeyID     string  `json:"key_id"`
	Signature string  `json:"signature"`
}

// SigningData is the byte string the signature covers. Exported for
// client/licensecompat, which verifies a gateway signature against these
// bytes. Change the format without changing the gateway's and every license
// in the field stops verifying.
func (p Payload) SigningData() []byte {
	v := p.Type + ":" +
		strconv.FormatInt(p.IssuedAt, 10) + ":" +
		strconv.FormatInt(p.ExpireAt, 10) + ":" +
		strings.Join(p.AllowedHosts, ",") + ":" +
		p.Description
	// Appended only when present, so licenses signed before the field
	// existed keep verifying against the same data.
	if len(p.Features) > 0 {
		v += ":" + strings.Join(p.Features, ",")
	}
	return []byte(v)
}

// IsFeatureEnabled reports whether the license names a feature. An empty
// feature list means every feature.
func (l License) IsFeatureEnabled(feature string) bool {
	if len(l.Payload.Features) == 0 {
		return true
	}
	return slices.Contains(l.Payload.Features, feature)
}

// Verify checks the signature and the term against the current time. It
// returns ErrExpired for a sound document that ran out, so a caller can tell
// that from "this is not a license".
func (l License) Verify() error { return l.verify(time.Now().UTC()) }

func (l License) verify(now time.Time) error {
	if err := l.validateAttributes(); err != nil {
		return err
	}
	pubkey, err := parsePublicKey()
	if err != nil {
		return err
	}
	signature, err := base64.StdEncoding.DecodeString(l.Signature)
	if err != nil {
		return fmt.Errorf("the signature is not valid base64: %v", err)
	}
	sum := sha256.Sum256(l.Payload.SigningData())
	if err := rsa.VerifyPSS(pubkey, crypto.SHA256, sum[:], signature, nil); err != nil {
		return errors.New("the signature does not match the payload, so the license was " +
			"altered or was not issued by Hoop")
	}
	issuedAt := time.Unix(l.Payload.IssuedAt, 0).UTC()
	expireAt := time.Unix(l.Payload.ExpireAt, 0).UTC()
	if now.Before(issuedAt) {
		// A container without NTP far more often than a forged date, so
		// the message names the clock.
		return fmt.Errorf("the license is dated %s, which is in the future; check this "+
			"machine's clock", issuedAt.Format(time.RFC3339))
	}
	if now.After(expireAt) {
		return ErrExpired
	}
	return nil
}

func (l License) validateAttributes() error {
	if l.Payload.Type != OSSType && l.Payload.Type != EnterpriseType {
		return fmt.Errorf("unknown license type %q, expected %q or %q",
			l.Payload.Type, OSSType, EnterpriseType)
	}
	if len(l.Payload.AllowedHosts) == 0 {
		return errors.New("the license names no allowed hosts")
	}
	if l.Payload.Description == "" {
		return errors.New("the license has no description")
	}
	if l.Payload.IssuedAt == 0 || l.Payload.ExpireAt == 0 {
		return errors.New("the license has no issued_at or expire_at")
	}
	if l.Signature == "" {
		return errors.New("the license has no signature")
	}
	return nil
}

// State is what the sidecar concluded about the license it was handed.
type State string

const (
	// StateMissing means nobody supplied one, which is the free tier and
	// not an error. It is the EMPTY string so it is also the zero value:
	// an embedder who never sets a license gets an unlicensed process
	// rather than an undefined one.
	StateMissing State = ""
	// StateValid means the signature checks out and the term is current.
	StateValid State = "valid"
	// StateExpired means the document is genuine and its term has ended.
	StateExpired State = "expired"
	// StateInvalid means nobody can read the reference, or what it points
	// at is not a license Hoop issued. Setup refuses to start on it.
	StateInvalid State = "invalid"
)

// String names the state for an operator. A log line or a JSON field
// carrying "" for the missing state says nothing at all.
func (s State) String() string {
	if s == StateMissing {
		return "missing"
	}
	return string(s)
}

// Ref is one place a license might come from. Source appears in every
// message, since "your license expired" helps nobody who sets one in three
// places.
type Ref struct {
	// Value is a path to a file holding the document, or the document
	// itself. Empty means this source supplied nothing.
	Value string
	// Source names Value for an operator: "HOOP_LICENSE", "the license
	// config key".
	Source string
}

// Status is the resolved license, and the only thing the daemon reads.
//
// The zero value is a missing license, so an embedder who forgets to set one
// keeps the caps rather than losing them.
type Status struct {
	// verified records that the signature checked out when the document
	// was read. The TERM is deliberately not recorded with it: State
	// recomputes that against the clock on every call, so a process
	// outliving ExpireAt loses what the license bought.
	verified bool
	// Source names the Ref that supplied the document, empty when none did.
	Source string
	// License is the parsed document, non-nil whenever the JSON decoded.
	// An expired or forged one still names the customer and the date.
	License *License
	// Err is what went wrong at load, as one sentence. Nil for a license
	// that loaded and expired later; ask Reason for that one.
	Err error
}

// Verdict wraps a document whose signature the caller has already checked.
//
// The control-plane path lands here: the plane verifies, the sidecar stores.
// The term still applies, so a document past ExpireAt reports expired
// however it arrived and whatever the caller believes.
func Verdict(l *License, source string) Status {
	return Status{verified: true, License: l, Source: source}
}

// State is the verdict NOW.
//
// Derived rather than stored, which is the whole point: a license that
// verified at startup reports expired the moment its term ends, so a process
// running for months cannot hold caps it stopped paying for.
func (s Status) State() State { return s.StateAt(time.Now().UTC()) }

// StateAt is the verdict at an instant. A caller schedules on it, and a test
// moves the clock without waiting for one.
func (s Status) StateAt(now time.Time) State {
	switch {
	case s.License == nil && s.Err == nil:
		return StateMissing
	case !s.verified:
		return StateInvalid
	case now.After(s.ExpiresAt()):
		return StateExpired
	}
	return StateValid
}

// ExpiresAt is the instant the term ends, zero when no document arrived.
func (s Status) ExpiresAt() time.Time {
	if s.License == nil {
		return time.Time{}
	}
	return time.Unix(s.License.Payload.ExpireAt, 0).UTC()
}

// Reason says what is wrong with the license now, empty when nothing is. A
// license that expired after it loaded carries no Err, so the sentence comes
// from the term instead.
func (s Status) Reason() string {
	if s.Err != nil {
		return s.Err.Error()
	}
	if s.State() == StateExpired {
		return fmt.Sprintf("the license from %s expired on %s. Renew it at %s",
			sourceName(s.Source), expiryDate(*s.License), Support)
	}
	return ""
}

// Resolve picks the license the process runs under from the candidates, in
// the order given. The first Ref holding anything decides, valid or not: fall
// through from a broken HOOP_LICENSE to the config file and the operator
// learns their env var never worked on the restart after the file changes.
func Resolve(candidates ...Ref) Status {
	for _, c := range candidates {
		if strings.TrimSpace(c.Value) == "" {
			continue
		}
		return Load(c)
	}
	return Status{}
}

// Load reads and verifies one reference. The value is the document when it
// starts with "{" and a path otherwise: a JSON document has one first
// character, and no path begins with it.
func Load(ref Ref) Status {
	s := Status{Source: ref.Source}
	raw := strings.TrimSpace(ref.Value)
	if raw == "" {
		return Status{}
	}

	data := []byte(raw)
	if raw[0] != '{' {
		b, err := os.ReadFile(raw)
		if err != nil {
			s.Err = fmt.Errorf("cannot read the license file named by %s: %v. Give a path "+
				"to the license document, or paste the document itself", ref.Source, err)
			return s
		}
		data = b
	}

	var l License
	if err := json.Unmarshal(data, &l); err != nil {
		s.Err = fmt.Errorf("the license from %s is not valid JSON: %v. Copy the whole "+
			"document Hoop issued, braces included", ref.Source, err)
		return s
	}
	s.License = &l

	switch err := l.Verify(); {
	case err == nil:
		s.verified = true
	case errors.Is(err, ErrExpired):
		// The signature is good and only the term ran out, so this is a
		// verified license. StateAt reads the same term and reaches
		// StateExpired without the verdict being stored anywhere.
		s.verified = true
	default:
		s.Err = fmt.Errorf("the license from %s is not valid: %v. Ask for a replacement at %s",
			ref.Source, err, Support)
	}
	return s
}

// Allows reports whether the license grants a feature right now.
func (s Status) Allows(feature string) bool { return s.AllowsAt(time.Now().UTC(), feature) }

// AllowsAt reports whether the license grants a feature at an instant: it
// verified, its term covers that instant, it is an enterprise license, and
// its feature list names the feature or is empty. An oss license grants
// nothing, matching the control plane.
func (s Status) AllowsAt(now time.Time, feature string) bool {
	return s.StateAt(now) == StateValid &&
		s.License.Payload.Type == EnterpriseType &&
		s.License.IsFeatureEnabled(feature)
}

// Line renders the one line the sidecar prints at startup and again when the
// term ends. Every state gets one, the missing state included: an operator
// who cannot tell "unlicensed" from "the license did not load" reads the
// wrong config file for an hour.
func (s Status) Line() string {
	switch s.State() {
	case StateValid:
		l := s.License
		body := fmt.Sprintf("license: valid. %s %q, expires %s, features: %s",
			l.Payload.Type, l.Payload.Description, expiryDate(*l), featureList(*l))
		if l.Payload.Type != EnterpriseType {
			body += ". The free tier caps still apply"
		}
		return body + sourceSuffix(s.Source)

	case StateExpired:
		return fmt.Sprintf("license: expired. %q expired on %s, running the free tier. "+
			"Renew it at %s", s.License.Payload.Description, expiryDate(*s.License), Support) +
			sourceSuffix(s.Source)

	case StateInvalid:
		return "license: invalid. " + errText(s.Err) + sourceSuffix(s.Source)

	default:
		return `license: missing, running the free tier. Add one with the license flag, ` +
			`the ` + EnvVar + ` environment variable, or the "license" key in the config file`
	}
}

// Report renders the status for the admin endpoint, without the signature. An
// endpoint handing out a complete, reusable license is a licensing hole with
// an HTTP interface.
func (s Status) Report() map[string]any {
	out := map[string]any{"state": s.State().String()}
	if s.Source != "" {
		out["source"] = s.Source
	}
	if reason := s.Reason(); reason != "" {
		out["problem"] = reason
	}
	if l := s.License; l != nil {
		out["type"] = l.Payload.Type
		out["description"] = l.Payload.Description
		out["issued_at"] = l.Payload.IssuedAt
		out["expire_at"] = l.Payload.ExpireAt
		out["key_id"] = l.KeyID
		// Empty means every feature, so render the list rather than
		// omitting it and losing the distinction in JSON.
		features := l.Payload.Features
		if features == nil {
			features = []string{}
		}
		out["features"] = features
	}
	return out
}

// PublicKeyID is the sha256 of the public key this build trusts, hex encoded.
// A license carries the same identifier in key_id, so comparing the two tells
// a rotated key from a corrupted document. client/licensecompat asserts it
// equals the gateway's.
func PublicKeyID() (string, error) {
	pubkey, err := parsePublicKey()
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(pubkey)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

func parsePublicKey() (*rsa.PublicKey, error) {
	block, _ := pem.Decode(trustedKeyPEM)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("this build carries no usable license key")
	}
	obj, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("this build's license key is unreadable: %v", err)
	}
	rsaPub, ok := obj.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("this build's license key is a %T, not an RSA key", obj)
	}
	return rsaPub, nil
}

func expiryDate(l License) string {
	return time.Unix(l.Payload.ExpireAt, 0).UTC().Format(time.DateOnly)
}

// featureList renders what a license grants. An empty list means every
// feature, and "[]" says the reverse.
func featureList(l License) string {
	if len(l.Payload.Features) == 0 {
		return "all"
	}
	return strings.Join(l.Payload.Features, ", ")
}

func sourceSuffix(source string) string {
	if source == "" {
		return ""
	}
	return " (from " + source + ")"
}

func errText(err error) string {
	if err == nil {
		return "no reason given"
	}
	return err.Error()
}

// sourceName names a source in a sentence when one is known.
func sourceName(source string) string {
	if source == "" {
		return "this process"
	}
	return source
}
