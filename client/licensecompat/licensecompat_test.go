// Package licensecompat pins the sidecar's license verifier to the
// gateway's, and client is the only module that can import both. Change the
// key or the signed bytes on one side and every licensed sidecar drops to
// the free tier at its next restart, with nothing in CI naming the commit.
package licensecompat

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	gateway "github.com/hoophq/hoop/common/license"
	sidecar "github.com/hoophq/hoop/sidecar/license"
)

// The two builds must trust the same signing key. Nothing else in either
// package can detect a rotation applied to one copy and not the other: every
// test on each side signs with a key it generated, so both keep passing while
// no real license verifies in the sidecar.
func TestTheTrustedKeysAreTheSame(t *testing.T) {
	want, err := gateway.PublicKeyID()
	if err != nil {
		t.Fatalf("common/license: %v", err)
	}
	got, err := sidecar.PublicKeyID()
	if err != nil {
		t.Fatalf("sidecar/license: %v", err)
	}
	if got != want {
		t.Errorf("the sidecar trusts a different license key\n gateway: %s\n sidecar: %s",
			want, got)
	}
}

// The two must sign over identical bytes, proved by cryptography rather than
// string comparison: the gateway signs with a key generated here, and the
// signature is checked against the SIDECAR's SigningData. PSS passes only if
// the two byte strings match.
func TestTheSignedBytesAreTheSame(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	cases := []struct {
		name     string
		features []string
	}{
		// A license signed before the features field existed. The
		// segment is appended only when present, and the gateway still
		// has customers holding one of these.
		{"no features", nil},
		{"one feature", []string{gateway.FeatureDataMasking}},
		{"several features", []string{gateway.FeatureDataMasking, gateway.FeatureGuardrails}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signed, err := gateway.Sign(key, gateway.EnterpriseType, "Acme Corp",
				[]string{"*.acme.example", "acme.example"}, tc.features, 720*time.Hour)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}

			sig, err := base64.StdEncoding.DecodeString(signed.Signature)
			if err != nil {
				t.Fatalf("decode signature: %v", err)
			}
			sum := sha256.Sum256(sidecarPayload(signed.Payload).SigningData())
			if err := rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, sum[:], sig, nil); err != nil {
				t.Errorf("the sidecar signs different bytes than the gateway: %v\n"+
					"sidecar: %q", err, sidecarPayload(signed.Payload).SigningData())
			}
		})
	}
}

// The document has to survive the trip in JSON: a license the gateway issues
// is pasted into a sidecar config unchanged, so every field the sidecar reads
// has to carry the gateway's tag.
func TestTheDocumentDecodesUnchanged(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issued, err := gateway.Sign(key, gateway.EnterpriseType, "Acme Corp",
		[]string{"acme.example"}, []string{gateway.FeatureGuardrails}, 720*time.Hour)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := json.Marshal(issued)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got sidecar.License
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the sidecar cannot decode a license the gateway issued: %v", err)
	}

	if got.KeyID != issued.KeyID || got.Signature != issued.Signature {
		t.Error("key_id or signature did not survive the decode")
	}
	if got.Payload.Type != issued.Payload.Type ||
		got.Payload.Description != issued.Payload.Description ||
		got.Payload.IssuedAt != issued.Payload.IssuedAt ||
		got.Payload.ExpireAt != issued.Payload.ExpireAt {
		t.Errorf("payload fields did not survive the decode:\n gateway: %+v\n sidecar: %+v",
			issued.Payload, got.Payload)
	}
	if len(got.Payload.AllowedHosts) != len(issued.Payload.AllowedHosts) {
		t.Error("allowed_hosts did not survive the decode")
	}
	if !got.IsFeatureEnabled(gateway.FeatureGuardrails) {
		t.Error("the feature the license names did not survive the decode")
	}
	if got.IsFeatureEnabled(gateway.FeatureDataMasking) {
		t.Error("the sidecar granted a feature the license does not name")
	}
}

// The feature keys the sidecar gates on have to be the ones the gateway
// issues licenses for. A typo in either constant is a license that verifies
// and unlocks nothing.
func TestTheFeatureKeysMatch(t *testing.T) {
	if sidecar.FeatureGuardrails != gateway.FeatureGuardrails {
		t.Errorf("guardrails key: sidecar %q, gateway %q",
			sidecar.FeatureGuardrails, gateway.FeatureGuardrails)
	}
	if sidecar.FeatureDataMasking != gateway.FeatureDataMasking {
		t.Errorf("data masking key: sidecar %q, gateway %q",
			sidecar.FeatureDataMasking, gateway.FeatureDataMasking)
	}
	if sidecar.EnterpriseType != gateway.EnterpriseType || sidecar.OSSType != gateway.OSSType {
		t.Error("the license types do not match")
	}
}

func sidecarPayload(p gateway.Payload) sidecar.Payload {
	return sidecar.Payload{
		Type:         p.Type,
		IssuedAt:     p.IssuedAt,
		ExpireAt:     p.ExpireAt,
		AllowedHosts: p.AllowedHosts,
		Description:  p.Description,
		Features:     p.Features,
	}
}
