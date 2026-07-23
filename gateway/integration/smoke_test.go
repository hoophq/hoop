//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
	"github.com/hoophq/hoop/gateway/models"
)

// These smoke tests run serially against one shared gateway + DB initialized
// in TestMain. They MUST NOT be parallelized: they share package globals
// (testServer, the gateway's appconfig/models.DB/global user roles) and one
// database, and adminToken's first-user bootstrap is not concurrency-safe.
// Tests use uniquely named resources and clean up after themselves so reruns
// and ordering stay independent; the only intentional order dependency
// (first-user creation) is funneled through adminToken.
//
// Body lifecycle: every *http.Response obtained below is closed exactly once
// via a deferred close at the call site; the testutil helpers only read.

// adminToken returns a JWT for the default admin user. The first invocation
// registers the user (granting it the admin group); later invocations log in.
func adminToken(t *testing.T) string {
	t.Helper()
	// Try login first; if the user doesn't exist yet, register.
	resp := testServer.Post(t, "/localauth/login", "", openapi.LocalUserRequest{
		Email:    testutil.FirstUserEmail,
		Password: testutil.FirstUserPassword,
	})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		token := resp.Header.Get("Token")
		if token == "" {
			t.Fatal("adminToken: login returned 200 without Token header")
		}
		return token
	}
	return testutil.RegisterFirstUser(t, testServer)
}

// T1 — unauthenticated public server info.
func TestPublicServerInfo(t *testing.T) {
	resp := testServer.Get(t, "/publicserverinfo", "")
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if _, ok := body["auth_method"]; !ok {
		t.Errorf("publicserverinfo: missing auth_method field, got %v", body)
	}
}

// T2 — first-user registration and its security boundary.
func TestRegisterFirstUser(t *testing.T) {
	// Ensure the first user exists (idempotent via adminToken).
	_ = adminToken(t)

	// A second registration with a *different* email must be rejected: once
	// the org has a user, self-registration is closed (403), not a conflict.
	resp := testServer.Post(t, "/localauth/register", "", openapi.LocalUserRequest{
		Email:    "intruder@smoke.test",
		Password: "whatever-123",
		Name:     "Intruder",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("second registration: expected 403, got %d (body: %s)",
			resp.StatusCode, testutil.ReadBody(t, resp))
	}
}

// T3 — login success and wrong-password rejection.
func TestLogin(t *testing.T) {
	_ = adminToken(t) // ensure user exists

	// Correct credentials.
	token := testutil.Login(t, testServer, testutil.FirstUserEmail, testutil.FirstUserPassword)
	if token == "" {
		t.Fatal("login returned empty token")
	}

	// Wrong password.
	resp := testServer.Post(t, "/localauth/login", "", openapi.LocalUserRequest{
		Email:    testutil.FirstUserEmail,
		Password: "wrong-password",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong-password login: expected 401, got %d", resp.StatusCode)
	}
}

// T4 — serverinfo requires auth and returns version metadata.
func TestServerInfo(t *testing.T) {
	// No token → 401.
	noAuth := testServer.Get(t, "/serverinfo", "")
	defer noAuth.Body.Close()
	if noAuth.StatusCode != http.StatusUnauthorized {
		t.Errorf("serverinfo without token: expected 401, got %d", noAuth.StatusCode)
	}

	// Valid token → 200 with a version field.
	token := adminToken(t)
	resp := testServer.Get(t, "/serverinfo", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if _, ok := body["version"]; !ok {
		t.Errorf("serverinfo: missing version field, got keys %v", keysOf(body))
	}
}

// T5 — user listing returns the registered admin (read-only access).
func TestUserListing(t *testing.T) {
	token := adminToken(t)
	resp := testServer.Get(t, "/users", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	var users []map[string]any
	testutil.DecodeJSON(t, resp, &users)
	found := false
	for _, u := range users {
		if email, _ := u["email"].(string); email == testutil.FirstUserEmail {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("user listing: registered admin %q not found", testutil.FirstUserEmail)
	}
}

// T6 — full connection CRUD lifecycle.
func TestConnectionCRUD(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "conn-crud-agent")
	defer deleteAgent(t, token, "conn-crud-agent")

	const connName = "smoke-conn-crud"
	// Create. AccessMode* and AccessSchema are required enabled/disabled flags.
	created := testServer.Post(t, "/connections", token, openapi.Connection{
		Name:               connName,
		Type:               "database",
		SubType:            "postgres",
		AgentId:            agentID,
		Command:            []string{"psql"},
		AccessModeRunbooks: "enabled",
		AccessModeExec:     "enabled",
		AccessModeConnect:  "enabled",
		AccessSchema:       "enabled",
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)

	// List contains it.
	list := testServer.Get(t, "/connections", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var conns []map[string]any
	testutil.DecodeJSON(t, list, &conns)
	if !containsName(conns, connName) {
		t.Errorf("connection listing: %q not found after create", connName)
	}

	// Get by name.
	got := testServer.Get(t, "/connections/"+connName, token)
	defer got.Body.Close()
	testutil.RequireStatus(t, got, http.StatusOK)
	var conn map[string]any
	testutil.DecodeJSON(t, got, &conn)
	if conn["name"] != connName {
		t.Errorf("get connection: expected name %q, got %v", connName, conn["name"])
	}

	// Delete.
	del := testServer.Delete(t, "/connections/"+connName, token)
	defer del.Body.Close()
	if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
		t.Errorf("delete connection: expected 200/204, got %d (body: %s)",
			del.StatusCode, testutil.ReadBody(t, del))
	}

	// Gone.
	gone := testServer.Get(t, "/connections/"+connName, token)
	defer gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("get deleted connection: expected 404, got %d", gone.StatusCode)
	}
}

// T6b — POST /resources persists per-role attributes on the created connections.
func TestResourceCreateWithRoleAttributes(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "resource-attrs-agent")
	defer deleteAgent(t, token, "resource-attrs-agent")

	const resourceName = "smoke-resource-attrs"
	const roleWithAttrs = "smoke-resource-attrs-role-1"
	const roleWithoutAttrs = "smoke-resource-attrs-role-2"

	created := testServer.Post(t, "/resources", token, openapi.ResourceRequest{
		Name:    resourceName,
		Type:    "database",
		SubType: "postgres",
		AgentID: agentID,
		EnvVars: map[string]string{},
		Roles: []openapi.ResourceRoleRequest{
			{
				Name:       roleWithAttrs,
				Type:       "database",
				SubType:    "postgres",
				Command:    []string{"psql"},
				Attributes: []string{"evl87-team", "evl87-pii"},
			},
			{
				Name:    roleWithoutAttrs,
				Type:    "database",
				SubType: "postgres",
				Command: []string{"psql"},
			},
		},
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	defer func() {
		// Connections must go before the resource.
		for _, name := range []string{roleWithAttrs, roleWithoutAttrs} {
			del := testServer.Delete(t, "/connections/"+name, token)
			del.Body.Close()
		}
		del := testServer.Delete(t, "/resources/"+resourceName, token)
		del.Body.Close()
	}()

	got := testServer.Get(t, "/connections/"+roleWithAttrs, token)
	defer got.Body.Close()
	testutil.RequireStatus(t, got, http.StatusOK)
	var conn map[string]any
	testutil.DecodeJSON(t, got, &conn)
	attrs, _ := conn["attributes"].([]any)
	for _, want := range []string{"evl87-team", "evl87-pii"} {
		found := false
		for _, a := range attrs {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("connection %q: expected attribute %q in %v", roleWithAttrs, want, attrs)
		}
	}

	gotBare := testServer.Get(t, "/connections/"+roleWithoutAttrs, token)
	defer gotBare.Body.Close()
	testutil.RequireStatus(t, gotBare, http.StatusOK)
	var bare map[string]any
	testutil.DecodeJSON(t, gotBare, &bare)
	if bareAttrs, ok := bare["attributes"].([]any); ok && len(bareAttrs) > 0 {
		t.Errorf("connection %q: expected no attributes, got %v", roleWithoutAttrs, bareAttrs)
	}
}

// T6c — protection profile: managed rules expose managed_by, managed masking
// rules are immutable, and skip_protection_profile opts a role out of tagging.
func TestProtectionProfileManagedRules(t *testing.T) {
	token := adminToken(t)

	applied := testServer.Put(t, "/orgs/protection-profile", token, map[string]any{
		"profile": "protection-permissive",
		"source":  "settings",
	})
	defer applied.Body.Close()
	testutil.RequireStatus(t, applied, http.StatusOK)
	defer func() {
		// Back to manual configuration: tears down all managed rules/attributes
		// so later tests see a clean org.
		reset := testServer.Put(t, "/orgs/protection-profile", token, map[string]any{
			"profile": nil,
			"source":  "settings",
		})
		reset.Body.Close()
	}()

	// Managed guardrail is listed with managed_by set.
	grList := testServer.Get(t, "/guardrails", token)
	defer grList.Body.Close()
	testutil.RequireStatus(t, grList, http.StatusOK)
	var guardrails []map[string]any
	testutil.DecodeJSON(t, grList, &guardrails)
	managedGuardrails := 0
	for _, g := range guardrails {
		if g["managed_by"] == "hoop" {
			managedGuardrails++
		}
	}
	if managedGuardrails == 0 {
		t.Errorf("guardrails list: expected at least one rule with managed_by=hoop, got none")
	}

	// Managed masking rule is listed with managed_by set and refuses updates.
	dmList := testServer.Get(t, "/datamasking-rules", token)
	defer dmList.Body.Close()
	testutil.RequireStatus(t, dmList, http.StatusOK)
	var maskingRules []map[string]any
	testutil.DecodeJSON(t, dmList, &maskingRules)
	var managedMasking map[string]any
	for _, r := range maskingRules {
		if r["managed_by"] == "hoop" {
			managedMasking = r
			break
		}
	}
	if managedMasking == nil {
		t.Fatalf("datamasking list: expected a rule with managed_by=hoop, got none")
	}
	blocked := testServer.Put(t, "/datamasking-rules/"+managedMasking["id"].(string), token, map[string]any{
		"name":                   managedMasking["name"],
		"description":            "tampered",
		"connection_ids":         []string{},
		"attributes":             []string{},
		"supported_entity_types": []map[string]any{},
		"custom_entity_types":    []map[string]any{},
		"score_threshold":        0.6,
	})
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusBadRequest {
		t.Errorf("update managed masking rule: expected 400, got %d (body: %s)",
			blocked.StatusCode, testutil.ReadBody(t, blocked))
	}

	// skip_protection_profile opts a role out of the profile attribute.
	agentID := createAgentReturningID(t, token, "profile-skip-agent")
	defer deleteAgent(t, token, "profile-skip-agent")
	const resourceName = "smoke-profile-skip"
	const taggedRole = "smoke-profile-skip-role-tagged"
	const skippedRole = "smoke-profile-skip-role-skipped"
	created := testServer.Post(t, "/resources", token, openapi.ResourceRequest{
		Name:    resourceName,
		Type:    "database",
		SubType: "postgres",
		AgentID: agentID,
		EnvVars: map[string]string{},
		Roles: []openapi.ResourceRoleRequest{
			{Name: taggedRole, Type: "database", SubType: "postgres", Command: []string{"psql"}},
			{Name: skippedRole, Type: "database", SubType: "postgres", Command: []string{"psql"},
				SkipProtectionProfile: true},
		},
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	defer func() {
		for _, name := range []string{taggedRole, skippedRole} {
			del := testServer.Delete(t, "/connections/"+name, token)
			del.Body.Close()
		}
		del := testServer.Delete(t, "/resources/"+resourceName, token)
		del.Body.Close()
	}()

	countProfileAttrs := func(connName string) int64 {
		var count int64
		err := models.DB.Raw(`SELECT COUNT(*) FROM private.connections_attributes
			WHERE connection_name = ? AND attribute_name LIKE 'hoop_protection_profile-%'`, connName).
			Scan(&count).Error
		if err != nil {
			t.Fatalf("querying connections_attributes for %s: %v", connName, err)
		}
		return count
	}
	if got := countProfileAttrs(taggedRole); got != 1 {
		t.Errorf("connection %q: expected 1 profile attribute association, got %d", taggedRole, got)
	}
	if got := countProfileAttrs(skippedRole); got != 0 {
		t.Errorf("connection %q: expected 0 profile attribute associations (skip_protection_profile), got %d", skippedRole, got)
	}

	// The managed attribute is exposed read-only via managed_attributes on
	// connection reads (the regular attributes array keeps hiding it).
	managedAttrsOf := func(connName string) []any {
		resp := testServer.Get(t, "/connections/"+connName, token)
		defer resp.Body.Close()
		testutil.RequireStatus(t, resp, http.StatusOK)
		var conn map[string]any
		testutil.DecodeJSON(t, resp, &conn)
		if attrs, ok := conn["attributes"].([]any); ok {
			for _, a := range attrs {
				if s, _ := a.(string); strings.HasPrefix(s, "hoop_protection_profile-") {
					t.Errorf("connection %q: managed attribute leaked into attributes: %v", connName, attrs)
				}
			}
		}
		managed, _ := conn["managed_attributes"].([]any)
		return managed
	}
	if managed := managedAttrsOf(taggedRole); len(managed) != 1 {
		t.Errorf("connection %q: expected 1 managed attribute in managed_attributes, got %v", taggedRole, managed)
	}
	if managed := managedAttrsOf(skippedRole); len(managed) != 0 {
		t.Errorf("connection %q: expected empty managed_attributes, got %v", skippedRole, managed)
	}
}

// T7 — unknown connection returns 404.
func TestConnectionNotFound(t *testing.T) {
	token := adminToken(t)
	resp := testServer.Get(t, "/connections/does-not-exist-xyz", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown connection: expected 404, got %d", resp.StatusCode)
	}
}

// T8 — agent create/list/delete; the create response carries a gRPC DSN.
func TestAgentCRUD(t *testing.T) {
	token := adminToken(t)
	const agentName = "smoke-agent-crud"

	create := testServer.Post(t, "/agents", token, openapi.AgentRequest{
		Name: agentName,
		Mode: "standard",
	})
	defer create.Body.Close()
	testutil.RequireStatus(t, create, http.StatusCreated)
	var created openapi.AgentCreateResponse
	testutil.DecodeJSON(t, create, &created)
	if !strings.Contains(created.Token, "grpc") {
		t.Errorf("agent create: token does not look like a gRPC DSN: %q", created.Token)
	}

	// List contains it.
	list := testServer.Get(t, "/agents", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var agents []map[string]any
	testutil.DecodeJSON(t, list, &agents)
	if !containsName(agents, agentName) {
		t.Errorf("agent listing: %q not found after create", agentName)
	}

	// Delete.
	deleteAgent(t, token, agentName)
}

// T9 — feature flags list is non-empty and every entry exposes a name.
func TestFeatureFlags(t *testing.T) {
	token := adminToken(t)
	resp := testServer.Get(t, "/feature-flags", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	var flags []map[string]any
	testutil.DecodeJSON(t, resp, &flags)
	if len(flags) == 0 {
		t.Fatal("feature-flags: expected a non-empty catalog")
	}
	for i, f := range flags {
		if _, ok := f["name"]; !ok {
			t.Errorf("feature-flags[%d]: missing name field: %v", i, f)
		}
	}
}

// T10 — hpk_ API key lifecycle: create, authenticate with it, revoke, reject.
func TestHPKApiKeyLifecycle(t *testing.T) {
	token := adminToken(t)
	const keyName = "smoke-key-admin"

	rawKey := testutil.CreateHPKApiKey(t, testServer, token, keyName, []string{"admin"})
	if !strings.HasPrefix(rawKey, "hpk_") {
		t.Fatalf("api key: expected hpk_ prefix, got %q", rawKey)
	}

	// The key authenticates a read-only request.
	ok := testServer.Get(t, "/connections", rawKey)
	defer ok.Body.Close()
	testutil.RequireStatus(t, ok, http.StatusOK)

	// Revoke.
	del := testServer.Delete(t, "/api-keys/"+keyName, token)
	defer del.Body.Close()
	if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke api key: expected 200/204, got %d (body: %s)",
			del.StatusCode, testutil.ReadBody(t, del))
	}

	// Revoked key is rejected.
	denied := testServer.Get(t, "/connections", rawKey)
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusUnauthorized && denied.StatusCode != http.StatusForbidden {
		t.Errorf("revoked api key: expected 401/403, got %d", denied.StatusCode)
	}
}

// T11 — RBAC enforcement: an auditor-scoped key cannot create connections.
func TestRoleEnforcement(t *testing.T) {
	token := adminToken(t)
	const keyName = "smoke-key-auditor"

	auditorKey := testutil.CreateHPKApiKey(t, testServer, token, keyName, []string{"auditor"})
	defer func() {
		resp := testServer.Delete(t, "/api-keys/"+keyName, token)
		resp.Body.Close()
	}()

	// Admin-only endpoint must reject the auditor key.
	resp := testServer.Post(t, "/connections", auditorKey, openapi.Connection{
		Name:    "rbac-denied-conn",
		Type:    "database",
		SubType: "postgres",
		AgentId: "00000000-0000-0000-0000-000000000000",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("auditor creating connection: expected 403, got %d", resp.StatusCode)
	}
}

// T12 — healthz reports degraded liveness when no gRPC server is running, as
// in this in-process harness. Asserts the documented degraded contract (400 +
// liveness=ERR) and that the route is reachable without auth.
func TestHealthzDegraded(t *testing.T) {
	resp := testServer.Get(t, "/healthz", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("healthz (no gRPC): expected 400 degraded, got %d (body: %s)",
			resp.StatusCode, testutil.ReadBody(t, resp))
	}
	var body map[string]any
	testutil.DecodeJSON(t, resp, &body)
	if body["liveness"] != "ERR" {
		t.Errorf("healthz (no gRPC): expected liveness=ERR, got %v", body["liveness"])
	}
}

// --- helpers ---

// createAgentReturningID creates an agent and returns its UUID by reading the
// agent listing (the create response only carries the DSN token).
func createAgentReturningID(t *testing.T, token, name string) string {
	t.Helper()
	create := testServer.Post(t, "/agents", token, openapi.AgentRequest{Name: name, Mode: "standard"})
	defer create.Body.Close()
	testutil.RequireStatus(t, create, http.StatusCreated)

	list := testServer.Get(t, "/agents", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var agents []map[string]any
	testutil.DecodeJSON(t, list, &agents)
	for _, a := range agents {
		if a["name"] == name {
			if id, _ := a["id"].(string); id != "" {
				return id
			}
		}
	}
	t.Fatalf("createAgentReturningID: agent %q not found or missing id", name)
	return ""
}

func deleteAgent(t *testing.T, token, name string) {
	t.Helper()
	resp := testServer.Delete(t, "/agents/"+name, token)
	resp.Body.Close()
}

func containsName(items []map[string]any, name string) bool {
	for _, it := range items {
		if it["name"] == name {
			return true
		}
	}
	return false
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
