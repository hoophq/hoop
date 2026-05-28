package gcpiam

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/federation"
	"github.com/hoophq/hoop/gateway/models"
)

// fakeIssuer is a deterministic stand-in for iamcredentials.GenerateAccessToken.
// Tests configure it with a canned response or error so the gcpiam Resolver
// behavior can be exercised without making a real GCP API call.
type fakeIssuer struct {
	calledPrincipal string
	calledScopes    []string
	calledLifetime  time.Duration
	token           string
	expiresAt       time.Time
	err             error
}

func (f *fakeIssuer) GenerateAccessToken(_ context.Context, principal string, scopes []string, lifetime time.Duration) (string, time.Time, error) {
	f.calledPrincipal = principal
	f.calledScopes = scopes
	f.calledLifetime = lifetime
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return f.token, f.expiresAt, nil
}

// fakeAdminSAJSON returns a syntactically valid service-account JSON blob the
// resolver can parse for client_email. The actual key material is not used
// because the issuer is mocked.
func fakeAdminSAJSON() []byte {
	return []byte(`{"type":"service_account","client_email":"hoop-admin@proj.iam.gserviceaccount.com"}`)
}

func newResolverWithIssuer(issuer *fakeIssuer) *Resolver {
	return &Resolver{
		newIssuer: func(_ context.Context, _ []byte) (tokenIssuer, error) {
			return issuer, nil
		},
	}
}

func newCfg(projectID string, ttlSec int) *models.ConnectionFederationConfig {
	raw, _ := json.Marshal(map[string]any{"project_id": projectID})
	return &models.ConnectionFederationConfig{
		HookSource:      models.FederationHookSourceBuiltin,
		TokenTTLSeconds: ttlSec,
		ExtraConfig:     raw,
	}
}

func TestResolve_HappyPath(t *testing.T) {
	expectedExpiry := time.Date(2026, 5, 25, 18, 0, 0, 0, time.UTC)
	issuer := &fakeIssuer{token: "ya29.deadbeef", expiresAt: expectedExpiry}
	r := newResolverWithIssuer(issuer)

	res, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 1800),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if issuer.calledPrincipal != "alice@acme.com" {
		t.Errorf("issuer received principal %q, want alice@acme.com", issuer.calledPrincipal)
	}
	if issuer.calledLifetime != 30*time.Minute {
		t.Errorf("issuer received lifetime %v, want 30m", issuer.calledLifetime)
	}
	if got := res.EnvVars["HOOP_GCP_ACCESS_TOKEN"]; got != "ya29.deadbeef" {
		t.Errorf("HOOP_GCP_ACCESS_TOKEN=%q, want ya29.deadbeef", got)
	}
	if got := res.EnvVars["CLOUDSDK_CORE_PROJECT"]; got != "my-proj" {
		t.Errorf("CLOUDSDK_CORE_PROJECT=%q, want my-proj", got)
	}
	if got := res.EnvVars["HOOP_FEDERATED_PRINCIPAL"]; got != "alice@acme.com" {
		t.Errorf("HOOP_FEDERATED_PRINCIPAL=%q, want alice@acme.com", got)
	}
	if !res.TokenExpiresAt.Equal(expectedExpiry) {
		t.Errorf("TokenExpiresAt=%v, want %v", res.TokenExpiresAt, expectedExpiry)
	}
	if res.AdminPrincipal != "hoop-admin@proj.iam.gserviceaccount.com" {
		t.Errorf("AdminPrincipal=%q, want hoop-admin@proj.iam.gserviceaccount.com", res.AdminPrincipal)
	}
}

func TestResolve_DefaultLifetimeWhenZero(t *testing.T) {
	issuer := &fakeIssuer{token: "x", expiresAt: time.Now().Add(time.Hour)}
	r := newResolverWithIssuer(issuer)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 0), // zero TTL → fall back to 1 h.
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issuer.calledLifetime != time.Hour {
		t.Errorf("got lifetime %v, want 1h", issuer.calledLifetime)
	}
}

func TestResolve_PropagatesIssuerError(t *testing.T) {
	issuer := &fakeIssuer{err: errors.New("permission denied: missing iam.serviceAccountTokenCreator")}
	r := newResolverWithIssuer(issuer)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "impersonation of \"alice@acme.com\" failed") {
		t.Errorf("error %q should mention the principal", err.Error())
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error %q should preserve the underlying issuer error", err.Error())
	}
}

func TestResolve_RejectsMissingProjectID(t *testing.T) {
	r := newResolverWithIssuer(&fakeIssuer{})
	cfg := newCfg("", 3600)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                cfg,
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "extra_config.project_id is required") {
		t.Errorf("error %q should call out missing project_id", err.Error())
	}
}

func TestResolve_RejectsMissingCredentials(t *testing.T) {
	r := newResolverWithIssuer(&fakeIssuer{})

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: nil,
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing admin service-account credentials") {
		t.Errorf("error %q should say credentials are missing", err.Error())
	}
}

func TestResolve_RejectsEmptyPrincipal(t *testing.T) {
	r := newResolverWithIssuer(&fakeIssuer{})

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "resolved principal is empty") {
		t.Errorf("error %q should describe the missing principal", err.Error())
	}
}

func TestResolve_RejectsMalformedAdminJSON(t *testing.T) {
	r := newResolverWithIssuer(&fakeIssuer{})

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: []byte("not json"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid admin service-account JSON") {
		t.Errorf("error %q should call out malformed JSON", err.Error())
	}
}

func TestProvider_ReturnsRegisteredName(t *testing.T) {
	if New().Provider() != "gcp_iam" {
		t.Errorf("provider name drifted; expected gcp_iam")
	}
}

// TestResolve_SupersedesLegacyGAC pins the public contract: when the gcp_iam
// resolver succeeds, the session-open path must drop the legacy
// GOOGLE_APPLICATION_CREDENTIALS from the connection's static envs. Without
// this, customers who haven't manually pruned their connection see both the
// federated access token AND the legacy SA key file active at runtime — the
// agent's bq wrapper detects the dual-set state and emits a noisy "both set;
// preferring federation" warning on every session, hiding real problems.
func TestResolve_SupersedesLegacyGAC(t *testing.T) {
	issuer := &fakeIssuer{token: "ya29.ok", expiresAt: time.Now().Add(time.Hour)}
	r := newResolverWithIssuer(issuer)

	res, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.SupersededEnvVars) != 1 || res.SupersededEnvVars[0] != "GOOGLE_APPLICATION_CREDENTIALS" {
		t.Errorf("SupersededEnvVars=%v, want [GOOGLE_APPLICATION_CREDENTIALS]", res.SupersededEnvVars)
	}
}

// TestResolve_PreflightRejectsBrokenSAPrincipal exercises the path that bit
// real users: a template like "{user.email}@<project>.iam.gserviceaccount.com"
// against a user whose email is "alice@acme.com" produces a double-"@"
// principal that GCP rejects with an opaque 400. The preflight must turn that
// into a clear local error before any GCP call is made.
func TestResolve_PreflightRejectsBrokenSAPrincipal(t *testing.T) {
	issuer := &fakeIssuer{token: "should-not-be-issued"}
	r := newResolverWithIssuer(issuer)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "alice@acme.com@my-proj.iam.gserviceaccount.com",
	})
	if err == nil {
		t.Fatalf("expected preflight to reject double-@ principal")
	}
	if !strings.Contains(err.Error(), "invalid GCP service-account name") {
		t.Errorf("error %q should call out the invalid SA name", err.Error())
	}
	if !strings.Contains(err.Error(), "{user.email_local}") {
		t.Errorf("error %q should hint at the {user.email_local} placeholder", err.Error())
	}
	if issuer.calledPrincipal != "" {
		t.Errorf("issuer must not be called when preflight rejects; got principal=%q", issuer.calledPrincipal)
	}
}

// TestResolve_PreflightRejectsDottedSALocalPart guards against the silent
// failure mode where a dotted email (e.g. first.last@example.com) is fed into
// {user.email_local} verbatim. SA names can't carry dots; GCP would refuse and
// the preflight should surface the rule before the API call.
func TestResolve_PreflightRejectsDottedSALocalPart(t *testing.T) {
	issuer := &fakeIssuer{}
	r := newResolverWithIssuer(issuer)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "first.last@my-proj.iam.gserviceaccount.com",
	})
	if err == nil {
		t.Fatalf("expected preflight to reject dotted SA local part")
	}
	if !strings.Contains(err.Error(), "invalid GCP service-account name") {
		t.Errorf("error %q should call out the invalid SA name", err.Error())
	}
	if issuer.calledPrincipal != "" {
		t.Errorf("issuer must not be called when preflight rejects; got principal=%q", issuer.calledPrincipal)
	}
}

// TestResolve_PreflightAcceptsWellFormedSAPrincipal documents the happy case:
// a template that uses {user.email_local} cleanly produces a valid SA email,
// preflight is a no-op, and the issuer is invoked with the principal verbatim.
func TestResolve_PreflightAcceptsWellFormedSAPrincipal(t *testing.T) {
	issuer := &fakeIssuer{token: "ya29.ok", expiresAt: time.Now().Add(time.Hour)}
	r := newResolverWithIssuer(issuer)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "matheusmachadoufsc@my-proj.iam.gserviceaccount.com",
	})
	if err != nil {
		t.Fatalf("expected preflight to accept well-formed principal, got: %v", err)
	}
	if issuer.calledPrincipal != "matheusmachadoufsc@my-proj.iam.gserviceaccount.com" {
		t.Errorf("issuer received principal %q, want matheusmachadoufsc@my-proj.iam.gserviceaccount.com", issuer.calledPrincipal)
	}
}

// TestResolve_PreflightIgnoresNonSAPrincipal locks in the design choice that
// only *.iam.gserviceaccount.com principals get the local-part check. Other
// principal types (workforce subjects, user-email pass-throughs for future
// resolvers) shouldn't be rejected by gcpiam's preflight even when reused as
// the resolved principal during tests; the failure mode for them happens at
// GCP itself.
func TestResolve_PreflightIgnoresNonSAPrincipal(t *testing.T) {
	issuer := &fakeIssuer{token: "ya29.x", expiresAt: time.Now().Add(time.Hour)}
	r := newResolverWithIssuer(issuer)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: fakeAdminSAJSON(),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("preflight should be a no-op for non-SA principals; got: %v", err)
	}
	if issuer.calledPrincipal != "alice@acme.com" {
		t.Errorf("issuer received principal %q, want alice@acme.com", issuer.calledPrincipal)
	}
}
