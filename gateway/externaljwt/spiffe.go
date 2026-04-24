package externaljwt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	keyfunc "github.com/MicahParks/keyfunc/v2"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/log"
)

// SPIFFEConfig bundles the runtime inputs for a SPIFFE JWT-SVID provider.
//
// Exactly one of BundleURL or BundleFile must be non-empty. The content at
// either source is expected to be a JWKS JSON document (RFC 7517); the SPIFFE
// JWT bundle format is a JWKS at its core. Support for the full
// SPIFFE Trust Bundle format (with trust-domain wrapper) and the Workload
// API UDS source can be added later without changing this package's
// exported surface.
type SPIFFEConfig struct {
	TrustDomain   string
	Audience      string
	BundleURL     string
	BundleFile    string
	RefreshPeriod time.Duration

	// HTTPClient is used for bundle URL fetches. Defaults to a client with
	// a 10s timeout. Tests may inject their own client.
	HTTPClient *http.Client
}

type spiffeProvider struct {
	cfg SPIFFEConfig

	mu      sync.RWMutex
	jwks    *keyfunc.JWKS
	lastErr error

	closeOnce sync.Once
	closed    chan struct{}
}

// NewSPIFFEProvider constructs a SPIFFE JWT-SVID provider and performs an
// initial bundle fetch. If the initial fetch fails the provider is returned
// anyway with an error so the caller can decide whether to surface startup
// failure or degrade gracefully. The background refresh loop is not started
// by this function; callers should invoke Start after construction.
func NewSPIFFEProvider(ctx context.Context, cfg SPIFFEConfig) (*spiffeProvider, error) {
	if cfg.TrustDomain == "" {
		return nil, fmt.Errorf("spiffe: trust_domain is required")
	}
	if cfg.Audience == "" {
		return nil, fmt.Errorf("spiffe: audience is required")
	}
	if (cfg.BundleURL == "") == (cfg.BundleFile == "") {
		return nil, fmt.Errorf("spiffe: exactly one of bundle_url or bundle_file must be set")
	}
	if cfg.BundleURL != "" {
		if _, err := url.Parse(cfg.BundleURL); err != nil {
			return nil, fmt.Errorf("spiffe: invalid bundle_url: %v", err)
		}
	}
	if cfg.RefreshPeriod <= 0 {
		cfg.RefreshPeriod = 30 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		}
	}

	p := &spiffeProvider{
		cfg:    cfg,
		closed: make(chan struct{}),
	}

	if err := p.Refresh(ctx); err != nil {
		return p, fmt.Errorf("spiffe: initial bundle fetch failed: %w", err)
	}
	return p, nil
}

// Start launches the background refresh loop. It blocks until Close is
// called or the context is canceled. Callers should invoke it in a
// goroutine.
func (p *spiffeProvider) Start(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.RefreshPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.closed:
			return
		case <-ticker.C:
			if err := p.Refresh(ctx); err != nil {
				log.With("trust_domain", p.cfg.TrustDomain).
					Warnf("spiffe: bundle refresh failed: %v", err)
			}
		}
	}
}

func (p *spiffeProvider) Type() IssuerType { return IssuerSPIFFE }

func (p *spiffeProvider) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.jwks != nil {
			p.jwks.EndBackground()
			p.jwks = nil
		}
	})
	return nil
}

// Refresh fetches the current trust bundle from the configured source and
// atomically swaps the JWKS used by Validate. If refresh fails, the
// previously-loaded JWKS (if any) remains in use. The error is also stored
// on the provider so observers can see it.
func (p *spiffeProvider) Refresh(ctx context.Context) error {
	raw, err := p.fetchBundle(ctx)
	if err != nil {
		p.mu.Lock()
		p.lastErr = err
		p.mu.Unlock()
		return err
	}
	// the bundle may be either a bare JWKS ({"keys":[...]}) or a SPIFFE
	// trust bundle that wraps a JWKS under "keys" with trust-domain
	// metadata; both are accepted because keyfunc.NewJSON tolerates the
	// extra top-level fields as long as "keys" is present.
	jwks, err := keyfunc.NewJSON(raw)
	if err != nil {
		wrapped := fmt.Errorf("failed parsing bundle as JWKS: %w", err)
		p.mu.Lock()
		p.lastErr = wrapped
		p.mu.Unlock()
		return wrapped
	}
	p.mu.Lock()
	// keyfunc starts a background refresh goroutine when constructed with
	// Get(); NewJSON does not, but we still call EndBackground on the
	// previous instance for hygiene.
	if prev := p.jwks; prev != nil {
		prev.EndBackground()
	}
	p.jwks = jwks
	p.lastErr = nil
	p.mu.Unlock()
	return nil
}

func (p *spiffeProvider) fetchBundle(ctx context.Context) (json.RawMessage, error) {
	if p.cfg.BundleFile != "" {
		data, err := os.ReadFile(p.cfg.BundleFile)
		if err != nil {
			return nil, fmt.Errorf("failed reading bundle file %q: %w", p.cfg.BundleFile, err)
		}
		return data, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BundleURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed constructing bundle request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed fetching bundle from %q: %w", p.cfg.BundleURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bundle endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed reading bundle body: %w", err)
	}
	return body, nil
}

// Validate parses and verifies a JWT-SVID. The token must satisfy:
//
//   - signature verifies against a key in the currently-loaded bundle;
//   - exp claim is present and in the future;
//   - aud claim contains the configured audience;
//   - sub claim is a spiffe:// URI under the configured trust domain.
//
// Validation does not accept stale bundles: if no bundle has ever been
// successfully loaded, every validation attempt fails immediately.
func (p *spiffeProvider) Validate(ctx context.Context, tokenStr string) (*ValidatedIdentity, error) {
	p.mu.RLock()
	jwks := p.jwks
	lastErr := p.lastErr
	p.mu.RUnlock()
	if jwks == nil {
		if lastErr != nil {
			return nil, fmt.Errorf("spiffe: no trust bundle loaded (last error: %v)", lastErr)
		}
		return nil, fmt.Errorf("spiffe: no trust bundle loaded")
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}),
		jwt.WithAudience(p.cfg.Audience),
		jwt.WithExpirationRequired(),
	)
	tok, err := parser.Parse(tokenStr, jwks.Keyfunc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}
	if !tok.Valid {
		return nil, fmt.Errorf("%w: token is not valid", ErrValidationFailed)
	}

	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected claims type", ErrValidationFailed)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("%w: missing sub claim", ErrValidationFailed)
	}
	if !strings.HasPrefix(sub, "spiffe://") {
		return nil, fmt.Errorf("%w: sub is not a spiffe:// URI", ErrValidationFailed)
	}
	td, err := trustDomainFromSPIFFEID(sub)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}
	if td != p.cfg.TrustDomain {
		return nil, fmt.Errorf("%w: sub trust domain %q does not match configured %q", ErrValidationFailed, td, p.cfg.TrustDomain)
	}

	var expiresAt, issuedAt time.Time
	if e, err := claims.GetExpirationTime(); err == nil && e != nil {
		expiresAt = e.Time
	}
	if i, err := claims.GetIssuedAt(); err == nil && i != nil {
		issuedAt = i.Time
	}

	return &ValidatedIdentity{
		IssuerType:  IssuerSPIFFE,
		Subject:     sub,
		TrustDomain: td,
		Audience:    p.cfg.Audience,
		ExpiresAt:   expiresAt,
		IssuedAt:    issuedAt,
	}, nil
}

// trustDomainFromSPIFFEID extracts the trust domain segment from a spiffe://
// URI. It does not perform full URI validation but is strict enough to
// reject obviously malformed inputs.
func trustDomainFromSPIFFEID(id string) (string, error) {
	rest := strings.TrimPrefix(id, "spiffe://")
	if rest == "" || rest == id {
		return "", fmt.Errorf("not a spiffe:// URI: %q", id)
	}
	slash := strings.IndexByte(rest, '/')
	if slash == -1 {
		return rest, nil
	}
	return rest[:slash], nil
}
