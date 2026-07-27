package localprovider

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/common/keys"
)

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	_, priv, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	p, err := New(Options{SharedSigningKey: priv})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestRenewAccessTokenSlidingSession(t *testing.T) {
	p := newTestProvider(t)

	original, err := p.NewAccessToken("subj-1", "u@x.io", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	renewed, err := p.RenewAccessToken(original, 12*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed == original {
		t.Fatal("renewal must mint a new token")
	}

	// Renewed token must verify and keep the identity.
	subject, err := p.VerifyAccessToken(renewed)
	if err != nil {
		t.Fatalf("verify renewed: %v", err)
	}
	if subject != "subj-1" {
		t.Fatalf("subject changed across renewal: %q", subject)
	}
}

func TestRenewAccessTokenRefusesBeyondMaxSessionAge(t *testing.T) {
	p := newTestProvider(t)

	// Simulate a session that originally authenticated 8 days ago by
	// minting with a pinned auth_time (still-valid expiry).
	authTime := time.Now().UTC().Add(-8 * 24 * time.Hour)
	old, err := keys.NewJwtTokenWithAuthTime(p.tokenSigningKey, "subj-2", "", time.Hour, authTime)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	_, err = p.RenewAccessToken(old, 12*time.Hour, 7*24*time.Hour)
	if err == nil {
		t.Fatal("renewal past the absolute cap must be refused")
	}
	if !strings.Contains(err.Error(), "maximum age") {
		t.Fatalf("expected the max-age error, got: %v", err)
	}
}

func TestRenewAccessTokenPreservesAuthTime(t *testing.T) {
	p := newTestProvider(t)
	pubKey, ok := p.tokenSigningKey.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("failed to derive public key")
	}

	authTime := time.Now().UTC().Add(-3 * 24 * time.Hour).Truncate(time.Second)
	tok, err := keys.NewJwtTokenWithAuthTime(p.tokenSigningKey, "subj-3", "a@b.c", time.Hour, authTime)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// Renew twice; auth_time must survive both hops so the absolute cap
	// keeps counting from the original login, not the last renewal.
	for i := 0; i < 2; i++ {
		tok, err = p.RenewAccessToken(tok, 12*time.Hour, 7*24*time.Hour)
		if err != nil {
			t.Fatalf("renew #%d: %v", i+1, err)
		}
	}
	claims, err := keys.VerifySessionClaims(tok, pubKey)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !claims.AuthTime.Equal(authTime) {
		t.Fatalf("auth_time drifted across renewals: got %v want %v", claims.AuthTime, authTime)
	}
	if claims.Email != "a@b.c" {
		t.Fatalf("email lost across renewals: %q", claims.Email)
	}
}

func TestRenewAccessTokenRejectsForeignToken(t *testing.T) {
	p := newTestProvider(t)
	other := newTestProvider(t)

	foreign, err := other.NewAccessToken("subj-4", "", time.Hour)
	if err != nil {
		t.Fatalf("mint foreign: %v", err)
	}
	if _, err := p.RenewAccessToken(foreign, 12*time.Hour, 7*24*time.Hour); err == nil {
		t.Fatal("a token signed by another key must not be renewable")
	}
}
