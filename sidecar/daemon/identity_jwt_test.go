package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

func jwtVerifier(t *testing.T, f *fakeIssuer, cfg IdentityConfig) *jwtIdentity {
	t.Helper()
	cfg.Type = IdentityJWT
	if cfg.Issuer == "" {
		cfg.Issuer = f.srv.URL
	}
	v, err := buildJWTIdentity(cfg, IdentityDeps{Roots: f.roots()})
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jwtIdentity)
}

// A token from the named issuer with the default claims becomes the person:
// sub as subject, email and groups carried over, the issuer recorded.
func TestJWTVerifierAcceptsTheIssuersToken(t *testing.T) {
	f := startIssuer(t)
	v := jwtVerifier(t, f, IdentityConfig{})
	for _, kid := range []string{"rsa-1", "ec-1"} {
		tok := f.token(t, kid, map[string]any{"sub": "jane@acme.com", "email": "jane@acme.com", "groups": []string{"dba", "analysts"}})
		id, err := v.Verify(context.Background(), "", tok, PeerInfo{})
		if err != nil {
			t.Fatalf("%s: %v", kid, err)
		}
		if id.Subject != "jane@acme.com" || id.Email != "jane@acme.com" {
			t.Errorf("%s: subject %q email %q", kid, id.Subject, id.Email)
		}
		if strings.Join(id.Groups, ",") != "dba,analysts" {
			t.Errorf("%s: groups %v", kid, id.Groups)
		}
		if id.Attributes[identityTypeAttribute] != IdentityJWT || id.Attributes["issuer"] != f.srv.URL {
			t.Errorf("%s: attributes %v", kid, id.Attributes)
		}
	}
}

// A protocol that carries a login user must agree with the token: the
// subject or the email passes, another name is refused, and a protocol
// with no login user (http) is not asked.
func TestJWTVerifierBindsTheLoginUserToTheToken(t *testing.T) {
	f := startIssuer(t)
	v := jwtVerifier(t, f, IdentityConfig{})
	tok := f.token(t, "rsa-1", map[string]any{"sub": "jane", "email": "jane@acme.com"})
	for _, user := range []string{"jane", "jane@acme.com", ""} {
		if _, err := v.Verify(context.Background(), user, tok, PeerInfo{}); err != nil {
			t.Errorf("user %q: %v", user, err)
		}
	}
	_, err := v.Verify(context.Background(), "bob@acme.com", tok, PeerInfo{})
	assertRefused(t, err, `the token names "jane", and the login user is "bob@acme.com"`)
}

// The claim map picks which claims become the identity; a groups claim
// written as one string is one group; an absent optional claim is empty.
func TestJWTVerifierMapsClaims(t *testing.T) {
	f := startIssuer(t)
	v := jwtVerifier(t, f, IdentityConfig{Claims: &IdentityClaimsConfig{Subject: "preferred_username", Email: "mail", Groups: "roles"}})
	tok := f.token(t, "rsa-1", map[string]any{"preferred_username": "jane", "mail": "jane@acme.com", "roles": "dba", "groups": []string{"ignored"}})
	id, err := v.Verify(context.Background(), "", tok, PeerInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "jane" || id.Email != "jane@acme.com" || strings.Join(id.Groups, ",") != "dba" {
		t.Errorf("identity %+v", id)
	}

	tok = f.token(t, "rsa-1", map[string]any{"preferred_username": "jane"})
	id, err = v.Verify(context.Background(), "", tok, PeerInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "" || len(id.Groups) != 0 {
		t.Errorf("optional claims absent, got %+v", id)
	}

	_, err = v.Verify(context.Background(), "", f.token(t, "rsa-1", map[string]any{"preferred_username": nil}), PeerInfo{})
	assertRefused(t, err, `no "preferred_username" claim`)
	_, err = v.Verify(context.Background(), "", f.token(t, "rsa-1", map[string]any{"preferred_username": 42}), PeerInfo{})
	assertRefused(t, err, "not a string")
}

// Every registered-claim failure is a refusal that names the problem and
// never the token.
func TestJWTVerifierRefusals(t *testing.T) {
	f := startIssuer(t)
	v := jwtVerifier(t, f, IdentityConfig{Audience: "appdb"})
	cases := []struct {
		name      string
		overrides map[string]any
		want      string
	}{
		{"wrong issuer", map[string]any{"iss": "https://other.example", "aud": "appdb"}, "issuer"},
		{"wrong audience", map[string]any{"aud": "hoop-sidecar"}, "audience"},
		{"expired", map[string]any{"aud": "appdb", "exp": time.Now().Add(-time.Hour).Unix()}, "expired"},
		{"not yet valid", map[string]any{"aud": "appdb", "nbf": time.Now().Add(time.Hour).Unix()}, "not valid before"},
		{"no sub", map[string]any{"aud": "appdb", "sub": nil}, "no sub"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), "", f.token(t, "rsa-1", c.overrides), PeerInfo{})
			assertRefused(t, err, c.want)
		})
	}
	_, err := v.Verify(context.Background(), "", "", PeerInfo{})
	assertRefused(t, err, "no token")
	_, err = v.Verify(context.Background(), "", "not.a.jwt.at.all", PeerInfo{})
	assertRefused(t, err, "not a JWT")

	// A token signed by another issuer's key with this issuer's name.
	other := startIssuer(t)
	forged := other.token(t, "rsa-1", map[string]any{"iss": f.srv.URL, "aud": "appdb"})
	_, err = v.Verify(context.Background(), "", forged, PeerInfo{})
	assertRefused(t, err, "signature")
}

// jwks_url skips discovery: the keys are read from the URL given, and the
// issuer's discovery document is never asked for.
func TestJWTVerifierReadsAStaticJWKSURL(t *testing.T) {
	f := startIssuer(t)
	v := jwtVerifier(t, f, IdentityConfig{JWKSURL: f.srv.URL + "/openid/v1/jwks"})
	if _, err := v.Verify(context.Background(), "", f.token(t, "ec-1", nil), PeerInfo{}); err != nil {
		t.Fatal(err)
	}
	if v.jwksURI != f.srv.URL+"/openid/v1/jwks" {
		t.Errorf("jwks uri %q", v.jwksURI)
	}
}

// An unreachable issuer is a verifier failure, not a refusal: the operator
// fixes the lane, the client is not told the token was bad.
func TestJWTVerifierIssuerErrors(t *testing.T) {
	if _, err := buildJWTIdentity(IdentityConfig{Type: IdentityJWT}, IdentityDeps{}); err == nil {
		t.Error("an empty issuer was accepted")
	}
	for _, issuer := range []string{"http://plain", "login.acme.com"} {
		if _, err := buildJWTIdentity(IdentityConfig{Issuer: issuer}, IdentityDeps{}); err == nil {
			t.Errorf("issuer %q was accepted", issuer)
		}
	}
	if _, err := buildJWTIdentity(IdentityConfig{Issuer: "https://login.acme.com", JWKSURL: "http://keys"}, IdentityDeps{}); err == nil {
		t.Error("a plain-http jwks_url was accepted")
	}
	v, err := buildJWTIdentity(IdentityConfig{Issuer: "https://127.0.0.1:1"}, IdentityDeps{})
	if err != nil {
		t.Fatal(err)
	}
	f := startIssuer(t)
	_, err = v.Verify(context.Background(), "", f.token(t, "rsa-1", map[string]any{"iss": "https://127.0.0.1:1"}), PeerInfo{})
	if err == nil || errors.Is(err, codectypes.ErrLoginRefused) {
		t.Errorf("want a verifier failure, got %v", err)
	}
}
