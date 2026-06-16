// Package gcpoauth implements the built-in gcp_oauth Federation resolver.
//
// Unlike gcp_iam (which impersonates a per-user service account via an admin
// SA key), gcp_oauth uses NO service accounts at all. Each user consents once
// through Google's OAuth flow; the gateway stores their refresh token and, at
// session-open time, exchanges it for a short-lived USER access token. The
// identity that shows up in GCP audit logs is the user's real Google account
// (e.g. alice@acme.com), not a service account.
//
// Credential contract:
//
//   - AdminCredentialsPlain carries the OAuth *client* config (a JSON blob
//     with client_id/client_secret). This is the app registration, not a
//     service-account key — it has no IAM bindings and is not per-user.
//   - UserCredentialsPlain carries the per-user OAuth refresh token, loaded by
//     the federation service from private.federation_user_credentials keyed by
//     (connection, user). When empty, the user has not completed the consent
//     flow and Resolve returns an actionable error.
//   - ResolvedPrincipal is overridden by the federation service with the
//     consented Google email (the honest identity), so the audit metadata and
//     HOOP_FEDERATED_PRINCIPAL reflect the real human, not an identity-template
//     render.
//
// Env-var contract emitted to the agent is identical to gcp_iam so the agent's
// bq wrapper is credential-source agnostic:
//
//	HOOP_GCP_ACCESS_TOKEN         The short-lived OAuth bearer token.
//	HOOP_GCP_TOKEN_EXPIRES_AT     RFC3339 expiry of the bearer.
//	CLOUDSDK_CORE_PROJECT         The GCP project id from extra_config.project_id.
//	HOOP_FEDERATED_PRINCIPAL      The consented Google email.
package gcpoauth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hoophq/hoop/gateway/federation"
	"github.com/hoophq/hoop/gateway/models"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// cloudPlatformScope is the default OAuth scope minted tokens carry when
// extra_config does not specify an explicit scope list. It is broad enough for
// BigQuery and most GCP data-plane APIs.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// providerName matches models.FederationProviderGCPOAuth; duplicated as a
// package-local string so the registry entry is self-contained. A compile-time
// assertion below guards against drift.
const providerName = "gcp_oauth"

// oauthClientConfig is the shape of the AdminCredentialsPlain blob for this
// provider: the OAuth 2.0 client (app) credentials the admin registers in the
// GCP console. It is NOT a service-account key.
type oauthClientConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// extraConfig is the parsed shape of ConnectionFederationConfig.ExtraConfig for
// gcp_oauth. project_id is required (emitted as CLOUDSDK_CORE_PROJECT); scopes
// is optional and defaults to cloud-platform.
type extraConfig struct {
	ProjectID string   `json:"project_id"`
	Scopes    []string `json:"scopes"`
}

// tokenExchanger abstracts the refresh-token → access-token exchange so unit
// tests can inject a fake without talking to Google's token endpoint.
type tokenExchanger interface {
	ExchangeRefreshToken(ctx context.Context, refreshToken string, scopes []string) (token string, expiresAt time.Time, err error)
}

// Resolver implements federation.Resolver for the user-token (gcp_oauth) flow.
type Resolver struct {
	// newExchanger builds a per-resolve exchanger from the OAuth client
	// credentials. Pluggable so tests can substitute an in-memory fake.
	newExchanger func(clientID, clientSecret string) tokenExchanger
}

// New returns a production-ready gcp_oauth resolver.
func New() *Resolver {
	return &Resolver{newExchanger: newRealExchanger}
}

// Provider satisfies federation.Resolver.
func (r *Resolver) Provider() string { return providerName }

// Resolve exchanges the user's stored refresh token for a fresh access token
// and returns the env-var bundle the agent injects into the session command.
func (r *Resolver) Resolve(ctx context.Context, req federation.ResolveRequest) (*federation.Result, error) {
	if req.Config == nil {
		return nil, fmt.Errorf("missing federation config")
	}
	if len(req.AdminCredentialsPlain) == 0 {
		return nil, fmt.Errorf("missing oauth client credentials (expected JSON with client_id/client_secret)")
	}
	if req.ResolvedPrincipal == "" {
		return nil, fmt.Errorf("resolved principal is empty (identity mapping returned nothing)")
	}

	extra, err := parseExtraConfig(req.Config.ExtraConfig)
	if err != nil {
		return nil, fmt.Errorf("invalid extra_config: %w", err)
	}
	if extra.ProjectID == "" {
		return nil, fmt.Errorf("extra_config.project_id is required for gcp_oauth federation")
	}

	clientCfg, err := parseClientConfig(req.AdminCredentialsPlain)
	if err != nil {
		return nil, fmt.Errorf("invalid oauth client credentials: %w", err)
	}

	// An empty refresh token means the federation service found no stored
	// credential for this (connection, user): the user has not consented yet.
	// Surface an actionable message rather than a generic OAuth error.
	if len(req.UserCredentialsPlain) == 0 {
		return nil, fmt.Errorf(
			"user %q has not connected a Google account for this connection: complete the OAuth consent flow "+
				"(GET /api/connections/{connection}/federation/oauth/authorize) and try again",
			req.UserEmail,
		)
	}

	scopes := extra.Scopes
	if len(scopes) == 0 {
		scopes = []string{cloudPlatformScope}
	}

	exchanger := r.newExchanger(clientCfg.ClientID, clientCfg.ClientSecret)
	token, expiresAt, err := exchanger.ExchangeRefreshToken(ctx, string(req.UserCredentialsPlain), scopes)
	if err != nil {
		// Wrap with the principal so operators can correlate failures with a
		// specific user. A refresh failure usually means the user revoked the
		// grant or it expired (e.g. a "Testing" consent screen's 7-day cap),
		// in which case re-consenting fixes it.
		return nil, fmt.Errorf("refreshing access token for %q failed (the user may need to re-connect their Google account): %w",
			req.ResolvedPrincipal, err)
	}

	return &federation.Result{
		EnvVars: map[string]string{
			"HOOP_GCP_ACCESS_TOKEN":     token,
			"HOOP_GCP_TOKEN_EXPIRES_AT": expiresAt.UTC().Format(time.RFC3339),
			"CLOUDSDK_CORE_PROJECT":     extra.ProjectID,
			"HOOP_FEDERATED_PRINCIPAL":  req.ResolvedPrincipal,
		},
		// Same supersede contract as gcp_iam: the federated token replaces the
		// legacy GOOGLE_APPLICATION_CREDENTIALS key-file auth path end-to-end.
		SupersededEnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"},
		ResolvedPrincipal: req.ResolvedPrincipal,
		// AdminPrincipal carries the OAuth client_id for audit correlation;
		// there is no impersonator service account in this flow.
		AdminPrincipal: clientCfg.ClientID,
		TokenExpiresAt: expiresAt,
	}, nil
}

func parseExtraConfig(raw json.RawMessage) (extraConfig, error) {
	var out extraConfig
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}

func parseClientConfig(raw []byte) (oauthClientConfig, error) {
	var cfg oauthClientConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("not valid JSON: %v", err)
	}
	if cfg.ClientID == "" {
		return cfg, fmt.Errorf("client_id is missing")
	}
	if cfg.ClientSecret == "" {
		return cfg, fmt.Errorf("client_secret is missing")
	}
	return cfg, nil
}

// realExchanger talks to Google's OAuth token endpoint via golang.org/x/oauth2.
type realExchanger struct {
	clientID     string
	clientSecret string
}

func newRealExchanger(clientID, clientSecret string) tokenExchanger {
	return &realExchanger{clientID: clientID, clientSecret: clientSecret}
}

func (e *realExchanger) ExchangeRefreshToken(ctx context.Context, refreshToken string, scopes []string) (string, time.Time, error) {
	conf := &oauth2.Config{
		ClientID:     e.clientID,
		ClientSecret: e.clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       scopes,
	}
	// A token carrying only a refresh token forces the TokenSource to refresh
	// on the first Token() call, returning a freshly minted access token.
	src := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := src.Token()
	if err != nil {
		return "", time.Time{}, err
	}
	return tok.AccessToken, tok.Expiry, nil
}

// init auto-registers the built-in implementation when the package is imported
// (gateway/main.go blank-imports it alongside gcpiam).
func init() {
	federation.Register(New())
}

// providerMatchesModel asserts the package-local constant stays in sync with
// the model at startup; a build break is preferable to a runtime mismatch.
var _ = func() struct{} {
	if providerName != models.FederationProviderGCPOAuth {
		panic(fmt.Sprintf("gcpoauth: providerName %q does not match models.FederationProviderGCPOAuth %q",
			providerName, models.FederationProviderGCPOAuth))
	}
	return struct{}{}
}()
