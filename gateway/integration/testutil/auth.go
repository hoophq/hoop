//go:build integration

package testutil

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
)

// FirstUserEmail / FirstUserPassword are the canonical credentials the smoke
// suite registers as the default org's first (admin) user. Exported so tests
// can re-login or assert on the user listing.
const (
	FirstUserEmail    = "admin@smoke.test"
	FirstUserPassword = "smoke-pass-123"
	FirstUserName     = "Smoke Admin"
)

// RegisterFirstUser registers the default org's first user via
// POST /localauth/register and returns the JWT from the Token response header.
// The first user is automatically granted the admin group. Fails the test if
// registration does not return 201 with a token.
func RegisterFirstUser(t *testing.T, s *GatewayTestServer) string {
	t.Helper()
	resp := s.Post(t, "/localauth/register", "", openapi.LocalUserRequest{
		Email:    FirstUserEmail,
		Password: FirstUserPassword,
		Name:     FirstUserName,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("RegisterFirstUser: expected 201, got %d (body: %s)", resp.StatusCode, ReadBody(t, resp))
	}
	token := resp.Header.Get("Token")
	if token == "" {
		t.Fatalf("RegisterFirstUser: missing Token header in registration response")
	}
	return token
}

// Login authenticates via POST /localauth/login and returns the JWT from the
// Token response header. Fails the test if login does not return 200.
func Login(t *testing.T, s *GatewayTestServer, email, password string) string {
	t.Helper()
	resp := s.Post(t, "/localauth/login", "", openapi.LocalUserRequest{
		Email:    email,
		Password: password,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Login: expected 200, got %d (body: %s)", resp.StatusCode, ReadBody(t, resp))
	}
	token := resp.Header.Get("Token")
	if token == "" {
		t.Fatalf("Login: missing Token header in login response")
	}
	return token
}

// CreateHPKApiKey creates an hpk_ API key via POST /api-keys using the given
// admin token, and returns the raw key (shown only once). groups assigns the
// key's RBAC groups (e.g. []string{"admin"} or []string{"auditor"}).
func CreateHPKApiKey(t *testing.T, s *GatewayTestServer, adminToken, name string, groups []string) string {
	t.Helper()
	resp := s.Post(t, "/api-keys", adminToken, openapi.APIKeyCreateRequest{
		Name:   name,
		Groups: groups,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CreateHPKApiKey: expected 201, got %d (body: %s)", resp.StatusCode, ReadBody(t, resp))
	}
	var out openapi.APIKeyCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("CreateHPKApiKey: decode response: %v", err)
	}
	if out.Key == "" {
		t.Fatalf("CreateHPKApiKey: empty key in response")
	}
	return out.Key
}
