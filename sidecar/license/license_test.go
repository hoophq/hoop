package license

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signingKey generates a key and points the verifier at it for one test.
// Without a signer these tests could only assert that real licenses fail.
// client/licensecompat covers the other direction: the shipped key and the
// signing format are the gateway's.
func signingKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	prev := trustedKeyPEM
	trustedKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	t.Cleanup(func() { trustedKeyPEM = prev })
	return key
}

// issue signs a license the way the gateway's sign endpoint does.
func issue(t *testing.T, key *rsa.PrivateKey, p Payload) License {
	t.Helper()
	sum := sha256.Sum256(p.SigningData())
	sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, sum[:], nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return License{
		Payload:   p,
		KeyID:     "test-key",
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
}

func enterprise() Payload {
	return Payload{
		Type:         EnterpriseType,
		IssuedAt:     time.Now().Add(-time.Hour).Unix(),
		ExpireAt:     time.Now().Add(720 * time.Hour).Unix(),
		AllowedHosts: []string{"*"},
		Description:  "Acme Corp",
	}
}

func document(t *testing.T, l License) string {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}
	return string(b)
}

func TestValidEnterpriseLicenseGrantsItsFeatures(t *testing.T) {
	key := signingKey(t)
	l := issue(t, key, enterprise())

	s := Load(Ref{Value: document(t, l), Source: "the test"})

	if s.State != StateValid {
		t.Fatalf("state = %q, err = %v", s.State, s.Err)
	}
	if !s.Allows(FeatureGuardrails) || !s.Allows(FeatureDataMasking) {
		t.Error("a license with no feature list did not grant every feature")
	}
	if s.Source != "the test" {
		t.Errorf("source = %q", s.Source)
	}
}

// A feature list restricts. A license naming only data-masking leaves the
// guardrail cap in force, or one-feature licenses pay for two.
func TestAFeatureListRestrictsWhatIsGranted(t *testing.T) {
	key := signingKey(t)
	p := enterprise()
	p.Features = []string{FeatureDataMasking}
	s := Load(Ref{Value: document(t, issue(t, key, p))})

	if s.State != StateValid {
		t.Fatalf("state = %q, err = %v", s.State, s.Err)
	}
	if !s.Allows(FeatureDataMasking) {
		t.Error("the feature the license names was not granted")
	}
	if s.Allows(FeatureGuardrails) {
		t.Error("a feature the license does not name was granted")
	}
}

// An oss license is signed, current and grants nothing. Treat any verifying
// license as enterprise and the free tier gets the paid caps.
func TestAnOSSLicenseGrantsNothing(t *testing.T) {
	key := signingKey(t)
	p := enterprise()
	p.Type = OSSType
	s := Load(Ref{Value: document(t, issue(t, key, p))})

	if s.State != StateValid {
		t.Fatalf("state = %q, err = %v", s.State, s.Err)
	}
	if s.Allows(FeatureGuardrails) || s.Allows(FeatureDataMasking) {
		t.Error("an oss license lifted a cap")
	}
	if !strings.Contains(s.Line(), "free tier caps still apply") {
		t.Errorf("the line does not say the caps remain: %q", s.Line())
	}
}

// Expiry is its own state: the daemon drops to the free tier on it and
// refuses to start on a forgery.
func TestAnExpiredLicenseIsExpiredAndGrantsNothing(t *testing.T) {
	key := signingKey(t)
	p := enterprise()
	p.IssuedAt = time.Now().Add(-48 * time.Hour).Unix()
	p.ExpireAt = time.Now().Add(-24 * time.Hour).Unix()
	s := Load(Ref{Value: document(t, issue(t, key, p)), Source: "the test"})

	if s.State != StateExpired {
		t.Fatalf("state = %q, err = %v", s.State, s.Err)
	}
	if s.Allows(FeatureGuardrails) {
		t.Error("an expired license lifted a cap")
	}
	if s.License == nil {
		t.Fatal("the parsed document was dropped, so no message can name the customer")
	}
	for _, want := range []string{"expired on", "Acme Corp", Support} {
		if !strings.Contains(s.Line(), want) {
			t.Errorf("the line is missing %q: %q", want, s.Line())
		}
	}
}

// A payload edited to say enterprise, or to move the expiry out a year,
// must not verify.
func TestATamperedPayloadIsInvalid(t *testing.T) {
	key := signingKey(t)
	l := issue(t, key, enterprise())
	l.Payload.ExpireAt = time.Now().Add(10 * 365 * 24 * time.Hour).Unix()

	s := Load(Ref{Value: document(t, l), Source: "the test"})

	if s.State != StateInvalid {
		t.Fatalf("a rewritten payload verified: state = %q", s.State)
	}
	if s.Allows(FeatureGuardrails) {
		t.Error("a tampered license lifted a cap")
	}
	if !strings.Contains(s.Err.Error(), "altered") {
		t.Errorf("the message does not say the document was altered: %v", s.Err)
	}
}

// Somebody else's key gets the same refusal. This signs with a second key
// while the verifier trusts the first.
func TestALicenseFromAnotherKeyIsInvalid(t *testing.T) {
	signingKey(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s := Load(Ref{Value: document(t, issue(t, other, enterprise())), Source: "the test"})

	if s.State != StateInvalid {
		t.Fatalf("a license signed with an untrusted key verified: state = %q", s.State)
	}
}

// A clock behind the issue date is a container without NTP far more often
// than a forgery. "License is not valid" for a machine thirty seconds fast
// sends an operator to support instead of to timedatectl.
func TestALicenseFromTheFutureBlamesTheClock(t *testing.T) {
	key := signingKey(t)
	p := enterprise()
	p.IssuedAt = time.Now().Add(48 * time.Hour).Unix()
	p.ExpireAt = time.Now().Add(96 * time.Hour).Unix()

	s := Load(Ref{Value: document(t, issue(t, key, p)), Source: "the test"})

	if s.State != StateInvalid {
		t.Fatalf("state = %q", s.State)
	}
	if !strings.Contains(s.Err.Error(), "clock") {
		t.Errorf("the message does not mention the clock: %v", s.Err)
	}
}

// The reference is a path or the document, told apart by the first byte.
// Moving a license from a file into an env var must not change the verdict.
func TestAPathAndTheDocumentItselfAgree(t *testing.T) {
	key := signingKey(t)
	doc := document(t, issue(t, key, enterprise()))
	path := filepath.Join(t.TempDir(), "license.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fromFile := Load(Ref{Value: path, Source: "a file"})
	fromInline := Load(Ref{Value: doc, Source: "a string"})

	if fromFile.State != StateValid {
		t.Fatalf("the file did not verify: %v", fromFile.Err)
	}
	if fromInline.State != StateValid {
		t.Fatalf("the inline document did not verify: %v", fromInline.Err)
	}
	if !fromFile.Allows(FeatureGuardrails) || !fromInline.Allows(FeatureGuardrails) {
		t.Error("the two readings granted different things")
	}
}

// A YAML block scalar and a pasted secret both carry leading whitespace. The
// first byte still has to read as "{".
func TestAnIndentedDocumentIsStillADocument(t *testing.T) {
	key := signingKey(t)
	doc := "\n  " + document(t, issue(t, key, enterprise())) + "\n"

	if s := Load(Ref{Value: doc}); s.State != StateValid {
		t.Fatalf("state = %q, err = %v", s.State, s.Err)
	}
}

// A path that does not resolve is the most common licensing mistake. The
// message names the knob that supplied it and what a good value looks like.
// This is the "not a stack trace" requirement, tested.
func TestAMissingFileNamesTheSourceAndTheFix(t *testing.T) {
	s := Load(Ref{Value: "/nonexistent/license.json", Source: "HOOP_LICENSE"})

	if s.State != StateInvalid {
		t.Fatalf("state = %q", s.State)
	}
	msg := s.Err.Error()
	for _, want := range []string{"HOOP_LICENSE", "/nonexistent/license.json", "paste the document"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message is missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "*errors.errorString") || strings.Contains(msg, "0x") {
		t.Errorf("the message reads like a dump: %s", msg)
	}
}

func TestGarbageIsRefusedWithAdvice(t *testing.T) {
	s := Load(Ref{Value: "{not json at all", Source: "the license config key"})

	if s.State != StateInvalid {
		t.Fatalf("state = %q", s.State)
	}
	if !strings.Contains(s.Err.Error(), "the license config key") {
		t.Errorf("the message does not name the source: %v", s.Err)
	}
	if s.License != nil {
		t.Error("a document that did not decode was reported as parsed")
	}
}

// Every attribute the signature covers has to arrive before checking the
// signature is worth anything, and each refusal names the field.
func TestIncompleteDocumentsNameTheMissingField(t *testing.T) {
	key := signingKey(t)
	cases := []struct {
		name  string
		edit  func(*Payload)
		wants string
	}{
		{"no type", func(p *Payload) { p.Type = "" }, "unknown license type"},
		{"no hosts", func(p *Payload) { p.AllowedHosts = nil }, "allowed hosts"},
		{"no description", func(p *Payload) { p.Description = "" }, "description"},
		{"no dates", func(p *Payload) { p.IssuedAt, p.ExpireAt = 0, 0 }, "issued_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := enterprise()
			tc.edit(&p)
			s := Load(Ref{Value: document(t, issue(t, key, p))})
			if s.State != StateInvalid {
				t.Fatalf("state = %q", s.State)
			}
			if !strings.Contains(s.Err.Error(), tc.wants) {
				t.Errorf("the message does not name the field: %v", s.Err)
			}
		})
	}
}

// An empty signature is not "nothing to check". PSS over zero bytes fails
// anyway; the attribute check is what makes the message say so.
func TestAnUnsignedDocumentIsRefused(t *testing.T) {
	signingKey(t)
	l := License{Payload: enterprise()}

	s := Load(Ref{Value: document(t, l)})

	if s.State != StateInvalid {
		t.Fatalf("an unsigned license verified: state = %q", s.State)
	}
	if !strings.Contains(s.Err.Error(), "signature") {
		t.Errorf("the message does not mention the signature: %v", s.Err)
	}
}

// Precedence is the spec: flag over environment over file, and the control
// plane over all three later. Resolve honours the order it is given.
func TestResolveTakesTheFirstSourceThatSuppliedSomething(t *testing.T) {
	key := signingKey(t)
	first := enterprise()
	first.Description = "the winner"
	second := enterprise()
	second.Description = "the loser"

	s := Resolve(
		Ref{Value: "  ", Source: "an empty flag"},
		Ref{Value: document(t, issue(t, key, first)), Source: EnvVar},
		Ref{Value: document(t, issue(t, key, second)), Source: "the license config key"},
	)

	if s.State != StateValid {
		t.Fatalf("state = %q, err = %v", s.State, s.Err)
	}
	if s.License.Payload.Description != "the winner" {
		t.Errorf("resolved %q", s.License.Payload.Description)
	}
	if s.Source != EnvVar {
		t.Errorf("source = %q, want %q", s.Source, EnvVar)
	}
}

// First wins, not first valid. Fall through from a broken license to a
// working one and the operator learns their env var never worked on the
// restart after the file changed.
func TestABrokenHigherSourceIsNotSkipped(t *testing.T) {
	key := signingKey(t)
	s := Resolve(
		Ref{Value: "/nonexistent/license.json", Source: EnvVar},
		Ref{Value: document(t, issue(t, key, enterprise())), Source: "the license config key"},
	)

	if s.State != StateInvalid {
		t.Fatalf("a broken license fell through to a working one: state = %q", s.State)
	}
	if s.Source != EnvVar {
		t.Errorf("source = %q, want %q", s.Source, EnvVar)
	}
}

func TestNoSourceIsMissingRatherThanAnError(t *testing.T) {
	s := Resolve(Ref{Source: "a flag"}, Ref{Value: "\n\t ", Source: EnvVar})

	if s.State != StateMissing {
		t.Fatalf("state = %q", s.State)
	}
	if s.Err != nil {
		t.Errorf("an absent license produced an error: %v", s.Err)
	}
	if s.Allows(FeatureGuardrails) {
		t.Error("a missing license granted a feature")
	}
	line := s.Line()
	for _, want := range []string{"missing", "free tier", EnvVar, `"license"`} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is missing %q: %q", want, line)
		}
	}
}

// An embedder calling Run with a Config built in Go gets the zero Status. It
// has to be the free tier, not a bypass.
func TestTheZeroStatusIsTheFreeTier(t *testing.T) {
	var s Status
	if s.State != StateMissing {
		t.Errorf("state = %q", s.State)
	}
	if s.Allows(FeatureGuardrails) || s.Allows(FeatureDataMasking) {
		t.Error("the zero value granted a feature")
	}
	if s.Line() == "" {
		t.Error("the zero value prints nothing at startup")
	}
}

// The endpoint reports what a running process concluded. It must never hand
// back a reusable document.
func TestReportOmitsTheSignature(t *testing.T) {
	key := signingKey(t)
	l := issue(t, key, enterprise())
	s := Load(Ref{Value: document(t, l), Source: EnvVar})

	got, err := json.Marshal(s.Report())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(got), l.Signature) {
		t.Errorf("the report carries the signature: %s", got)
	}
	for _, want := range []string{`"state":"valid"`, `"type":"enterprise"`, `"Acme Corp"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("the report is missing %s: %s", want, got)
		}
	}
}

// An absent feature list means every feature. JSON has to keep that apart
// from a list that happens to be empty.
func TestReportRendersFeaturesAsAList(t *testing.T) {
	key := signingKey(t)
	s := Load(Ref{Value: document(t, issue(t, key, enterprise()))})

	got, err := json.Marshal(s.Report())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(got), `"features":[]`) {
		t.Errorf("features did not render as an empty list: %s", got)
	}
}

func TestPublicKeyIDIsStable(t *testing.T) {
	id, err := PublicKeyID()
	if err != nil {
		t.Fatalf("PublicKeyID: %v", err)
	}
	if len(id) != 64 {
		t.Errorf("key id is not a sha256 hex digest: %q", id)
	}
	again, err := PublicKeyID()
	if err != nil || again != id {
		t.Errorf("PublicKeyID is not stable: %q, %q, %v", id, again, err)
	}
}

// The daemon reads ErrExpired to tell "renew this" from "not a license", so
// it has to stay matchable.
func TestExpiredIsMatchable(t *testing.T) {
	key := signingKey(t)
	p := enterprise()
	p.IssuedAt = time.Now().Add(-48 * time.Hour).Unix()
	p.ExpireAt = time.Now().Add(-time.Second).Unix()
	l := issue(t, key, p)

	if err := l.Verify(); !errors.Is(err, ErrExpired) {
		t.Errorf("Verify() = %v, want ErrExpired", err)
	}
}
