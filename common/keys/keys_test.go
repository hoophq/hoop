package keys

import (
	"crypto/ed25519"
	"errors"
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
