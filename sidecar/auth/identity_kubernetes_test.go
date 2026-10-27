package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// fakeIssuer is an OIDC issuer: discovery, a JWKS it can rotate, and a
// signer for tokens that name it.
type fakeIssuer struct {
	srv     *httptest.Server
	rsaKey  *rsa.PrivateKey
	ecKey   *ecdsa.PrivateKey
	kids    atomic.Value // []string served now
	fetches atomic.Int64
	// jwksURI is what discovery names; empty is the issuer's own.
	jwksURI atomic.Value
	// down makes discovery answer 503.
	down        atomic.Bool
	discoveries atomic.Int64
	// discoveryAuth is the Authorization header the last discovery saw.
	discoveryAuth atomic.Value
}

func startIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	f := &fakeIssuer{}
	var err error
	if f.rsaKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	if f.ecKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	f.kids.Store([]string{"rsa-1", "ec-1"})
	f.jwksURI.Store("")
	f.discoveryAuth.Store("")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		f.discoveries.Add(1)
		f.discoveryAuth.Store(r.Header.Get("Authorization"))
		if f.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		jwks := f.jwksURI.Load().(string)
		if jwks == "" {
			jwks = f.srv.URL + "/openid/v1/jwks"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "jwks_uri": jwks})
	})
	mux.HandleFunc("GET /openid/v1/jwks", f.jwks)
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIssuer) jwks(w http.ResponseWriter, r *http.Request) {
	f.fetches.Add(1)
	var keys []map[string]string
	for _, kid := range f.kids.Load().([]string) {
		switch {
		case strings.HasPrefix(kid, "rsa"):
			keys = append(keys, map[string]string{"kty": "RSA", "kid": kid, "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(f.rsaKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.rsaKey.E)).Bytes())})
		default:
			keys = append(keys, map[string]string{"kty": "EC", "kid": kid, "crv": "P-256",
				"x": base64.RawURLEncoding.EncodeToString(f.ecKey.X.FillBytes(make([]byte, 32))),
				"y": base64.RawURLEncoding.EncodeToString(f.ecKey.Y.FillBytes(make([]byte, 32)))})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

func (f *fakeIssuer) roots() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(f.srv.Certificate())
	return pool
}

// token signs claims with the key kid names; overrides patch the claims.
func (f *fakeIssuer) token(t *testing.T, kid string, overrides map[string]any) string {
	t.Helper()
	claims := map[string]any{
		"iss": f.srv.URL, "sub": "system:serviceaccount:payments:api", "aud": []string{"hoop-sidecar"},
		"exp": time.Now().Add(time.Hour).Unix(), "nbf": time.Now().Add(-time.Minute).Unix(),
		"kubernetes.io": map[string]any{
			"namespace":      "payments",
			"pod":            map[string]string{"name": "api-7d9f", "uid": "pod-uid-1"},
			"serviceaccount": map[string]string{"name": "api"},
		},
	}
	for k, v := range overrides {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	alg := "RS256"
	if strings.HasPrefix(kid, "ec") {
		alg = "ES256"
	}
	hdr, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(signing))
	var sig []byte
	var err error
	if alg == "RS256" {
		sig, err = rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, digest[:])
	} else {
		var r, s *big.Int
		r, s, err = ecdsa.Sign(rand.Reader, f.ecKey, digest[:])
		if err == nil {
			sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func kubernetesVerifier(t *testing.T, f *fakeIssuer, cfg daemon.IdentityConfig) *kubernetesIdentity {
	t.Helper()
	cfg.Type = daemon.IdentityKubernetes
	if cfg.Issuer == "" {
		cfg.Issuer = f.srv.URL
	}
	v, err := buildKubernetesIdentity(cfg, daemon.IdentityDeps{Roots: f.roots()})
	if err != nil {
		t.Fatal(err)
	}
	return v.(*kubernetesIdentity)
}

func assertRefused(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, codectypes.ErrLoginRefused) || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want a refusal saying %q", err, want)
	}
}

// A projected token verifies through discovery and the JWKS, under either
// algorithm, and the identity carries the pod facts.
func TestKubernetesVerifierAcceptsATokenFromTheIssuer(t *testing.T) {
	f := startIssuer(t)
	v := kubernetesVerifier(t, f, daemon.IdentityConfig{})
	for _, kid := range []string{"rsa-1", "ec-1"} {
		id, err := v.Verify(context.Background(), "ignored", f.token(t, kid, nil), daemon.PeerInfo{})
		if err != nil {
			t.Fatalf("%s: %v", kid, err)
		}
		if id.Subject != "system:serviceaccount:payments:api" || id.Attributes["namespace"] != "payments" ||
			id.Attributes["pod"] != "api-7d9f" || id.Attributes["service_account"] != "api" ||
			id.Attributes[daemon.IdentityTypeAttribute] != daemon.IdentityKubernetes {
			t.Fatalf("%s: identity = %+v", kid, id)
		}
	}
	if n := f.fetches.Load(); n != 1 {
		t.Fatalf("the JWKS was fetched %d times for two logins; want once", n)
	}
}

func TestKubernetesVerifierRefusals(t *testing.T) {
	f := startIssuer(t)
	other := startIssuer(t)
	v := kubernetesVerifier(t, f, daemon.IdentityConfig{})
	bad := f.token(t, "rsa-1", nil)
	bad = bad[:len(bad)-4] + "AAAA"
	for name, tc := range map[string]struct{ token, want string }{
		"empty":          {"", "no service account token"},
		"not a jwt":      {"abc", "not a JWT"},
		"wrong audience": {f.token(t, "rsa-1", map[string]any{"aud": "other"}), "audience"},
		"expired":        {f.token(t, "rsa-1", map[string]any{"exp": time.Now().Add(-2 * time.Minute).Unix()}), "expired at"},
		"no exp":         {f.token(t, "rsa-1", map[string]any{"exp": nil}), "no exp"},
		"not yet":        {f.token(t, "rsa-1", map[string]any{"nbf": time.Now().Add(time.Hour).Unix()}), "not valid before"},
		"other issuer":   {other.token(t, "rsa-1", nil), "issuer"},
		"forged issuer":  {other.token(t, "rsa-1", map[string]any{"iss": f.srv.URL}), "does not verify"},
		"tampered":       {bad, "does not verify"},
		"no sub":         {f.token(t, "rsa-1", map[string]any{"sub": nil}), "no sub"},
		"unknown kid":    {f.token(t, "rsa-9", nil), `key "rsa-9"`},
		"unsigned alg":   {strings.Replace(f.token(t, "rsa-1", nil), base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"rsa-1","typ":"JWT"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"rsa-1","typ":"JWT"}`)), 1), "alg"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), "", tc.token, daemon.PeerInfo{})
			assertRefused(t, err, tc.want)
		})
	}
}

// A rotated signing key is picked up on the next unknown kid, and a flood
// of unknown kids costs one fetch a minute.
func TestKubernetesVerifierRefreshesTheJWKSOnAnUnknownKidOnce(t *testing.T) {
	f := startIssuer(t)
	v := kubernetesVerifier(t, f, daemon.IdentityConfig{})
	now := time.Now()
	v.now = func() time.Time { return now }
	if _, err := v.Verify(context.Background(), "", f.token(t, "rsa-1", nil), daemon.PeerInfo{}); err != nil {
		t.Fatal(err)
	}
	// The issuer rotates: the verifier has not seen rsa-2 and asks again.
	f.kids.Store([]string{"rsa-2"})
	_, err := v.Verify(context.Background(), "", f.token(t, "rsa-2", nil), daemon.PeerInfo{})
	assertRefused(t, err, `key "rsa-2"`) // within the minute: no fetch
	if n := f.fetches.Load(); n != 1 {
		t.Fatalf("fetches = %d after a refused refresh, want 1", n)
	}
	now = now.Add(2 * jwksRefreshMin)
	if _, err := v.Verify(context.Background(), "", f.token(t, "rsa-2", nil), daemon.PeerInfo{}); err != nil {
		t.Fatalf("after the rotation: %v", err)
	}
	if n := f.fetches.Load(); n != 2 {
		t.Fatalf("fetches = %d, want 2", n)
	}
	// And the old key is gone.
	_, err = v.Verify(context.Background(), "", f.token(t, "rsa-1", nil), daemon.PeerInfo{})
	assertRefused(t, err, `key "rsa-1"`)
}

// The pod's token goes to the issuer and to a JWKS the issuer itself
// serves; a discovery document naming another host gets the keys read
// anonymously, and one naming a plain-http URL is refused.
func TestKubernetesVerifierSendsThePodTokenToTheIssuerAlone(t *testing.T) {
	f := startIssuer(t)
	var otherAuth atomic.Value
	otherAuth.Store("unset")
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherAuth.Store(r.Header.Get("Authorization"))
		f.jwks(w, r)
	}))
	t.Cleanup(other.Close)
	f.jwksURI.Store(other.URL + "/keys")

	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("pod-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := kubernetesVerifier(t, f, daemon.IdentityConfig{})
	v.inCluster, v.caFile, v.tokenFile = true, ca, token
	if _, err := v.Verify(context.Background(), "", f.token(t, "rsa-1", nil), daemon.PeerInfo{}); err != nil {
		t.Fatal(err)
	}
	if got := f.discoveryAuth.Load(); got != "Bearer pod-token" {
		t.Errorf("discovery saw Authorization %q, want the pod token", got)
	}
	if got := otherAuth.Load(); got != "" {
		t.Errorf("the JWKS on another host saw Authorization %q, want none", got)
	}

	// The issuer's own JWKS, same host, gets the token.
	f.jwksURI.Store("")
	var sameAuth atomic.Value
	sameAuth.Store("unset")
	v = kubernetesVerifier(t, f, daemon.IdentityConfig{})
	v.inCluster, v.caFile, v.tokenFile = true, ca, token
	v.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/jwks") {
			sameAuth.Store(r.Header.Get("Authorization"))
		}
		return f.srv.Client().Transport.RoundTrip(r)
	})}
	if _, err := v.Verify(context.Background(), "", f.token(t, "rsa-1", nil), daemon.PeerInfo{}); err != nil {
		t.Fatal(err)
	}
	if got := sameAuth.Load(); got != "Bearer pod-token" {
		t.Errorf("the issuer's JWKS saw Authorization %q, want the pod token", got)
	}

	// A plain-http JWKS is refused before any request to it.
	f.jwksURI.Store("http://" + other.Listener.Addr().String() + "/keys")
	v = kubernetesVerifier(t, f, daemon.IdentityConfig{})
	_, err := v.Verify(context.Background(), "", f.token(t, "rsa-1", nil), daemon.PeerInfo{})
	if err == nil || errors.Is(err, codectypes.ErrLoginRefused) || !strings.Contains(err.Error(), "not an https URL") {
		t.Fatalf("plain-http jwks_uri: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// An issuer that is down is asked once a minute, not on every login: the
// logins in between fail without a request.
func TestKubernetesVerifierAsksADownIssuerOnceAMinute(t *testing.T) {
	f := startIssuer(t)
	f.down.Store(true)
	v := kubernetesVerifier(t, f, daemon.IdentityConfig{})
	now := time.Now()
	v.now = func() time.Time { return now }
	tok := f.token(t, "rsa-1", nil)
	for i := range 2 {
		_, err := v.Verify(context.Background(), "", tok, daemon.PeerInfo{})
		if err == nil || errors.Is(err, codectypes.ErrLoginRefused) {
			t.Fatalf("login %d: %v, want a failure", i, err)
		}
	}
	if n := f.discoveries.Load(); n != 1 {
		t.Fatalf("discovery asked %d times while down, want 1", n)
	}
	f.down.Store(false)
	now = now.Add(2 * jwksRefreshMin)
	if _, err := v.Verify(context.Background(), "", tok, daemon.PeerInfo{}); err != nil {
		t.Fatalf("after the issuer came back: %v", err)
	}
	if n := f.discoveries.Load(); n != 2 {
		t.Fatalf("discovery asked %d times, want 2", n)
	}
}

// POD_UID binds the lane to its own pod: a token minted for another pod of
// the same service account is refused.
func TestKubernetesVerifierPinsThePod(t *testing.T) {
	f := startIssuer(t)
	t.Setenv(podUIDEnv, "pod-uid-1")
	v := kubernetesVerifier(t, f, daemon.IdentityConfig{Audience: "pg-prod"})
	if _, err := v.Verify(context.Background(), "", f.token(t, "ec-1", map[string]any{"aud": "pg-prod"}), daemon.PeerInfo{}); err != nil {
		t.Fatal(err)
	}
	stolen := f.token(t, "ec-1", map[string]any{"aud": "pg-prod", "kubernetes.io": map[string]any{
		"namespace": "payments", "pod": map[string]string{"name": "api-other", "uid": "pod-uid-2"}}})
	_, err := v.Verify(context.Background(), "", stolen, daemon.PeerInfo{})
	assertRefused(t, err, `issued to pod "api-other"`)
}

// An unreachable issuer is a failure, not a refusal by rule, and the
// builder refuses an issuer that is not https.
func TestKubernetesVerifierIssuerErrors(t *testing.T) {
	v, err := buildKubernetesIdentity(daemon.IdentityConfig{Type: daemon.IdentityKubernetes, Issuer: "https://127.0.0.1:1"}, daemon.IdentityDeps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f := startIssuer(t)
	_, err = v.Verify(ctx, "", f.token(t, "rsa-1", map[string]any{"iss": "https://127.0.0.1:1"}), daemon.PeerInfo{})
	if err == nil || errors.Is(err, codectypes.ErrLoginRefused) {
		t.Fatalf("want a transport error, not a refusal: %v", err)
	}
	for _, issuer := range []string{"http://plain", "kubernetes.default.svc", "https://"} {
		if _, err := buildKubernetesIdentity(daemon.IdentityConfig{Issuer: issuer}, daemon.IdentityDeps{}); err == nil {
			t.Errorf("issuer %q was accepted", issuer)
		}
	}
	// The in-cluster default needs the mounted CA; off a cluster it says so.
	v, err = buildKubernetesIdentity(daemon.IdentityConfig{}, daemon.IdentityDeps{})
	if err != nil {
		t.Fatal(err)
	}
	k := v.(*kubernetesIdentity)
	if k.issuer != kubernetesInClusterIssuer || !k.inCluster {
		t.Fatalf("default issuer = %q in-cluster %v", k.issuer, k.inCluster)
	}
}
