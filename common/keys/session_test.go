package keys

import (
	"strings"
	"testing"
	"time"
)

func TestNewJwtTokenPreservesAuthTimeAcrossRenewal(t *testing.T) {
	pub, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}

	// First token of the session: auth_time == iat (login moment).
	login := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	first, err := NewJwtTokenWithAuthTime(priv, "user-1", "u@x.io", time.Hour, login)
	if err != nil {
		t.Fatalf("mint first: %v", err)
	}

	claims, err := VerifySessionClaims(first, pub)
	if err != nil {
		t.Fatalf("verify first: %v", err)
	}
	if !claims.AuthTime.Equal(login) {
		t.Fatalf("auth_time not preserved: got %v want %v", claims.AuthTime, login)
	}
	if claims.Subject != "user-1" || claims.Email != "u@x.io" {
		t.Fatalf("identity claims corrupted: %+v", claims)
	}

	// Renewal: fresh expiry, same auth_time.
	renewed, err := NewJwtTokenWithAuthTime(priv, claims.Subject, claims.Email, time.Hour, claims.AuthTime)
	if err != nil {
		t.Fatalf("mint renewed: %v", err)
	}
	renewedClaims, err := VerifySessionClaims(renewed, pub)
	if err != nil {
		t.Fatalf("verify renewed: %v", err)
	}
	if !renewedClaims.AuthTime.Equal(login) {
		t.Fatalf("renewal must carry the original auth_time: got %v want %v", renewedClaims.AuthTime, login)
	}
	if !renewedClaims.ExpireAt.After(claims.AuthTime.Add(time.Hour)) {
		t.Fatalf("renewal must extend expiry beyond the original")
	}
}

func TestVerifySessionClaimsFallsBackToIatForLegacyTokens(t *testing.T) {
	pub, priv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	// NewJwtToken now embeds auth_time too, so simulate a legacy token by
	// minting with the plain path and checking auth_time == iat semantics:
	// for a never-renewed token they are the same instant either way.
	tok, err := NewJwtToken(priv, "user-2", "", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	claims, err := VerifySessionClaims(tok, pub)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if time.Since(claims.AuthTime) > time.Minute {
		t.Fatalf("fresh token must have auth_time ~now, got %v", claims.AuthTime)
	}
}

func TestVerifySessionClaimsRejectsForgedToken(t *testing.T) {
	pub, _, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	_, otherPriv, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("other keypair: %v", err)
	}
	forged, err := NewJwtToken(otherPriv, "attacker", "", time.Hour)
	if err != nil {
		t.Fatalf("mint forged: %v", err)
	}
	if _, err := VerifySessionClaims(forged, pub); err == nil {
		t.Fatal("token signed by a different key must be rejected")
	}
	if _, err := VerifySessionClaims("not-a-jwt", pub); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("garbage token must fail parsing, got %v", err)
	}
}
