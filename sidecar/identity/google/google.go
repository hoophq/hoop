// Package google resolves a Google OAuth2 access token to the identity that
// holds it, by asking Google.
//
// # Why tokeninfo
//
// The credential a kubectl client sends to GKE Connect Gateway is minted by
// gke-gcloud-auth-plugin: a Google OAuth2 access token ("ya29...."). It is
// opaque, not a JWT. There is no signature to check, no claim to read, no key
// set to fetch, so nothing on this side of the wire can tell who a token
// belongs to without asking the issuer. Google's tokeninfo endpoint is the one
// that answers for access tokens, so this package asks it and nothing else.
//
// # Trust model
//
// The result names who HOLDS the token, and nothing more. It is what the audit
// trail records and what a policy keys on. It is not an authorization: Connect
// Gateway still checks the same token against IAM and RBAC on every request,
// so a resolver that named the wrong principal would mislabel the trail but
// could not grant access the token does not already carry. That is also why
// the audience and scope are not checked here: rejecting a token Connect
// Gateway would accept is an outage for a caller who did nothing wrong.
//
// # A network call on the request path
//
// Every request through an http lane carries its own Authorization header, and
// a kubectl session sends many of them a second. A tokeninfo round trip each
// would add Google's latency to every one and hand Google's rate limit to the
// busiest client. So a verified identity is cached for as long as the token
// lives (capped by MaxCacheTTL, so a revoked token stops resolving within one
// window), a rejected token is remembered briefly so a client retrying a dead
// credential does not hammer Google, and concurrent lookups of one token share
// one call. An unreachable Google is never cached: it says nothing about the
// token, and remembering it would turn a blip into a thirty second outage.
package google

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/session"
)

// DefaultTokenInfoURL is Google's OAuth2 v3 tokeninfo endpoint.
const DefaultTokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"

const (
	defaultCacheSize   = 4096
	defaultMaxCacheTTL = 5 * time.Minute
	defaultTimeout     = 10 * time.Second

	// negativeTTL is how long a token Google rejected stays rejected without
	// asking again. Short, because the only cost of a miss is one round trip,
	// and a token cannot become valid again once Google calls it invalid.
	negativeTTL = 30 * time.Second

	// lookupTimeout bounds one tokeninfo call independently of any caller.
	// The call is shared by every request waiting on the same token, so no
	// single caller's context may cancel it (see Resolve); this is what stops
	// it from running forever when the injected Client has no Timeout.
	lookupTimeout = defaultTimeout

	// maxResponseBytes caps what is read from tokeninfo. The real answer is a
	// few hundred bytes; anything larger is not Google.
	maxResponseBytes = 64 << 10
)

// Sentinels, wrapped by every error Resolve returns, so a caller can tell a
// client problem (refuse the request) from a Google problem (the token may be
// fine; the check could not run).
var (
	// ErrNoToken: the request carried no credential at all.
	ErrNoToken = errors.New("google: no bearer token")
	// ErrInvalidToken: the credential is malformed, or Google says the token
	// is not valid, or it has expired, or it names nobody.
	ErrInvalidToken = errors.New("google: invalid token")
	// ErrUnavailable: tokeninfo could not be asked, or its answer could not be
	// read. Says nothing about the token.
	ErrUnavailable = errors.New("google: tokeninfo unavailable")
)

// Options configures a Resolver. The zero value is usable.
type Options struct {
	// TokenInfoURL is the endpoint tokens are verified against. Must be an
	// absolute https URL, because the request body is the bearer token.
	// Empty means DefaultTokenInfoURL.
	TokenInfoURL string
	// Client makes the tokeninfo call. nil means an http.Client with a 10s
	// timeout. Its redirect policy is overridden: see New.
	Client *http.Client
	// CacheSize bounds the number of cached tokens. 0 means 4096.
	CacheSize int
	// MaxCacheTTL caps how long a verified identity is served from cache,
	// whatever the token's own expiry. 0 means 5 minutes.
	MaxCacheTTL time.Duration
	// Now is the clock, injected for tests. nil means time.Now.
	Now func() time.Time
}

// Resolver turns one request's Authorization header into a session.Identity.
// Safe for concurrent use; one Resolver is meant to serve every connection of
// a lane so they share its cache.
type Resolver struct {
	url     string
	client  *http.Client
	size    int
	maxTTL  time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[[sha256.Size]byte]entry
	flights map[[sha256.Size]byte]*flight
}

// entry is one cached answer. Keyed by the token's SHA-256 so the cache never
// holds a credential: a heap dump of the relay yields digests, not tokens.
type entry struct {
	id      session.Identity
	err     error // non-nil for a cached rejection (always ErrInvalidToken)
	expires time.Time
}

// flight is one tokeninfo call that callers of the same token wait on.
type flight struct {
	done chan struct{}
	id   session.Identity
	err  error
}

// New validates o and returns a Resolver.
func New(o Options) (*Resolver, error) {
	raw := o.TokenInfoURL
	if raw == "" {
		raw = DefaultTokenInfoURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("google: token_info_url %q: %w", raw, err)
	}
	// https only, loopback included: the body of this request IS a bearer
	// token, and "it is only localhost" is how a plaintext hop gets into a
	// production config. Tests use httptest.NewTLSServer.
	if u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("google: token_info_url %q must be an absolute https URL", raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("google: token_info_url must not carry credentials")
	}
	if o.CacheSize < 0 {
		return nil, fmt.Errorf("google: cache size %d is negative", o.CacheSize)
	}
	if o.MaxCacheTTL < 0 {
		return nil, fmt.Errorf("google: max cache ttl %s is negative", o.MaxCacheTTL)
	}

	client := &http.Client{Timeout: defaultTimeout}
	if o.Client != nil {
		c := *o.Client // shallow: shares the Transport and its pool
		client = &c
	}
	// Never follow a redirect. A 307/308 replays the POST body, which is the
	// token, to whatever Location says; a tokeninfo that redirects is
	// misconfigured or intercepted, and either way it is not an answer.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	r := &Resolver{
		url:     u.String(),
		client:  client,
		size:    o.CacheSize,
		maxTTL:  o.MaxCacheTTL,
		now:     o.Now,
		entries: make(map[[sha256.Size]byte]entry),
		flights: make(map[[sha256.Size]byte]*flight),
	}
	if r.size == 0 {
		r.size = defaultCacheSize
	}
	if r.maxTTL == 0 {
		r.maxTTL = defaultMaxCacheTTL
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

// Resolve returns the identity holding the bearer token in credential, the
// raw Authorization header value of ONE request. Errors wrap ErrNoToken,
// ErrInvalidToken or ErrUnavailable, and never contain the token.
func (r *Resolver) Resolve(ctx context.Context, credential string) (session.Identity, error) {
	token, err := bearerToken(credential)
	if err != nil {
		return session.Identity{}, err
	}
	key := sha256.Sum256([]byte(token))

	r.mu.Lock()
	if e, ok := r.entries[key]; ok {
		if r.now().Before(e.expires) {
			r.mu.Unlock()
			return cloneIdentity(e.id), e.err
		}
		delete(r.entries, key)
	}
	f, joined := r.flights[key]
	if !joined {
		f = &flight{done: make(chan struct{})}
		r.flights[key] = f
		// Detached from ctx: the call is shared, so the caller that happened
		// to start it must not cancel it for the others by hanging up.
		// lookupTimeout bounds it instead.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
		go func() {
			defer cancel()
			r.finish(lctx, key, f, token)
		}()
	}
	r.mu.Unlock()

	select {
	case <-f.done:
		return cloneIdentity(f.id), f.err
	case <-ctx.Done():
		return session.Identity{}, fmt.Errorf("%w: %v", ErrUnavailable, ctx.Err())
	}
}

// finish runs the call for f, stores the answer and releases the waiters.
func (r *Resolver) finish(ctx context.Context, key [sha256.Size]byte, f *flight, token string) {
	id, expires, err := r.lookup(ctx, token)
	f.id, f.err = id, err

	r.mu.Lock()
	delete(r.flights, key)
	now := r.now()
	switch {
	case err == nil:
		r.store(key, entry{id: id, expires: minTime(expires, now.Add(r.maxTTL))})
	case errors.Is(err, ErrInvalidToken):
		r.store(key, entry{err: err, expires: now.Add(min(negativeTTL, r.maxTTL))})
	}
	// ErrUnavailable is deliberately not stored: see the package doc.
	r.mu.Unlock()
	close(f.done)
}

// store inserts e, evicting when full: expired entries first, then an
// arbitrary one. Arbitrary rather than LRU because every entry is bounded by
// MaxCacheTTL anyway, and the cost of a wrong eviction is one round trip.
// Called with r.mu held.
func (r *Resolver) store(key [sha256.Size]byte, e entry) {
	if _, ok := r.entries[key]; !ok && len(r.entries) >= r.size {
		now := r.now()
		for k, old := range r.entries {
			if !now.Before(old.expires) {
				delete(r.entries, k)
			}
		}
		for k := range r.entries {
			if len(r.entries) < r.size {
				break
			}
			delete(r.entries, k)
		}
	}
	r.entries[key] = e
}

// tokenInfo is the subset of the tokeninfo answer the identity is built from.
type tokenInfo struct {
	Sub           string     `json:"sub"`
	Email         string     `json:"email"`
	EmailVerified flexBool   `json:"email_verified"`
	Azp           string     `json:"azp"`
	Exp           flexNumber `json:"exp"`
	ExpiresIn     flexNumber `json:"expires_in"`
}

// tokenInfoError is Google's rejection body, observed as
// {"error": "invalid_token", "error_description": "Invalid Value"} with 400.
type tokenInfoError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// lookup asks tokeninfo about token. The expiry returned is the token's own,
// not yet capped by MaxCacheTTL.
func (r *Resolver) lookup(ctx context.Context, token string) (session.Identity, time.Time, error) {
	// POST with a form body, never ?access_token=: the URL of a request is
	// what every proxy on the path logs, the customer's intercepting Envoy
	// included, and this one would log a live credential.
	form := url.Values{"access_token": {token}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, strings.NewReader(form))
	if err != nil {
		return session.Identity{}, time.Time{}, fail(ErrUnavailable, token, "build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return session.Identity{}, time.Time{}, fail(ErrUnavailable, token, "%v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return session.Identity{}, time.Time{}, fail(ErrUnavailable, token, "read response: %v", err)
	}
	if len(body) > maxResponseBytes {
		return session.Identity{}, time.Time{}, fail(ErrUnavailable, token, "response exceeds %d bytes", maxResponseBytes)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		// Google's verdict on the token. The body only decorates the
		// message; a 400 is a rejection whatever it says.
		var e tokenInfoError
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			return session.Identity{}, time.Time{}, fail(ErrInvalidToken, token, "tokeninfo answered %d", resp.StatusCode)
		}
		return session.Identity{}, time.Time{}, fail(ErrInvalidToken, token, "tokeninfo answered %d: %s (%s)", resp.StatusCode, e.Error, e.Description)
	default:
		// 429, 5xx, and anything else (a 3xx we refused to follow, a 404
		// from a wrong URL): the check did not run, so the token is
		// neither good nor bad.
		return session.Identity{}, time.Time{}, fail(ErrUnavailable, token, "tokeninfo answered %d", resp.StatusCode)
	}

	var ti tokenInfo
	if err := json.Unmarshal(body, &ti); err != nil {
		return session.Identity{}, time.Time{}, fail(ErrUnavailable, token, "decode tokeninfo: %v", err)
	}

	now := r.now()
	var expires time.Time
	switch {
	case ti.Exp.set:
		expires = time.Unix(ti.Exp.v, 0)
	case ti.ExpiresIn.set:
		expires = now.Add(time.Duration(ti.ExpiresIn.v) * time.Second)
	default:
		// Google always sends both. A 200 carrying neither is not an answer
		// this package understands, and caching it for MaxCacheTTL on the
		// guess that the token is still alive would be a silent default.
		return session.Identity{}, time.Time{}, fail(ErrUnavailable, token, "tokeninfo answer has no exp")
	}
	if !now.Before(expires) {
		return session.Identity{}, time.Time{}, fail(ErrInvalidToken, token, "token expired at %s", expires.UTC().Format(time.RFC3339))
	}

	// The method rides on the cached identity too: cloneIdentity copies the
	// struct, so an answer served from the cache names its source as well.
	id := session.Identity{Method: session.MethodGoogleIdentity, Attributes: map[string]string{}}
	if ti.EmailVerified.v && ti.Email != "" {
		// An unverified email is a string the account holder typed; it
		// must not become the name an audit trail blames.
		id.Subject, id.Email = ti.Email, ti.Email
	} else {
		id.Subject = ti.Sub
	}
	if id.Subject == "" {
		return session.Identity{}, time.Time{}, fail(ErrInvalidToken, token, "tokeninfo names no verified email and no sub")
	}
	if ti.Sub != "" {
		id.Attributes["google.sub"] = ti.Sub
	}
	if ti.Azp != "" {
		id.Attributes["google.azp"] = ti.Azp
	}
	return id, expires, nil
}

// bearerToken extracts the one token from an Authorization header value.
//
// Strict on purpose. Two Authorization headers joined into "Bearer a, Bearer
// b" by an intermediary must fail rather than resolve to either half, because
// which half Connect Gateway honours is not this package's to guess.
func bearerToken(credential string) (string, error) {
	v := strings.TrimSpace(credential)
	if v == "" {
		return "", ErrNoToken
	}
	scheme, rest, ok := strings.Cut(v, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", fmt.Errorf("%w: authorization is not a Bearer credential", ErrInvalidToken)
	}
	token := strings.TrimLeft(rest, " ")
	if !isB64Token(token) {
		return "", fmt.Errorf("%w: authorization must carry exactly one bearer token", ErrInvalidToken)
	}
	return token, nil
}

// isB64Token reports whether s matches RFC 6750's b64token:
// 1*( ALPHA / DIGIT / "-" / "." / "_" / "~" / "+" / "/" ) *"=".
// It is what rejects a comma, a space, or a second token.
func isB64Token(s string) bool {
	body := strings.TrimRight(s, "=")
	if body == "" {
		return false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '-', c == '.', c == '_', c == '~', c == '+', c == '/':
		default:
			return false
		}
	}
	return true
}

// fail wraps sentinel with a message scrubbed of token. The scrub is the
// guarantee, not the care taken at each call site: a transport error, or a
// tokeninfo that echoes its input, would otherwise put a live credential in a
// log line.
func fail(sentinel error, token, format string, args ...any) error {
	// Both spellings: the raw token, and the form-encoded one that went on
	// the wire, in case something echoes the request body.
	msg := strings.NewReplacer(token, "[redacted]", url.QueryEscape(token), "[redacted]").
		Replace(fmt.Sprintf(format, args...))
	return fmt.Errorf("%w: %s", sentinel, msg)
}

// cloneIdentity copies the Attributes map so a caller mutating its identity
// cannot race or rewrite the cached one every other connection is served.
func cloneIdentity(id session.Identity) session.Identity {
	id.Attributes = maps.Clone(id.Attributes)
	return id
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// flexBool decodes email_verified, which tokeninfo sends as the string
// "true"/"false" while other Google endpoints send a JSON bool.
type flexBool struct{ v bool }

func (b *flexBool) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("email_verified %q is not a boolean", s)
		}
		b.v = v
		return nil
	}
	return json.Unmarshal(data, &b.v)
}

// flexNumber decodes exp and expires_in, which tokeninfo sends as decimal
// strings; a JSON number is accepted too. set distinguishes absent from 0.
type flexNumber struct {
	v   int64
	set bool
}

func (n *flexNumber) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("%q is not an integer", s)
		}
		n.v, n.set = v, true
		return nil
	}
	if err := json.Unmarshal(data, &n.v); err != nil {
		return err
	}
	n.set = true
	return nil
}
