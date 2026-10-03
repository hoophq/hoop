package externaljwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

const testAudience = "https://hoop.example.com"

type testKey struct {
	kid  string
	alg  string
	priv any
}

func newRSAKey(t *testing.T, kid string) testKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{kid: kid, alg: "RS256", priv: k}
}

func newECKey(t *testing.T, kid string) testKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{kid: kid, alg: "ES256", priv: k}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (k testKey) jwk() map[string]string {
	switch priv := k.priv.(type) {
	case *rsa.PrivateKey:
		return map[string]string{"kty": "RSA", "kid": k.kid, "use": "sig", "alg": k.alg,
			"n": b64(priv.N.Bytes()), "e": b64(big.NewInt(int64(priv.E)).Bytes())}
	case *ecdsa.PrivateKey:
		ecdhKey, err := priv.PublicKey.ECDH()
		if err != nil {
			panic(err)
		}
		// Uncompressed point: 0x04 || X || Y, each 32 bytes on P-256.
		point := ecdhKey.Bytes()
		return map[string]string{"kty": "EC", "kid": k.kid, "use": "sig", "alg": k.alg, "crv": "P-256",
			"x": b64(point[1:33]), "y": b64(point[33:])}
	}
	panic("unknown key type")
}

func jwksOf(keys ...testKey) json.RawMessage {
	doc := map[string][]map[string]string{"keys": {}}
	for _, k := range keys {
		doc["keys"] = append(doc["keys"], k.jwk())
	}
	raw, _ := json.Marshal(doc)
	return raw
}

func sign(t *testing.T, k testKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.GetSigningMethod(k.alg), claims)
	tok.Header["kid"] = k.kid
	s, err := tok.SignedString(k.priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validClaims(issuer string, now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": issuer,
		"aud": []string{testAudience},
		"sub": "system:serviceaccount:ws-1:hoop-sidecar",
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
}

func TestOIDCVerifyStaticJWKS(t *testing.T) {
	const issuer = "https://kubernetes.default.svc.cluster.local"
	rsaKey, ecKey := newRSAKey(t, "rsa-1"), newECKey(t, "ec-1")
	jwks := jwksOf(rsaKey, ecKey)
	// A static key set is never fetched: any request fails the test.
	v := NewOIDCVerifier(OIDCOptions{HTTPClient: failingClient(t)})

	for _, k := range []testKey{rsaKey, ecKey} {
		claims := validClaims(issuer, time.Now())
		claims["email"] = "sidecar@p.iam.gserviceaccount.com"
		claims["email_verified"] = true
		got, err := v.Verify(context.Background(), sign(t, k, claims), issuer, testAudience, jwks)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", k.alg, err)
		}
		if got.Subject != "system:serviceaccount:ws-1:hoop-sidecar" || got.Email != "sidecar@p.iam.gserviceaccount.com" ||
			!got.EmailVerified || got.Issuer != issuer || got.ExpiresAt.IsZero() {
			t.Errorf("%s: claims not read: %+v", k.alg, got)
		}
	}

	// A string "true" is not a verified email.
	claims := validClaims(issuer, time.Now())
	claims["email_verified"] = "true"
	got, err := v.Verify(context.Background(), sign(t, rsaKey, claims), issuer, testAudience, jwks)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmailVerified {
		t.Error(`email_verified "true" (a string) read as verified`)
	}
}

func TestOIDCVerifyRefuses(t *testing.T) {
	const issuer = "https://issuer.example.com"
	key := newRSAKey(t, "k1")
	other := newRSAKey(t, "k1") // same kid, different key
	jwks := jwksOf(key)
	now := time.Now()

	mutate := func(f func(jwt.MapClaims)) string {
		c := validClaims(issuer, now)
		f(c)
		return sign(t, key, c)
	}
	none := func() string {
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(issuer, now))
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	hmac := func() string {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims(issuer, now))
		tok.Header["kid"] = "k1"
		s, err := tok.SignedString([]byte("shared-secret"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	for _, tt := range []struct {
		name  string
		token string
	}{
		{"wrong audience", mutate(func(c jwt.MapClaims) { c["aud"] = "https://other.example.com" })},
		{"no audience", mutate(func(c jwt.MapClaims) { delete(c, "aud") })},
		{"expired", mutate(func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Hour).Unix() })},
		{"no exp", mutate(func(c jwt.MapClaims) { delete(c, "exp") })},
		{"other issuer", mutate(func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com" })},
		{"alg none", none()},
		{"alg HS256", hmac()},
		{"signed by another key", sign(t, other, validClaims(issuer, now))},
		{"unknown kid", sign(t, newRSAKey(t, "k2"), validClaims(issuer, now))},
		{"larger than the limit", strings.Repeat("a", MaxOIDCTokenBytes+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := NewOIDCVerifier(OIDCOptions{HTTPClient: failingClient(t)})
			_, err := v.Verify(context.Background(), tt.token, issuer, testAudience, jwks)
			if !errors.Is(err, ErrValidationFailed) {
				t.Fatalf("want ErrValidationFailed, got %v", err)
			}
		})
	}
}

// issuerServer is an OIDC issuer over TLS whose key set the test can swap.
type issuerServer struct {
	*httptest.Server
	mu            sync.Mutex
	keys          json.RawMessage
	discoveryHits atomic.Int32
	jwksHits      atomic.Int32
	docIssuer     string
}

func newIssuerServer(t *testing.T, keys ...testKey) *issuerServer {
	t.Helper()
	s := &issuerServer{keys: jwksOf(keys...)}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			s.discoveryHits.Add(1)
			iss := s.docIssuer
			if iss == "" {
				iss = s.URL
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss, "jwks_uri": s.URL + "/keys"})
		case "/keys":
			s.jwksHits.Add(1)
			s.mu.Lock()
			defer s.mu.Unlock()
			_, _ = w.Write(s.keys)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *issuerServer) serve(keys ...testKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = jwksOf(keys...)
}

func TestOIDCDiscovery(t *testing.T) {
	key := newECKey(t, "k1")
	srv := newIssuerServer(t, key)
	v := NewOIDCVerifier(OIDCOptions{HTTPClient: srv.Client()})

	for i := 0; i < 3; i++ {
		if _, err := v.Verify(context.Background(), sign(t, key, validClaims(srv.URL, time.Now())), srv.URL, testAudience, nil); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}
	if d, j := srv.discoveryHits.Load(), srv.jwksHits.Load(); d != 1 || j != 1 {
		t.Errorf("keys are cached per issuer: want 1 discovery and 1 jwks fetch, got %d and %d", d, j)
	}
}

func TestOIDCDiscoveryRefusals(t *testing.T) {
	key := newRSAKey(t, "k1")

	t.Run("http issuer is never fetched", func(t *testing.T) {
		var hits atomic.Int32
		plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
		defer plain.Close()
		v := NewOIDCVerifier(OIDCOptions{HTTPClient: plain.Client()})
		_, err := v.Verify(context.Background(), sign(t, key, validClaims(plain.URL, time.Now())), plain.URL, testAudience, nil)
		if err == nil || !strings.Contains(err.Error(), "only https") {
			t.Fatalf("want an https refusal, got %v", err)
		}
		if hits.Load() != 0 {
			t.Errorf("an http issuer was fetched %d times", hits.Load())
		}
	})

	t.Run("discovery naming another issuer", func(t *testing.T) {
		srv := newIssuerServer(t, key)
		srv.docIssuer = "https://evil.example.com"
		v := NewOIDCVerifier(OIDCOptions{HTTPClient: srv.Client()})
		_, err := v.Verify(context.Background(), sign(t, key, validClaims(srv.URL, time.Now())), srv.URL, testAudience, nil)
		if err == nil || !strings.Contains(err.Error(), "names issuer") {
			t.Fatalf("want an issuer mismatch, got %v", err)
		}
		if srv.jwksHits.Load() != 0 {
			t.Error("the key set of a mismatched discovery document was fetched")
		}
	})

	t.Run("a failed fetch is not retried on every request", func(t *testing.T) {
		srv := newIssuerServer(t, key)
		srv.docIssuer = "https://evil.example.com"
		v := NewOIDCVerifier(OIDCOptions{HTTPClient: srv.Client()})
		for i := 0; i < 3; i++ {
			if _, err := v.Verify(context.Background(), sign(t, key, validClaims(srv.URL, time.Now())), srv.URL, testAudience, nil); err == nil {
				t.Fatal("want an error")
			}
		}
		if srv.discoveryHits.Load() != 1 {
			t.Errorf("want 1 discovery fetch within the refetch interval, got %d", srv.discoveryHits.Load())
		}
	})
}

// A rotated key reaches the plane through an unknown kid, at most once per
// refetch interval: a caller choosing kids must not turn requests into fetches.
func TestOIDCUnknownKIDRefetch(t *testing.T) {
	oldKey, newKey, bogus := newRSAKey(t, "old"), newRSAKey(t, "new"), newRSAKey(t, "bogus")
	srv := newIssuerServer(t, oldKey)
	v := NewOIDCVerifier(OIDCOptions{HTTPClient: srv.Client()})
	clock := time.Now()
	v.now = func() time.Time { return clock }
	verify := func(k testKey) error {
		_, err := v.Verify(context.Background(), sign(t, k, validClaims(srv.URL, clock)), srv.URL, testAudience, nil)
		return err
	}

	if err := verify(oldKey); err != nil {
		t.Fatal(err)
	}
	srv.serve(oldKey, newKey)

	// Within the interval of the first fetch: refused, nothing fetched.
	if err := verify(newKey); err == nil {
		t.Fatal("an unknown kid verified without a fetch")
	}
	if got := srv.jwksHits.Load(); got != 1 {
		t.Fatalf("want 1 jwks fetch inside the refetch interval, got %d", got)
	}

	clock = clock.Add(oidcRefetchInterval + time.Second)
	if err := verify(newKey); err != nil {
		t.Fatalf("the rotated key was not fetched: %v", err)
	}
	if got := srv.jwksHits.Load(); got != 2 {
		t.Fatalf("want 2 jwks fetches, got %d", got)
	}
	// A kid the issuer never published right after: still one fetch per interval.
	for i := 0; i < 5; i++ {
		if err := verify(bogus); err == nil {
			t.Fatal("a kid the issuer never published verified")
		}
	}
	if got := srv.jwksHits.Load(); got != 2 {
		t.Errorf("unknown kids caused %d extra fetches inside the interval", got-2)
	}

	// Past the TTL the keys are fetched again, and still verify meanwhile.
	clock = clock.Add(oidcKeyTTL)
	if err := verify(oldKey); err != nil {
		t.Fatal(err)
	}
	if got := srv.jwksHits.Load(); got != 3 {
		t.Errorf("want a refetch after the TTL, got %d fetches", got)
	}
}

func TestUnverifiedIssuer(t *testing.T) {
	key := newRSAKey(t, "k1")
	iss, err := UnverifiedIssuer(sign(t, key, validClaims("https://issuer.example.com", time.Now())))
	if err != nil || iss != "https://issuer.example.com" {
		t.Fatalf("iss=%q err=%v", iss, err)
	}
	noIss := validClaims("", time.Now())
	delete(noIss, "iss")
	for name, raw := range map[string]string{
		"not a jwt":    "hsc_not-a-jwt",
		"no iss":       sign(t, key, noIss),
		"over the cap": strings.Repeat("a", MaxOIDCTokenBytes+1),
	} {
		if _, err := UnverifiedIssuer(raw); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestValidateJWKS(t *testing.T) {
	if err := ValidateJWKS(jwksOf(newRSAKey(t, "k1"))); err != nil {
		t.Errorf("valid key set refused: %v", err)
	}
	for name, raw := range map[string]string{
		"empty set": `{"keys":[]}`,
		"null":      `null`,
		"not json":  `keys`,
		"hmac only": `{"keys":[{"kty":"oct","kid":"s","k":"c2VjcmV0"}]}`,
	} {
		if err := ValidateJWKS([]byte(raw)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// failingClient fails the test on any request: a path that must not fetch.
func failingClient(t *testing.T) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected fetch of %s", r.URL)
		return nil, errors.New("no network in this test")
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
