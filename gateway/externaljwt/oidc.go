package externaljwt

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	keyfunc "github.com/MicahParks/keyfunc/v2"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/log"
)

// MaxOIDCTokenBytes bounds a token before anything decodes it. It is the
// limit the sidecar applies to the token file it reads.
const MaxOIDCTokenBytes = 16 << 10

const (
	// oidcKeyTTL is how long fetched keys are used before the next request
	// fetches them again. A kid the keys do not hold refetches sooner.
	oidcKeyTTL = time.Hour
	// oidcMaxStaleAge is how long after their last successful fetch keys
	// still verify while every refresh fails. An issuer that rotated out a
	// compromised key must not have it accepted for as long as its
	// discovery stays down.
	oidcMaxStaleAge = 24 * time.Hour
	// oidcRefetchInterval is the shortest time between two fetches for one
	// issuer. A caller can send any kid; without the limit each unknown one
	// would be a fetch.
	oidcRefetchInterval = time.Minute
	oidcFetchTimeout    = 10 * time.Second
	oidcMaxRedirects    = 3
	oidcMaxDocument     = 1 << 20
	// oidcClockSkew tolerates a kubelet or metadata server clock that runs
	// ahead of or behind the plane. exp is still required.
	oidcClockSkew = 30 * time.Second
	// maxStaticKeySets bounds the parsed copies of static key sets. They
	// come from admin-written rows; the bound only stops a stream of edits
	// from growing the map.
	maxStaticKeySets = 256
)

// oidcAllowedAlgs are the only signatures accepted: asymmetric ones, so a
// key set the plane fetched can never act as a shared secret, and never
// "none".
var oidcAllowedAlgs = []string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}

// OIDCClaims is what a verified token says about its holder.
type OIDCClaims struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
	ExpiresAt     time.Time
}

// OIDCOptions configures an OIDCVerifier. Issuers are not configured here:
// they come from the database, per mapping, on every call.
type OIDCOptions struct {
	// HTTPClient fetches discovery documents and key sets. Nil uses
	// newOIDCHTTPClient, which refuses non-public addresses. Discovery is
	// https only in every case, so a test passes the client of an httptest
	// TLS server rather than turning the check off.
	HTTPClient *http.Client
}

// OIDCVerifier verifies JWTs from any issuer a caller names.
// Keys come from a static JWKS the caller passes, or from OIDC discovery at
// {issuer}/.well-known/openid-configuration, cached per issuer. It is safe for
// concurrent use and starts no goroutine.
//
// It fetches only for the issuer it is asked about, so a caller must decide
// that an issuer is known before calling Verify: the issuer of an unverified
// token is chosen by whoever sent it.
type OIDCVerifier struct {
	client          *http.Client
	keyTTL          time.Duration
	maxStaleAge     time.Duration
	refetchInterval time.Duration
	now             func() time.Time

	mu      sync.Mutex
	issuers map[string]*issuerKeys
	static  map[[sha256.Size]byte]*keyfunc.JWKS
}

func NewOIDCVerifier(opts OIDCOptions) *OIDCVerifier {
	client := opts.HTTPClient
	if client == nil {
		client = newOIDCHTTPClient()
	}
	return &OIDCVerifier{
		client:          client,
		keyTTL:          oidcKeyTTL,
		maxStaleAge:     oidcMaxStaleAge,
		refetchInterval: oidcRefetchInterval,
		now:             func() time.Time { return time.Now().UTC() },
		issuers:         map[string]*issuerKeys{},
		static:          map[[sha256.Size]byte]*keyfunc.JWKS{},
	}
}

// newOIDCHTTPClient fetches from public addresses only. The issuer URL comes
// from an admin-written mapping, so without the check the plane would fetch
// from its own network on request: loopback, a cluster service, the cloud
// metadata endpoint. The check runs on the address each connection dials,
// after DNS resolution and on every redirect, so a public name that
// resolves to a private address is refused too.
//
// No proxy: through one, the dialed address is the proxy's and the check
// would see nothing of the issuer. An issuer on a private network, or one
// only a proxy reaches, is served by the mapping's static jwks.
func newOIDCHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   oidcFetchTimeout,
		KeepAlive: 30 * time.Second,
		Control:   refuseNonPublicAddress,
	}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Timeout:       oidcFetchTimeout,
		Transport:     transport,
		CheckRedirect: checkOIDCRedirect,
	}
}

// checkOIDCRedirect follows at most oidcMaxRedirects redirects, to https
// only.
func checkOIDCRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refused a redirect to %s: only https is fetched", req.URL.Scheme)
	}
	if len(via) > oidcMaxRedirects {
		return fmt.Errorf("stopped after %d redirects", oidcMaxRedirects)
	}
	return nil
}

// cgnatPrefix is the shared address space of RFC 6598, which carriers and
// some clusters use internally.
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// refuseNonPublicAddress is a net.Dialer Control hook. address is the
// resolved ip:port the connection is about to dial.
func refuseNonPublicAddress(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("oidc: refused to dial %q: not an ip:port: %w", address, err)
	}
	if !isPublicAddr(ap.Addr()) {
		return fmt.Errorf("oidc: refused to dial %s: not a public address; "+
			"for an issuer on a private network set jwks on the sidecar service account", ap.Addr())
	}
	return nil
}

// isPublicAddr refuses loopback, private (RFC 1918 and IPv6 ULA),
// link-local (169.254.169.254 among them), unspecified, multicast and CGNAT
// addresses, IPv4 ones in their IPv6-mapped form too.
func isPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() &&
		!addr.IsLoopback() &&
		!addr.IsPrivate() &&
		!addr.IsLinkLocalUnicast() &&
		!addr.IsMulticast() &&
		!addr.IsUnspecified() &&
		!cgnatPrefix.Contains(addr)
}

// UnverifiedIssuer reads iss from a token without checking anything else.
// Its only use is to pick which mappings to load before a key is fetched; the
// value is whatever the sender wrote.
func UnverifiedIssuer(raw string) (string, error) {
	if len(raw) > MaxOIDCTokenBytes {
		return "", fmt.Errorf("token is larger than %d bytes", MaxOIDCTokenBytes)
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return "", err
	}
	iss, err := claims.GetIssuer()
	if err != nil {
		return "", err
	}
	if iss == "" {
		return "", errors.New("token has no iss claim")
	}
	return iss, nil
}

// ValidateJWKS reports whether raw is a key set Verify can use: a JWKS
// document with at least one RSA or EC public key.
func ValidateJWKS(raw []byte) error {
	_, err := parseJWKS(raw)
	return err
}

// Verify checks raw's signature (oidcAllowedAlgs only), that iss is issuer,
// that exp is present and not past, and that aud contains audience. Keys come
// from staticJWKS when it is set, else from the issuer's OIDC discovery.
// Errors wrap ErrValidationFailed when the token itself is refused.
func (v *OIDCVerifier) Verify(ctx context.Context, raw, issuer, audience string, staticJWKS json.RawMessage) (*OIDCClaims, error) {
	if len(raw) > MaxOIDCTokenBytes {
		return nil, fmt.Errorf("%w: token is larger than %d bytes", ErrValidationFailed, MaxOIDCTokenBytes)
	}
	// WithAudience("") would accept a token whose aud lists "", so an empty
	// audience is a caller bug, never "any audience".
	if issuer == "" || audience == "" {
		return nil, errors.New("oidc: issuer and audience are required")
	}

	var keyFunc jwt.Keyfunc
	if len(staticJWKS) > 0 {
		jwks, err := v.staticKeySet(staticJWKS)
		if err != nil {
			return nil, err
		}
		keyFunc = jwks.Keyfunc
	} else {
		// Resolved inside the parse, after the alg check, so a token the
		// parser refuses on its header never causes a fetch.
		keyFunc = func(tok *jwt.Token) (any, error) { return v.discoveredKey(ctx, issuer, tok) }
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods(oidcAllowedAlgs),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(oidcClockSkew),
		jwt.WithTimeFunc(v.now),
	)
	claims := jwt.MapClaims{}
	if _, err := parser.ParseWithClaims(raw, claims, keyFunc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrValidationFailed, err)
	}

	out := &OIDCClaims{Issuer: issuer}
	out.Subject, _ = claims["sub"].(string)
	out.Email, _ = claims["email"].(string)
	// Only a JSON true counts. A string "true" is not what Google sends, and
	// accepting one would widen what verifies an email.
	out.EmailVerified, _ = claims["email_verified"].(bool)
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil {
		out.ExpiresAt = exp.Time
	}
	return out, nil
}

func (v *OIDCVerifier) staticKeySet(raw json.RawMessage) (*keyfunc.JWKS, error) {
	sum := sha256.Sum256(raw)
	v.mu.Lock()
	defer v.mu.Unlock()
	if jwks, ok := v.static[sum]; ok {
		return jwks, nil
	}
	jwks, err := parseJWKS(raw)
	if err != nil {
		return nil, fmt.Errorf("oidc: static jwks: %w", err)
	}
	if len(v.static) >= maxStaticKeySets {
		clear(v.static)
	}
	v.static[sum] = jwks
	return jwks, nil
}

func (v *OIDCVerifier) issuerEntry(issuer string) *issuerKeys {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.issuers[issuer]
	if !ok {
		e = &issuerKeys{issuer: issuer}
		v.issuers[issuer] = e
	}
	return e
}

func (v *OIDCVerifier) discoveredKey(ctx context.Context, issuer string, tok *jwt.Token) (any, error) {
	e := v.issuerEntry(issuer)
	jwks, err := v.keys(ctx, e, false)
	if err != nil {
		return nil, err
	}
	key, err := jwks.Keyfunc(tok)
	if !errors.Is(err, keyfunc.ErrKIDNotFound) {
		return key, err
	}
	// The issuer may have rotated its key since the last fetch.
	jwks, err = v.keys(ctx, e, true)
	if err != nil {
		return nil, err
	}
	return jwks.Keyfunc(tok)
}

// issuerKeys is the cached key set of one discovered issuer.
type issuerKeys struct {
	issuer string
	// fetching is held for the length of one fetch, so concurrent requests
	// for an issuer cause one fetch.
	fetching sync.Mutex

	mu        sync.RWMutex
	jwks      *keyfunc.JWKS
	fetchedAt time.Time // last successful fetch
	triedAt   time.Time // last fetch, successful or not
	err       error     // last failure; nil after a success
}

// keys returns e's key set, fetching it when there is none, when it is older
// than the TTL, or when unknownKID asks for a refetch. Fetches for one issuer
// are at least refetchInterval apart whatever the callers send. Keys fetched
// more than maxStaleAge ago are never returned: past it, only a successful
// fetch verifies a token again.
func (v *OIDCVerifier) keys(ctx context.Context, e *issuerKeys, unknownKID bool) (*keyfunc.JWKS, error) {
	e.mu.RLock()
	jwks, fetchedAt, triedAt, lastErr := e.jwks, e.fetchedAt, e.triedAt, e.err
	e.mu.RUnlock()

	now := v.now()
	if jwks != nil && !unknownKID && now.Sub(fetchedAt) < v.keyTTL {
		return jwks, nil
	}
	if !triedAt.IsZero() && now.Sub(triedAt) < v.refetchInterval {
		return v.currentKeys(jwks, fetchedAt, lastErr, now)
	}
	if jwks != nil && !unknownKID && now.Sub(fetchedAt) < v.maxStaleAge {
		// Keys past their TTL still verify: one request refreshes them, and
		// the others do not wait for it.
		if !e.fetching.TryLock() {
			return jwks, nil
		}
	} else {
		e.fetching.Lock()
	}
	defer e.fetching.Unlock()

	e.mu.RLock()
	if !e.triedAt.Equal(triedAt) {
		// Another request fetched while this one waited.
		jwks, fetchedAt, lastErr = e.jwks, e.fetchedAt, e.err
		e.mu.RUnlock()
		return v.currentKeys(jwks, fetchedAt, lastErr, v.now())
	}
	e.mu.RUnlock()

	fresh, err := v.fetch(ctx, e.issuer)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.triedAt = v.now()
	if err != nil {
		e.err = err
		kept, keptErr := v.currentKeys(e.jwks, e.fetchedAt, err, e.triedAt)
		if kept != nil {
			log.With("issuer", e.issuer).Warnf("oidc: key refresh failed, keeping the keys fetched at %s: %v",
				e.fetchedAt.UTC().Format(time.RFC3339), err)
		}
		return kept, keptErr
	}
	e.jwks, e.fetchedAt, e.err = fresh, e.triedAt, nil
	return fresh, nil
}

// currentKeys returns the cached keys while they are younger than
// maxStaleAge, and else the reason no key verifies.
func (v *OIDCVerifier) currentKeys(jwks *keyfunc.JWKS, fetchedAt time.Time, lastErr error, now time.Time) (*keyfunc.JWKS, error) {
	if jwks != nil && now.Sub(fetchedAt) < v.maxStaleAge {
		return jwks, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no key set fetched yet")
	}
	if jwks != nil {
		return nil, fmt.Errorf("oidc: the keys for this issuer were fetched at %s, more than %s ago, and every refresh since failed: %w",
			fetchedAt.UTC().Format(time.RFC3339), v.maxStaleAge, lastErr)
	}
	return nil, fmt.Errorf("oidc: keys for this issuer are unavailable, next fetch within %s: %w", v.refetchInterval, lastErr)
}

// fetch runs discovery and loads the key set it names.
func (v *OIDCVerifier) fetch(ctx context.Context, issuer string) (*keyfunc.JWKS, error) {
	// Detached from the request: a sidecar that hangs up must not record a
	// failed fetch that every other request then waits out.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oidcFetchTimeout)
	defer cancel()

	body, err := v.get(ctx, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration")
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("oidc discovery: decode the document: %w", err)
	}
	// OpenID Connect Discovery 1.0, section 4.3: the document's issuer must
	// be identical to the one it was fetched for.
	if doc.Issuer != issuer {
		return nil, fmt.Errorf("oidc discovery: the document names issuer %q, want %q", doc.Issuer, issuer)
	}
	if doc.JWKSURI == "" {
		return nil, errors.New("oidc discovery: the document has no jwks_uri")
	}
	body, err = v.get(ctx, doc.JWKSURI)
	if err != nil {
		return nil, fmt.Errorf("oidc jwks: %w", err)
	}
	jwks, err := parseJWKS(body)
	if err != nil {
		return nil, fmt.Errorf("oidc jwks: %w", err)
	}
	return jwks, nil
}

func (v *OIDCVerifier) get(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("refused %q: only https URLs are fetched", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s answered HTTP %d", u.Redacted(), resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, oidcMaxDocument+1))
	if err != nil {
		return nil, err
	}
	if len(body) > oidcMaxDocument {
		return nil, fmt.Errorf("GET %s answered more than %d bytes", u.Redacted(), oidcMaxDocument)
	}
	return body, nil
}

// parseJWKS decodes a key set. keyfunc skips the keys it cannot parse, and an
// HMAC key could never verify a token here, so the set must hold at least one
// RSA or EC public key.
func parseJWKS(raw []byte) (*keyfunc.JWKS, error) {
	jwks, err := keyfunc.NewJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("decode the key set: %w", err)
	}
	for _, key := range jwks.ReadOnlyKeys() {
		switch key.(type) {
		case *rsa.PublicKey, *ecdsa.PublicKey:
			return jwks, nil
		}
	}
	return nil, errors.New("the key set holds no RSA or EC public key")
}
