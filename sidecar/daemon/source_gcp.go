package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/session"
)

// The gcp_impersonation source presents, as the upstream password, an access
// token of the pool entry's service account, minted through
// iamcredentials.googleapis.com with the pod's own Workload Identity from
// the metadata server. The database or API then sees that account, and the
// pod needs roles/iam.serviceAccountTokenCreator on it and nothing else.
const (
	// iamCredentialsEndpoint is Google's token minting API. A var so a
	// test points it at a fake; production reaches the real host through
	// the trust section when a proxy re-signs it.
	iamCredentialsHost = "https://iamcredentials.googleapis.com"

	// gcpAccessTokenRefreshBefore is how long before expireTime a cached
	// access token is replaced, wide enough that a login never presents one
	// the upstream sees expired.
	gcpAccessTokenRefreshBefore = 5 * time.Minute

	// gcpImpersonationLifetime is what the mint asks for; the maximum IAM
	// grants without an org policy change.
	gcpImpersonationLifetime = "3600s"

	gcpDefaultScope = "https://www.googleapis.com/auth/cloud-platform"
	maxIAMAnswer    = 64 << 10
)

// iamCredentialsEndpoint is where the mint is posted. A var so a test and
// the compose stack can point it at a fake.
var iamCredentialsEndpoint = iamCredentialsHost

type gcpImpersonationSource struct {
	metadataHost string
	iam          *http.Client

	mu    sync.Mutex
	cache map[string]gcpMinted // by entry name
}

type gcpMinted struct {
	token string
	exp   time.Time
}

// newGCPImpersonationSource builds the source over the process trust pool,
// so a proxy that re-signs iamcredentials.googleapis.com verifies through
// the trust section like every other outbound client here.
func newGCPImpersonationSource(cfg *GCPImpersonationSourceConfig, roots *x509.CertPool) *gcpImpersonationSource {
	host := ""
	if cfg != nil {
		host = cfg.MetadataHost
	}
	return &gcpImpersonationSource{
		metadataHost: host,
		iam: &http.Client{
			Timeout: gcpMetadataTimeout,
			Transport: &http.Transport{
				Proxy:           http.ProxyFromEnvironment,
				TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			},
		},
		cache: map[string]gcpMinted{},
	}
}

func buildGCPImpersonationSource(cfg *CredentialSourcesConfig) (CredentialSource, error) {
	if cfg == nil || cfg.GCPImpersonation == nil {
		return nil, errors.New("credentials.gcp_impersonation is not configured")
	}
	// The trust pool reaches the source through useRoots, after the lanes
	// load it: the registry builder signature carries the config alone.
	return newGCPImpersonationSource(cfg.GCPImpersonation, nil), nil
}

// useRoots installs the process trust pool. buildLanes calls it once the
// bundle is loaded.
func (g *gcpImpersonationSource) useRoots(roots *x509.CertPool) {
	g.iam.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
}

func (g *gcpImpersonationSource) validateEntry(e CredentialEntry) error {
	if e.ServiceAccount == "" {
		return errors.New("service_account is required")
	}
	if !strings.Contains(e.ServiceAccount, "@") {
		return fmt.Errorf("service_account %q is not an email", e.ServiceAccount)
	}
	return nil
}

func (g *gcpImpersonationSource) Credential(ctx context.Context, e CredentialEntry, _ session.Identity) (Credential, error) {
	if err := g.validateEntry(e); err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if m, ok := g.cache[e.Name]; ok && time.Now().Before(m.exp.Add(-gcpAccessTokenRefreshBefore)) {
		return Credential{User: e.ServiceAccount, Password: m.token}, nil
	}
	own, err := g.ownAccessToken(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	tok, exp, err := g.mint(ctx, own, e)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	g.cache[e.Name] = gcpMinted{token: tok, exp: exp}
	return Credential{User: e.ServiceAccount, Password: tok}, nil
}

// ownAccessToken is the pod's Workload Identity access token from the
// metadata server, the caller of the mint. Short-lived and cheap, so it is
// fetched per mint rather than cached.
func (g *gcpImpersonationSource) ownAccessToken(ctx context.Context) (string, error) {
	host := g.metadataHost
	if host == "" {
		host = os.Getenv(gcpMetadataHostEnv)
	}
	if host == "" {
		host = gcpMetadataHost
	}
	u := url.URL{Scheme: "http", Host: host, Path: "/computeMetadata/v1/instance/service-accounts/default/token"}
	ctx, cancel := context.WithTimeout(ctx, gcpMetadataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	req.Header.Set("User-Agent", userAgent("credential/gcp_impersonation"))
	resp, err := gcpMetadataClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("the GCP metadata server at %s is unreachable: %w", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxIAMAnswer))
	if err != nil {
		return "", fmt.Errorf("reading the GCP metadata server's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the GCP metadata server at %s answered %s for an access token: %s", host, resp.Status, controlPlaneMessage(raw))
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.AccessToken == "" {
		return "", fmt.Errorf("the GCP metadata server at %s answered with no access token", host)
	}
	return body.AccessToken, nil
}

// mint calls generateAccessToken for the entry's service account.
func (g *gcpImpersonationSource) mint(ctx context.Context, own string, e CredentialEntry) (string, time.Time, error) {
	scopes := e.Scopes
	if len(scopes) == 0 {
		scopes = []string{gcpDefaultScope}
	}
	body, err := json.Marshal(map[string]any{"scope": scopes, "lifetime": gcpImpersonationLifetime})
	if err != nil {
		return "", time.Time{}, err
	}
	endpoint := iamCredentialsEndpoint + "/v1/projects/-/serviceAccounts/" + url.PathEscape(e.ServiceAccount) + ":generateAccessToken"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+own)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent("credential/gcp_impersonation"))
	resp, err := g.iam.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generateAccessToken for %s: %w", e.ServiceAccount, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxIAMAnswer))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generateAccessToken for %s: reading the answer: %w", e.ServiceAccount, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("generateAccessToken for %s answered %s: %s; the pod's identity needs "+
			"roles/iam.serviceAccountTokenCreator on that account", e.ServiceAccount, resp.Status, controlPlaneMessage(raw))
	}
	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireTime  string `json:"expireTime"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("generateAccessToken for %s answered with no accessToken", e.ServiceAccount)
	}
	exp, err := time.Parse(time.RFC3339, out.ExpireTime)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generateAccessToken for %s answered expireTime %q: %w", e.ServiceAccount, out.ExpireTime, err)
	}
	return out.AccessToken, exp, nil
}
