package auth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
)

// A kubernetes lane takes a projected service account token as the login
// secret and verifies it the way the API server would: against the issuer's
// JWKS, found through OIDC discovery. In-cluster the issuer is the API
// server itself, reached with the mounted CA and the pod's own token, since
// the discovery documents are not anonymous by default. An operator with
// the issuer published elsewhere (a managed cluster's OIDC URL) names it.
const (
	kubernetesInClusterIssuer = "https://kubernetes.default.svc"
	kubernetesCAFile          = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	// podUIDEnv is the downward-API variable the chart sets. When present
	// a token has to be this pod's: a token stolen from another pod of the
	// same service account is refused.
	podUIDEnv = "POD_UID"

	// jwksRefreshMin bounds how often an unknown kid triggers a fetch, so
	// a flood of forged tokens cannot turn into a flood of requests to the
	// API server.
	jwksRefreshMin = time.Minute
	jwksTimeout    = 10 * time.Second
	maxJWKSBytes   = 1 << 20

	// jwtClockSkew is the leeway on exp and nbf.
	jwtClockSkew = time.Minute
)

// audience is the token audience a lane requires.
func audience(cfg daemon.IdentityConfig) string {
	if cfg.Audience != "" {
		return cfg.Audience
	}
	return daemon.DefaultIdentityAudience
}

type kubernetesIdentity struct {
	kind      string // the identity type, for log and error text
	issuer    string
	audience  string
	inCluster bool
	roots     *x509.CertPool
	log       *slog.Logger
	podUID    string
	now       func() time.Time

	mu        sync.Mutex
	client    *http.Client
	jwksURI   string
	keys      map[string]crypto.PublicKey
	lastFetch time.Time
}

func buildKubernetesIdentity(cfg daemon.IdentityConfig, deps daemon.IdentityDeps) (daemon.IdentitySource, error) {
	issuer := strings.TrimRight(cfg.Issuer, "/")
	if issuer == "" {
		issuer = kubernetesInClusterIssuer
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("identity.issuer %q is not an https URL", cfg.Issuer)
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &kubernetesIdentity{
		kind:      daemon.IdentityKubernetes,
		issuer:    issuer,
		audience:  audience(cfg),
		inCluster: cfg.Issuer == "",
		roots:     deps.Roots,
		log:       log,
		podUID:    strings.TrimSpace(os.Getenv(podUIDEnv)),
		now:       time.Now,
	}, nil
}

// jwtHeader is the JOSE header this verifier reads.
type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// k8sClaims are the claims a projected token carries that this verifier
// checks or records.
type k8sClaims struct {
	Iss string      `json:"iss"`
	Sub string      `json:"sub"`
	Aud jwtAudience `json:"aud"`
	Exp int64       `json:"exp"`
	Nbf int64       `json:"nbf"`
	K8s struct {
		Namespace string `json:"namespace"`
		Pod       struct {
			Name string `json:"name"`
			UID  string `json:"uid"`
		} `json:"pod"`
		ServiceAccount struct {
			Name string `json:"name"`
		} `json:"serviceaccount"`
	} `json:"kubernetes.io"`
}

// jwtAudience decodes aud written as a string or a list.
type jwtAudience []string

func (a *jwtAudience) UnmarshalJSON(raw []byte) error {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		*a = jwtAudience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (k *kubernetesIdentity) Verify(ctx context.Context, _ string, secret string, _ daemon.PeerInfo) (session.Identity, error) {
	if secret == "" {
		return session.Identity{}, daemon.Refused("no service account token presented")
	}
	var claims k8sClaims
	if err := k.verifyToken(ctx, secret, &claims); err != nil {
		return session.Identity{}, err
	}
	if k.podUID != "" && claims.K8s.Pod.UID != k.podUID {
		return session.Identity{}, daemon.Refused("the token was issued to pod %q, and this sidecar serves pod %q", claims.K8s.Pod.Name, k.podUID)
	}

	attrs := map[string]string{daemon.IdentityTypeAttribute: daemon.IdentityKubernetes}
	if claims.K8s.Namespace != "" {
		attrs["namespace"] = claims.K8s.Namespace
	}
	if claims.K8s.Pod.Name != "" {
		attrs["pod"] = claims.K8s.Pod.Name
	}
	if claims.K8s.ServiceAccount.Name != "" {
		attrs["service_account"] = claims.K8s.ServiceAccount.Name
	}
	return session.Identity{Subject: claims.Sub, Attributes: attrs}, nil
}

// stdClaims are the registered claims every token is checked on.
type stdClaims struct {
	Iss string      `json:"iss"`
	Sub string      `json:"sub"`
	Aud jwtAudience `json:"aud"`
	Exp int64       `json:"exp"`
	Nbf int64       `json:"nbf"`
}

// verifyToken checks a compact JWS end to end: shape, alg, the registered
// claims against the issuer, audience and clock, then the signature against
// the issuer's key. into receives the full claim set once the registered
// ones pass; a verifier reads its own claims from it afterwards.
func (k *kubernetesIdentity) verifyToken(ctx context.Context, secret string, into any) error {
	parts := strings.Split(secret, ".")
	if len(parts) != 3 {
		return daemon.Refused("the secret is not a JWT")
	}
	var hdr jwtHeader
	if err := decodeJWTPart(parts[0], &hdr); err != nil {
		return daemon.Refused("JWT header: %v", err)
	}
	var std stdClaims
	if err := decodeJWTPart(parts[1], &std); err != nil {
		return daemon.Refused("JWT claims: %v", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return daemon.Refused("JWT signature is not base64url")
	}

	now := k.now()
	switch {
	case hdr.Alg != "RS256" && hdr.Alg != "ES256":
		return daemon.Refused("JWT alg %q is not RS256 or ES256", hdr.Alg)
	case std.Iss != k.issuer:
		return daemon.Refused("JWT issuer %q is not %s", std.Iss, k.issuer)
	case !containsString(std.Aud, k.audience):
		return daemon.Refused("JWT audience %v does not include %q", []string(std.Aud), k.audience)
	case std.Exp == 0:
		return daemon.Refused("JWT has no exp")
	case now.After(time.Unix(std.Exp, 0).Add(jwtClockSkew)):
		return daemon.Refused("JWT expired at %s", time.Unix(std.Exp, 0).UTC().Format(time.RFC3339))
	case std.Nbf != 0 && now.Before(time.Unix(std.Nbf, 0).Add(-jwtClockSkew)):
		return daemon.Refused("JWT is not valid before %s", time.Unix(std.Nbf, 0).UTC().Format(time.RFC3339))
	case std.Sub == "":
		return daemon.Refused("JWT has no sub")
	}

	key, err := k.keyFor(ctx, hdr.Kid)
	if err != nil {
		return err
	}
	signed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !verifyJWS(hdr.Alg, key, signed[:], sig) {
		return daemon.Refused("JWT signature does not verify against key %q of %s", hdr.Kid, k.issuer)
	}
	if err := decodeJWTPart(parts[1], into); err != nil {
		return daemon.Refused("JWT claims: %v", err)
	}
	return nil
}

func decodeJWTPart(part string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return errors.New("not base64url")
	}
	return json.Unmarshal(raw, into)
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// verifyJWS checks a JWS signature over the SHA-256 digest of the signing
// input. ES256 carries r||s raw, not DER.
func verifyJWS(alg string, key crypto.PublicKey, digest, sig []byte) bool {
	switch alg {
	case "RS256":
		pub, ok := key.(*rsa.PublicKey)
		return ok && rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest, sig) == nil
	case "ES256":
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok || len(sig) != 64 {
			return false
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(pub, digest, r, s)
	}
	return false
}

// keyFor returns the issuer's key named kid, fetching the JWKS on the first
// call and again on an unknown kid, at most once a minute: a rotation lands
// at the next login, a flood of bad kids costs one request.
func (k *kubernetesIdentity) keyFor(ctx context.Context, kid string) (crypto.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if key, ok := k.keys[kid]; ok {
		return key, nil
	}
	if k.keys != nil && k.now().Sub(k.lastFetch) < jwksRefreshMin {
		return nil, daemon.Refused("JWT key %q is not one %s publishes", kid, k.issuer)
	}
	if err := k.fetchKeysLocked(ctx); err != nil {
		return nil, fmt.Errorf("identity %s: %w", k.kind, err)
	}
	if key, ok := k.keys[kid]; ok {
		return key, nil
	}
	return nil, daemon.Refused("JWT key %q is not one %s publishes", kid, k.issuer)
}

// fetchKeysLocked runs discovery once, then reads the JWKS. Called with mu
// held.
func (k *kubernetesIdentity) fetchKeysLocked(ctx context.Context) error {
	if k.client == nil {
		c, err := k.buildClient()
		if err != nil {
			return err
		}
		k.client = c
	}
	// The fetch time is recorded whatever the outcome, so an issuer that
	// is down is asked again in a minute, not on every login.
	k.lastFetch = k.now()
	if k.jwksURI == "" {
		var disc struct {
			Issuer  string `json:"issuer"`
			JWKSURI string `json:"jwks_uri"`
		}
		if err := k.getJSON(ctx, k.issuer+"/.well-known/openid-configuration", &disc); err != nil {
			return fmt.Errorf("OIDC discovery at %s: %w", k.issuer, err)
		}
		if strings.TrimRight(disc.Issuer, "/") != k.issuer {
			return fmt.Errorf("OIDC discovery at %s names issuer %q", k.issuer, disc.Issuer)
		}
		if disc.JWKSURI == "" {
			return fmt.Errorf("OIDC discovery at %s names no jwks_uri", k.issuer)
		}
		k.jwksURI = disc.JWKSURI
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := k.getJSON(ctx, k.jwksURI, &set); err != nil {
		return fmt.Errorf("JWKS at %s: %w", k.jwksURI, err)
	}
	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, raw := range set.Keys {
		kid, key, err := parseJWK(raw)
		if err != nil {
			k.log.Warn("skipping a JWKS key", "issuer", k.issuer, "error", err)
			continue
		}
		keys[kid] = key
	}
	if len(keys) == 0 {
		return fmt.Errorf("JWKS at %s holds no usable key", k.jwksURI)
	}
	k.keys = keys
	return nil
}

// buildClient is the HTTP client for discovery and the JWKS. In-cluster it
// trusts the mounted CA and authenticates with the pod's own token, re-read
// per request because the kubelet rotates it.
func (k *kubernetesIdentity) buildClient() (*http.Client, error) {
	tlsConf := &tls.Config{RootCAs: k.roots, MinVersion: tls.VersionTLS12}
	if k.inCluster {
		pem, err := os.ReadFile(kubernetesCAFile)
		if err != nil {
			return nil, fmt.Errorf("this process is not in a cluster (%v); set identity.issuer to the cluster's OIDC issuer URL", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no certificate", kubernetesCAFile)
		}
		tlsConf.RootCAs = pool
	}
	return &http.Client{
		Timeout:   jwksTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsConf, Proxy: http.ProxyFromEnvironment},
	}, nil
}

func (k *kubernetesIdentity) getJSON(ctx context.Context, rawURL string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", daemon.UserAgent("identity/kubernetes"))
	if k.inCluster {
		tok, err := os.ReadFile(daemon.KubernetesTokenFile)
		if err != nil {
			return fmt.Errorf("reading the pod's own token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+string(bytes.TrimSpace(tok)))
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %s", resp.Status)
	}
	if len(body) > maxJWKSBytes {
		return fmt.Errorf("answered more than %d bytes", maxJWKSBytes)
	}
	return json.Unmarshal(body, into)
}

// parseJWK reads one RSA or P-256 key of a JWKS.
func parseJWK(raw json.RawMessage) (string, crypto.PublicKey, error) {
	var jwk struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Crv string `json:"crv"`
		N   string `json:"n"`
		E   string `json:"e"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	if err := json.Unmarshal(raw, &jwk); err != nil {
		return "", nil, err
	}
	if jwk.Use != "" && jwk.Use != "sig" {
		return "", nil, fmt.Errorf("key %q is for %s, not sig", jwk.Kid, jwk.Use)
	}
	b64 := func(s string) (*big.Int, error) {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			return nil, err
		}
		return new(big.Int).SetBytes(b), nil
	}
	switch jwk.Kty {
	case "RSA":
		n, err := b64(jwk.N)
		if err != nil {
			return "", nil, fmt.Errorf("key %q: n: %w", jwk.Kid, err)
		}
		e, err := b64(jwk.E)
		if err != nil || !e.IsInt64() || e.Int64() <= 1 {
			return "", nil, fmt.Errorf("key %q: bad e", jwk.Kid)
		}
		return jwk.Kid, &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		if jwk.Crv != "P-256" {
			return "", nil, fmt.Errorf("key %q: curve %s is not P-256", jwk.Kid, jwk.Crv)
		}
		x, err := b64(jwk.X)
		if err != nil {
			return "", nil, fmt.Errorf("key %q: x: %w", jwk.Kid, err)
		}
		y, err := b64(jwk.Y)
		if err != nil {
			return "", nil, fmt.Errorf("key %q: y: %w", jwk.Kid, err)
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		if _, err := pub.ECDH(); err != nil {
			return "", nil, fmt.Errorf("key %q is not a P-256 point: %w", jwk.Kid, err)
		}
		return jwk.Kid, pub, nil
	}
	return "", nil, fmt.Errorf("key %q: kty %q is not RSA or EC", jwk.Kid, jwk.Kty)
}
