// Package gcpiam implements the built-in GCP IAM Federation resolver.
//
// At session-open time the resolver impersonates the calling user's GCP
// principal using the admin service-account configured by the customer. The
// admin SA must hold roles/iam.serviceAccountTokenCreator on the target
// principal; without it iamcredentials.GenerateAccessToken returns a 403 the
// resolver propagates verbatim (PRD §5.3 requires actionable failure
// messages).
//
// Env-var contract emitted to the agent:
//
//	HOOP_GCP_ACCESS_TOKEN         The short-lived OAuth bearer token.
//	HOOP_GCP_TOKEN_EXPIRES_AT     RFC3339 expiry of the bearer.
//	CLOUDSDK_CORE_PROJECT         The GCP project id from extra_config.project_id.
//	HOOP_FEDERATED_PRINCIPAL      The resolved user principal (e.g. user@org.com).
//	                              Informational, surfaces in audit + bq logs.
//
// The agent's bq wrapper at rootfs/usr/local/bin/bq writes the access token to
// a tmpfile and exports CLOUDSDK_AUTH_ACCESS_TOKEN_FILE so the bq CLI picks up
// the impersonated identity. Customers who bring their own agent image are
// responsible for plumbing these vars into their tooling; the env-var names
// are the public contract.
package gcpiam

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hoophq/hoop/gateway/federation"
	"github.com/hoophq/hoop/gateway/models"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"
)

// iamCredentialsScope is the OAuth scope required to call
// iamcredentials.googleapis.com with the admin service account.
const iamCredentialsScope = "https://www.googleapis.com/auth/cloud-platform"

// saEmailSuffix is the fixed tail of every GCP service-account email. A
// resolved principal that ends with this suffix MUST conform to GCP's SA
// naming rules; otherwise iamcredentials.GenerateAccessToken returns a
// generic "400 Invalid form of account ID" that obscures the real problem
// (almost always a template that concatenated a full email into the local
// part; see {user.email_local} in gateway/federation/identity.go).
const saEmailSuffix = ".iam.gserviceaccount.com"

// saLocalPartRegex enforces GCP's documented service-account ID rules:
// lowercase letters/digits/hyphens, must start with a letter, must end with a
// letter or digit, length 6-30. Source:
// https://cloud.google.com/iam/docs/service-accounts-create#creating
var saLocalPartRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// providerName matches models.FederationProviderGCPIAM; duplicated as a
// package-local string so the gcpiam package compiles without importing
// models (it does import models below for the request context, but the
// constant string keeps the registry entry self-contained).
const providerName = "gcp_iam"

// tokenIssuer abstracts the iamcredentials.GenerateAccessToken call so unit
// tests can inject a fake without spinning up a real Google client. Production
// uses newRealIssuer which talks to iamcredentials.googleapis.com over HTTP.
type tokenIssuer interface {
	GenerateAccessToken(ctx context.Context, principal string, scopes []string, lifetime time.Duration) (token string, expiresAt time.Time, err error)
}

// Resolver implements federation.Resolver for GCP IAM impersonation.
type Resolver struct {
	// newIssuer is the constructor used to build a per-resolve issuer from
	// the admin credentials. Pluggable so tests can substitute an in-memory
	// fake.
	newIssuer func(ctx context.Context, adminSAJSON []byte) (tokenIssuer, error)
}

// New returns a production-ready GCP IAM resolver. The default issuer factory
// authenticates the iamcredentials client with the admin SA JSON the caller
// has decrypted out of models.ConnectionFederationConfig.
func New() *Resolver {
	return &Resolver{newIssuer: newRealIssuer}
}

// Provider satisfies federation.Resolver.
func (r *Resolver) Provider() string { return providerName }

// extraConfig is the parsed shape of models.ConnectionFederationConfig.ExtraConfig
// for the gcp_iam provider. Stored as JSONB so additional fields can be added
// without a migration; v1 only needs project_id.
type extraConfig struct {
	ProjectID string `json:"project_id"`
}

// Resolve impersonates the configured target principal and returns the
// env-var bundle the agent will inject into the session command.
func (r *Resolver) Resolve(ctx context.Context, req federation.ResolveRequest) (*federation.Result, error) {
	if req.Config == nil {
		return nil, fmt.Errorf("missing federation config")
	}
	if len(req.AdminCredentialsPlain) == 0 {
		return nil, fmt.Errorf("missing admin service-account credentials")
	}
	if req.ResolvedPrincipal == "" {
		return nil, fmt.Errorf("resolved principal is empty (identity mapping returned nothing)")
	}

	extra, err := parseExtraConfig(req.Config.ExtraConfig)
	if err != nil {
		return nil, fmt.Errorf("invalid extra_config: %w", err)
	}
	if extra.ProjectID == "" {
		return nil, fmt.Errorf("extra_config.project_id is required for gcp_iam federation")
	}

	adminEmail, err := parseAdminServiceAccountEmail(req.AdminCredentialsPlain)
	if err != nil {
		return nil, fmt.Errorf("invalid admin service-account JSON: %w", err)
	}

	if err := preflightServiceAccountPrincipal(req.ResolvedPrincipal); err != nil {
		return nil, err
	}

	issuer, err := r.newIssuer(ctx, req.AdminCredentialsPlain)
	if err != nil {
		return nil, fmt.Errorf("failed building iamcredentials client: %w", err)
	}

	lifetime := time.Duration(req.Config.TokenTTLSeconds) * time.Second
	if lifetime <= 0 {
		lifetime = time.Hour
	}

	token, expiresAt, err := issuer.GenerateAccessToken(ctx,
		req.ResolvedPrincipal,
		[]string{iamCredentialsScope},
		lifetime,
	)
	if err != nil {
		// Wrap with the principal so admins can correlate failures with
		// missing iam.serviceAccountTokenCreator grants on GCP. The
		// underlying googleapi.Error preserves the GCP message.
		return nil, fmt.Errorf("impersonation of %q failed: %w", req.ResolvedPrincipal, err)
	}

	return &federation.Result{
		EnvVars: map[string]string{
			"HOOP_GCP_ACCESS_TOKEN":     token,
			"HOOP_GCP_TOKEN_EXPIRES_AT": expiresAt.UTC().Format(time.RFC3339),
			"CLOUDSDK_CORE_PROJECT":     extra.ProjectID,
			"HOOP_FEDERATED_PRINCIPAL":  req.ResolvedPrincipal,
		},
		ResolvedPrincipal: req.ResolvedPrincipal,
		AdminPrincipal:    adminEmail,
		TokenExpiresAt:    expiresAt,
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

// adminServiceAccountJSON is the minimal subset of a GCP service-account key
// file the resolver reads. We only need client_email for audit metadata; the
// rest is handed off to google.CredentialsFromJSON.
type adminServiceAccountJSON struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
}

// preflightServiceAccountPrincipal validates the shape of an SA principal
// before it is sent to iamcredentials.GenerateAccessToken. Non-SA principals
// (anything that does not end in .iam.gserviceaccount.com) pass through
// unchanged: we only know the rules for our own domain. SA principals get a
// regex check on the local part so the operator sees a precise, actionable
// error instead of GCP's generic "400 Invalid form of account ID".
//
// The typical trigger for this path is a template like
// "{user.email}@<project>.iam.gserviceaccount.com" applied to a user whose
// email is "alice@acme.com", producing the obviously-broken double-"@"
// principal "alice@acme.com@<project>.iam.gserviceaccount.com". The
// {user.email_local} placeholder exists to avoid that mistake; this preflight
// catches templates that still produce something illegal (e.g. emails with
// dots or plus signs that no SA name can carry).
func preflightServiceAccountPrincipal(principal string) error {
	if !strings.HasSuffix(principal, saEmailSuffix) {
		return nil
	}
	localPart := strings.TrimSuffix(principal, saEmailSuffix)
	at := strings.LastIndex(localPart, "@")
	if at >= 0 {
		// Strip the project portion ("local@project") to isolate the SA
		// name; GCP SA emails are "<name>@<project>.iam.gserviceaccount.com".
		localPart = localPart[:at]
	}
	if !saLocalPartRegex.MatchString(localPart) {
		return fmt.Errorf(
			"resolved principal %q has an invalid GCP service-account name %q: "+
				"must be 6-30 chars, start with a lowercase letter, contain only [a-z0-9-], and end with a letter or digit "+
				"(hint: chain {user.email_local} with @<project>.iam.gserviceaccount.com and avoid dots/plus in the user's email)",
			principal, localPart,
		)
	}
	return nil
}

// parseAdminServiceAccountEmail extracts the admin SA's client_email from the
// JSON blob. Returns a usable error if the blob is malformed or missing the
// claim. PUT /federation runs this so save-time validation surfaces the
// problem before any session uses the config.
func parseAdminServiceAccountEmail(raw []byte) (string, error) {
	var sa adminServiceAccountJSON
	if err := json.Unmarshal(raw, &sa); err != nil {
		return "", fmt.Errorf("not valid JSON: %v", err)
	}
	if sa.Type != "" && sa.Type != "service_account" {
		return "", fmt.Errorf("expected service_account JSON, got type=%q", sa.Type)
	}
	if sa.ClientEmail == "" {
		return "", fmt.Errorf("client_email claim is missing")
	}
	return sa.ClientEmail, nil
}

// realIssuer wraps iamcredentials.Service so the production code path uses
// the actual Google client while tests inject a fake.
type realIssuer struct {
	svc *iamcredentials.Service
}

func newRealIssuer(ctx context.Context, adminSAJSON []byte) (tokenIssuer, error) {
	creds, err := google.CredentialsFromJSON(ctx, adminSAJSON, iamCredentialsScope)
	if err != nil {
		return nil, fmt.Errorf("CredentialsFromJSON: %w", err)
	}
	svc, err := iamcredentials.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("iamcredentials.NewService: %w", err)
	}
	return &realIssuer{svc: svc}, nil
}

func (r *realIssuer) GenerateAccessToken(ctx context.Context, principal string, scopes []string, lifetime time.Duration) (string, time.Time, error) {
	resp, err := r.svc.Projects.ServiceAccounts.
		GenerateAccessToken(
			fmt.Sprintf("projects/-/serviceAccounts/%s", principal),
			&iamcredentials.GenerateAccessTokenRequest{
				Scope:    scopes,
				Lifetime: fmt.Sprintf("%ds", int(lifetime.Seconds())),
			}).
		Context(ctx).
		Do()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt, perr := time.Parse(time.RFC3339, resp.ExpireTime)
	if perr != nil {
		// The expiry is required by the API contract; if we cannot parse
		// it, fall back to "now + lifetime" so callers still get a
		// non-zero deadline for audit.
		expiresAt = time.Now().UTC().Add(lifetime)
	}
	return resp.AccessToken, expiresAt, nil
}

// MustRegister is intended to be called from package main once the binary
// decides which providers to ship. The init function below auto-registers the
// built-in implementation; tests that need a clean registry should test via
// New() directly.
func init() {
	federation.Register(New())
}

// providerMatchesModel keeps the package-local constant in sync with the model
// during compile-time assertion. If the strings ever drift a build break is
// preferable to a runtime mismatch.
var _ = func() struct{} {
	if providerName != models.FederationProviderGCPIAM {
		panic(fmt.Sprintf("gcpiam: providerName %q does not match models.FederationProviderGCPIAM %q",
			providerName, models.FederationProviderGCPIAM))
	}
	return struct{}{}
}()
