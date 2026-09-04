// Package licensetest issues licenses that a test process genuinely verifies.
//
// The verifier has no bypass on purpose: license.Status cannot be assembled by
// hand and license.Load grants nothing without a signature from the key the
// build trusts. That leaves a test with no way to exercise a licensed daemon,
// so this package closes the loop honestly. It generates a keypair, points the
// trust root at it for the duration of one test, and signs real documents that
// go through the real Load.
//
// Every function takes a *testing.T, and that is the guard. Production code
// calling one would have to import "testing" and fabricate a T, which no
// review lets through and which the binary size announces anyway.
package licensetest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/internal/trust"
)

// Enterprise is a current enterprise payload for "Acme Corp". Naming no
// features means every feature, which is what most licenses carry.
func Enterprise(features ...string) license.Payload {
	return license.Payload{
		Type:         license.EnterpriseType,
		IssuedAt:     time.Now().Add(-time.Hour).Unix(),
		ExpireAt:     time.Now().Add(720 * time.Hour).Unix(),
		AllowedHosts: []string{"*"},
		Description:  "Acme Corp",
		Features:     features,
	}
}

// Expiring is Enterprise with the term ending after d. A negative d has
// already ended.
func Expiring(d time.Duration) license.Payload {
	p := Enterprise()
	p.ExpireAt = time.Now().Add(d).Unix()
	return p
}

// Sign issues a license for the payload and makes this process trust the key
// that signed it until t finishes.
//
// The trust root is process-wide, so a test calling this must not run beside
// one that reads it: no t.Parallel in a package that signs.
func Sign(t *testing.T, p license.Payload) license.License {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("licensetest: generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("licensetest: marshal public key: %v", err)
	}
	t.Cleanup(trust.Swap(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))

	sum := sha256.Sum256(p.SigningData())
	sig, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, sum[:], nil)
	if err != nil {
		t.Fatalf("licensetest: sign: %v", err)
	}
	id, err := license.PublicKeyID()
	if err != nil {
		t.Fatalf("licensetest: key id: %v", err)
	}
	return license.License{Payload: p, KeyID: id, Signature: base64.StdEncoding.EncodeToString(sig)}
}

// Ref is Sign marshalled into the reference a config or a flag would carry,
// ready for license.Load or Config.UseLicense.
func Ref(t *testing.T, p license.Payload) license.Ref {
	t.Helper()
	return license.Ref{Value: Document(t, p), Source: "the test"}
}

// Document is the signed license as the JSON an operator pastes.
func Document(t *testing.T, p license.Payload) string {
	t.Helper()
	b, err := json.Marshal(Sign(t, p))
	if err != nil {
		t.Fatalf("licensetest: marshal license: %v", err)
	}
	return string(b)
}

// Status is the verdict Load reaches for a freshly signed license. It went
// through the real verifier, so a test asserting on it is asserting on what a
// customer gets.
func Status(t *testing.T, p license.Payload) license.Status {
	t.Helper()
	s := license.Load(Ref(t, p))
	if s.License == nil {
		t.Fatalf("licensetest: the signed license did not load: %v", s.Err)
	}
	return s
}
