package keys

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifyExpiredTokenIsTyped(t *testing.T) {
	_, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := NewJwtToken(priv, "subj", "u@x.io", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)

	if _, err := VerifyAccessToken(tok, pub); !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("VerifyAccessToken: want jwt.ErrTokenExpired, got %v", err)
	}
	if _, err := VerifySessionClaims(tok, pub); !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("VerifySessionClaims: want jwt.ErrTokenExpired, got %v", err)
	}
}

// Callers treat ErrTokenExpired as benign, so it must never be reported for
// a token that also fails signature or structure checks.
func TestVerifyExpiredTokenTamperedIsNotExpired(t *testing.T) {
	_, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := NewJwtToken(priv, "subj", "u@x.io", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)

	parts := strings.Split(tok, ".")
	parts[2] = strings.Repeat("A", len(parts[2]))
	_, err = VerifyAccessToken(strings.Join(parts, "."), pub)
	if !errors.Is(err, jwt.ErrTokenSignatureInvalid) || errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("tampered signature: want only jwt.ErrTokenSignatureInvalid, got %v", err)
	}

	_, err = VerifyAccessToken("not.a.jwt", pub)
	if !errors.Is(err, jwt.ErrTokenMalformed) || errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("malformed: want only jwt.ErrTokenMalformed, got %v", err)
	}
}
