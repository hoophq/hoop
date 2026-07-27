package gcpoauth

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

// fakeExchanger is a deterministic stand-in for the Google token endpoint so
// the resolver can be exercised without a network call.
type fakeExchanger struct {
	gotRefreshToken string
	gotScopes       []string
	token           string
	expiresAt       time.Time
	err             error
}

func (f *fakeExchanger) ExchangeRefreshToken(_ context.Context, refreshToken string, scopes []string) (string, time.Time, error) {
	f.gotRefreshToken = refreshToken
	f.gotScopes = scopes
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return f.token, f.expiresAt, nil
}

func newResolverWithExchanger(ex *fakeExchanger) *Resolver {
	return &Resolver{
		newExchanger: func(_, _ string) tokenExchanger { return ex },
	}
}

func clientCredsJSON() []byte {
	return []byte(`{"client_id":"client-123.apps.googleusercontent.com","client_secret":"secret-xyz"}`)
}

func newCfg(projectID string, ttlSec int, scopes ...string) *models.ConnectionFederationConfig {
	extra := map[string]any{"project_id": projectID}
	if len(scopes) > 0 {
		extra["scopes"] = scopes
	}
	raw, _ := json.Marshal(extra)
	return &models.ConnectionFederationConfig{
		HookSource:      models.FederationHookSourceBuiltin,
		TokenTTLSeconds: ttlSec,
		ExtraConfig:     raw,
	}
}

func TestResolve_HappyPath(t *testing.T) {
	expiry := time.Date(2026, 6, 15, 18, 0, 0, 0, time.UTC)
	ex := &fakeExchanger{token: "ya29.user-token", expiresAt: expiry}
	r := newResolverWithExchanger(ex)

	res, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  []byte("1//refresh-token"),
		ResolvedPrincipal:     "alice@acme.com",
		UserEmail:             "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ex.gotRefreshToken != "1//refresh-token" {
		t.Errorf("exchanger received refresh token %q, want 1//refresh-token", ex.gotRefreshToken)
	}
	if got := res.EnvVars["HOOP_GCP_ACCESS_TOKEN"]; got != "ya29.user-token" {
		t.Errorf("HOOP_GCP_ACCESS_TOKEN=%q, want ya29.user-token", got)
	}
	if got := res.EnvVars["CLOUDSDK_CORE_PROJECT"]; got != "my-proj" {
		t.Errorf("CLOUDSDK_CORE_PROJECT=%q, want my-proj", got)
	}
	if got := res.EnvVars["HOOP_FEDERATED_PRINCIPAL"]; got != "alice@acme.com" {
		t.Errorf("HOOP_FEDERATED_PRINCIPAL=%q, want alice@acme.com", got)
	}
	if !res.TokenExpiresAt.Equal(expiry) {
		t.Errorf("TokenExpiresAt=%v, want %v", res.TokenExpiresAt, expiry)
	}
	if res.AdminPrincipal != "client-123.apps.googleusercontent.com" {
		t.Errorf("AdminPrincipal=%q, want the client_id", res.AdminPrincipal)
	}
}

func TestResolve_DefaultsToCloudPlatformScope(t *testing.T) {
	ex := &fakeExchanger{token: "x", expiresAt: time.Now().Add(time.Hour)}
	r := newResolverWithExchanger(ex)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ex.gotScopes) != 1 || ex.gotScopes[0] != cloudPlatformScope {
		t.Errorf("scopes=%v, want [%s]", ex.gotScopes, cloudPlatformScope)
	}
}

func TestResolve_HonorsConfiguredScopes(t *testing.T) {
	ex := &fakeExchanger{token: "x", expiresAt: time.Now().Add(time.Hour)}
	r := newResolverWithExchanger(ex)

	want := "https://www.googleapis.com/auth/bigquery"
	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600, want),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ex.gotScopes) != 1 || ex.gotScopes[0] != want {
		t.Errorf("scopes=%v, want [%s]", ex.gotScopes, want)
	}
}

func TestResolve_RejectsMissingUserCredential(t *testing.T) {
	ex := &fakeExchanger{}
	r := newResolverWithExchanger(ex)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  nil, // not connected
		ResolvedPrincipal:     "alice@acme.com",
		UserEmail:             "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "has not connected a Google account") {
		t.Errorf("error %q should tell the user to complete the consent flow", err.Error())
	}
	if ex.gotRefreshToken != "" {
		t.Errorf("exchanger must not be called when no user credential is present")
	}
}

func TestResolve_RejectsMissingClientCredentials(t *testing.T) {
	r := newResolverWithExchanger(&fakeExchanger{})

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: nil,
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing oauth client credentials") {
		t.Errorf("error %q should call out the missing client credentials", err.Error())
	}
}

func TestResolve_RejectsMalformedClientCredentials(t *testing.T) {
	r := newResolverWithExchanger(&fakeExchanger{})

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: []byte(`{"client_id":""}`),
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid oauth client credentials") {
		t.Errorf("error %q should call out invalid client credentials", err.Error())
	}
}

func TestResolve_RejectsMissingProjectID(t *testing.T) {
	r := newResolverWithExchanger(&fakeExchanger{})

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("", 3600),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "project_id is required") {
		t.Errorf("error %q should call out missing project_id", err.Error())
	}
}

func TestResolve_RejectsEmptyPrincipal(t *testing.T) {
	r := newResolverWithExchanger(&fakeExchanger{})

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "resolved principal is empty") {
		t.Errorf("error %q should describe the missing principal", err.Error())
	}
}

func TestResolve_PropagatesExchangeError(t *testing.T) {
	ex := &fakeExchanger{err: errors.New("invalid_grant: token expired or revoked")}
	r := newResolverWithExchanger(ex)

	_, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "alice@acme.com") {
		t.Errorf("error %q should mention the principal", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error %q should preserve the underlying exchange error", err.Error())
	}
}

// TestResolve_SupersedesLegacyGAC pins the same supersede contract gcp_iam
// guarantees: a successful resolve drops the legacy
// GOOGLE_APPLICATION_CREDENTIALS key-file env from the session.
func TestResolve_SupersedesLegacyGAC(t *testing.T) {
	ex := &fakeExchanger{token: "ya29.ok", expiresAt: time.Now().Add(time.Hour)}
	r := newResolverWithExchanger(ex)

	res, err := r.Resolve(context.Background(), federation.ResolveRequest{
		Config:                newCfg("my-proj", 3600),
		AdminCredentialsPlain: clientCredsJSON(),
		UserCredentialsPlain:  []byte("rt"),
		ResolvedPrincipal:     "alice@acme.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.SupersededEnvVars) != 1 || res.SupersededEnvVars[0] != "GOOGLE_APPLICATION_CREDENTIALS" {
		t.Errorf("SupersededEnvVars=%v, want [GOOGLE_APPLICATION_CREDENTIALS]", res.SupersededEnvVars)
	}
}

func TestProvider_ReturnsRegisteredName(t *testing.T) {
	if New().Provider() != "gcp_oauth" {
		t.Errorf("provider name drifted; expected gcp_oauth")
	}
	if New().Provider() != models.FederationProviderGCPOAuth {
		t.Errorf("provider name out of sync with models constant")
	}
}
