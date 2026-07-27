//go:build integration

package integration

// The tests in this file mirror the MAIN OPERATIONS THE WEBAPP PERFORMS,
// exercising the same endpoints its service layer calls (webapp_v2/src/
// services and the legacy CLJS app). The resources-API lifecycle in
// particular follows how the UI creates infrastructure: a resource with
// roles, which the gateway materializes as connections (review feedback on
// the original suite — the bare POST /connections in smoke_test.go is the
// API/CLI path, this is the UI path).
//
// Same execution contract as smoke_test.go: serial, shared gateway + DB,
// uniquely named self-cleaned resources, response bodies closed exactly once
// at the call site.

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
)

// uniqueSeq disambiguates names created within the same nanosecond tick.
var uniqueSeq atomic.Int64

// uniqueName returns a per-run unique resource name so an interrupted run
// (cleanup never executed) cannot poison the next one with conflicts.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano()%1_000_000_000, uniqueSeq.Add(1))
}

// cleanupDelete is the deferred-cleanup counterpart of Delete: idempotent
// (404 is fine — the test already deleted the resource on its happy path)
// but LOUD on anything else, so a broken delete path cannot silently rot the
// shared-database suite.
func cleanupDelete(t *testing.T, path, token string) {
	t.Helper()
	resp := testServer.Delete(t, path, token)
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
	default:
		t.Errorf("cleanup %s: unexpected status %d (body: %s)",
			path, resp.StatusCode, testutil.ReadBody(t, resp))
	}
}

// T13 — the UI infrastructure-creation path: a resource with a role is
// created through POST /resources and materializes a connection; the
// documented deletion guard (resource with live connections → 403) is
// enforced; deleting the connection unblocks the resource deletion.
func TestResourceLifecycleUIStyle(t *testing.T) {
	token := adminToken(t)
	agentName := uniqueName("resource-ui-agent")
	agentID := createAgentReturningID(t, token, agentName)
	defer deleteAgent(t, token, agentName)

	resourceName := uniqueName("smoke-resource-ui")
	roleName := resourceName + "-role"

	// Create the resource the way the webapp does: type/subtype, env vars,
	// an agent, and one role that becomes a connection.
	created := testServer.Post(t, "/resources", token, openapi.ResourceRequest{
		Name:    resourceName,
		Type:    "database",
		SubType: "postgres",
		AgentID: agentID,
		EnvVars: map[string]string{
			"HOST": "127.0.0.1",
			"PORT": "5432",
		},
		Roles: []openapi.ResourceRoleRequest{{
			Name:    roleName,
			Type:    "database",
			SubType: "postgres",
			AgentID: agentID,
		}},
	})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)

	// Ensure cleanup even on assertion failures below. Order matters: the
	// resource cannot be deleted while its connection exists.
	defer cleanupDelete(t, "/resources/"+resourceName, token)
	defer cleanupDelete(t, "/connections/"+roleName, token)

	// Get by name.
	got := testServer.Get(t, "/resources/"+resourceName, token)
	defer got.Body.Close()
	testutil.RequireStatus(t, got, http.StatusOK)
	var resource map[string]any
	testutil.DecodeJSON(t, got, &resource)
	if resource["name"] != resourceName {
		t.Errorf("get resource: expected name %q, got %v", resourceName, resource["name"])
	}

	// Paginated listing contains it.
	list := testServer.Get(t, "/resources?page=1&page_size=50", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var paginated struct {
		Data []map[string]any `json:"data"`
	}
	testutil.DecodeJSON(t, list, &paginated)
	if !containsName(paginated.Data, resourceName) {
		t.Errorf("resource listing: %q not found after create", resourceName)
	}

	// The role materialized as a real connection, linked to the resource,
	// with the UI defaults (exec/connect/runbooks enabled, schema enabled
	// for databases).
	conn := testServer.Get(t, "/connections/"+roleName, token)
	defer conn.Body.Close()
	testutil.RequireStatus(t, conn, http.StatusOK)
	var connBody map[string]any
	testutil.DecodeJSON(t, conn, &connBody)
	if connBody["access_mode_exec"] != "enabled" || connBody["access_schema"] != "enabled" {
		t.Errorf("role connection: expected UI defaults enabled, got exec=%v schema=%v",
			connBody["access_mode_exec"], connBody["access_schema"])
	}

	// Duplicate resource name is a conflict.
	dup := testServer.Post(t, "/resources", token, openapi.ResourceRequest{
		Name:    resourceName,
		Type:    "database",
		SubType: "postgres",
		EnvVars: map[string]string{},
	})
	defer dup.Body.Close()
	if dup.StatusCode != http.StatusConflict {
		t.Errorf("duplicate resource: expected 409, got %d", dup.StatusCode)
	}

	// Deleting a resource that still has connections is forbidden.
	blocked := testServer.Delete(t, "/resources/"+resourceName, token)
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusForbidden {
		t.Errorf("delete resource with connections: expected 403, got %d (body: %s)",
			blocked.StatusCode, testutil.ReadBody(t, blocked))
	}

	// Delete the role connection, then the resource goes through.
	delConn := testServer.Delete(t, "/connections/"+roleName, token)
	defer delConn.Body.Close()
	if delConn.StatusCode != http.StatusOK && delConn.StatusCode != http.StatusNoContent {
		t.Fatalf("delete role connection: expected 200/204, got %d", delConn.StatusCode)
	}
	delRes := testServer.Delete(t, "/resources/"+resourceName, token)
	defer delRes.Body.Close()
	if delRes.StatusCode != http.StatusOK && delRes.StatusCode != http.StatusNoContent {
		t.Fatalf("delete resource: expected 200/204, got %d (body: %s)",
			delRes.StatusCode, testutil.ReadBody(t, delRes))
	}

	// Gone.
	gone := testServer.Get(t, "/resources/"+resourceName, token)
	defer gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("get deleted resource: expected 404, got %d", gone.StatusCode)
	}
}

// T14 — the sessions list (webapp landing page) returns the paginated
// envelope, and an unknown session id is a 404.
func TestSessionsListAndNotFound(t *testing.T) {
	token := adminToken(t)

	list := testServer.Get(t, "/sessions", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var body map[string]any
	testutil.DecodeJSON(t, list, &body)
	for _, field := range []string{"data", "total", "has_next_page"} {
		if _, ok := body[field]; !ok {
			t.Errorf("sessions list: missing %q field, got keys %v", field, keysOf(body))
		}
	}

	notFound := testServer.Get(t, "/sessions/00000000-0000-0000-0000-00000000dead", token)
	defer notFound.Body.Close()
	if notFound.StatusCode != http.StatusNotFound {
		t.Errorf("unknown session: expected 404, got %d", notFound.StatusCode)
	}
}

// T15 — guardrail rule CRUD (webapp Guardrails page).
func TestGuardrailCRUD(t *testing.T) {
	token := adminToken(t)
	ruleName := uniqueName("smoke-guardrail")

	guardrailBody := func(description string) openapi.GuardRailRuleRequest {
		return openapi.GuardRailRuleRequest{
			Name:        ruleName,
			Description: description,
			Input: map[string]any{
				"rules": []map[string]any{
					{"type": "deny_words_list", "words": []string{"DROP TABLE"}, "pattern_regex": ""},
				},
			},
			Output: map[string]any{"rules": []map[string]any{}},
		}
	}

	created := testServer.Post(t, "/guardrails", token, guardrailBody("deny drop table"))
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	var rule map[string]any
	testutil.DecodeJSON(t, created, &rule)
	ruleID, _ := rule["id"].(string)
	if ruleID == "" {
		t.Fatalf("guardrail create: missing id in response: %v", rule)
	}
	defer cleanupDelete(t, "/guardrails/"+ruleID, token)

	// List contains the created rule.
	list := testServer.Get(t, "/guardrails", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var rules []map[string]any
	testutil.DecodeJSON(t, list, &rules)
	if !containsName(rules, ruleName) {
		t.Errorf("guardrail listing: %q not found after create", ruleName)
	}

	// Get by id.
	got := testServer.Get(t, "/guardrails/"+ruleID, token)
	defer got.Body.Close()
	testutil.RequireStatus(t, got, http.StatusOK)

	// Update the description and verify it PERSISTED (a 200 that ignores
	// the body must fail here).
	updated := testServer.Put(t, "/guardrails/"+ruleID, token, guardrailBody("updated description"))
	defer updated.Body.Close()
	testutil.RequireStatus(t, updated, http.StatusOK)
	after := testServer.Get(t, "/guardrails/"+ruleID, token)
	defer after.Body.Close()
	testutil.RequireStatus(t, after, http.StatusOK)
	var afterBody map[string]any
	testutil.DecodeJSON(t, after, &afterBody)
	if afterBody["description"] != "updated description" {
		t.Errorf("guardrail update did not persist: description=%v", afterBody["description"])
	}

	// Delete and verify it is gone.
	del := testServer.Delete(t, "/guardrails/"+ruleID, token)
	defer del.Body.Close()
	if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete guardrail: expected 200/204, got %d", del.StatusCode)
	}
	gone := testServer.Get(t, "/guardrails/"+ruleID, token)
	defer gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("get deleted guardrail: expected 404, got %d", gone.StatusCode)
	}
}

// T16 — data-masking rule CRUD (webapp Data Masking page).
func TestDataMaskingRuleCRUD(t *testing.T) {
	token := adminToken(t)
	ruleName := uniqueName("smoke-masking-rule")

	score := 0.6
	maskingBody := func(description, entityType string) openapi.DataMaskingRuleRequest {
		return openapi.DataMaskingRuleRequest{
			Name:        ruleName,
			Description: description,
			SupportedEntityTypes: []openapi.SupportedEntityTypesEntry{{
				Name:        "PII",
				EntityTypes: []string{entityType},
			}},
			ScoreThreshold: &score,
		}
	}

	created := testServer.Post(t, "/datamasking-rules", token, maskingBody("mask emails", "EMAIL_ADDRESS"))
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	var rule map[string]any
	testutil.DecodeJSON(t, created, &rule)
	ruleID, _ := rule["id"].(string)
	if ruleID == "" {
		t.Fatalf("datamasking create: missing id in response: %v", rule)
	}
	defer cleanupDelete(t, "/datamasking-rules/"+ruleID, token)

	// List contains the created rule.
	list := testServer.Get(t, "/datamasking-rules", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var rules []map[string]any
	testutil.DecodeJSON(t, list, &rules)
	if !containsName(rules, ruleName) {
		t.Errorf("datamasking listing: %q not found after create", ruleName)
	}

	got := testServer.Get(t, "/datamasking-rules/"+ruleID, token)
	defer got.Body.Close()
	testutil.RequireStatus(t, got, http.StatusOK)

	// Update (staying within the OSS limit of one entity type per rule) and
	// verify the change persisted.
	updated := testServer.Put(t, "/datamasking-rules/"+ruleID, token, maskingBody("mask phones instead", "PHONE_NUMBER"))
	defer updated.Body.Close()
	testutil.RequireStatus(t, updated, http.StatusOK)
	after := testServer.Get(t, "/datamasking-rules/"+ruleID, token)
	defer after.Body.Close()
	testutil.RequireStatus(t, after, http.StatusOK)
	var afterBody map[string]any
	testutil.DecodeJSON(t, after, &afterBody)
	if afterBody["description"] != "mask phones instead" {
		t.Errorf("datamasking update did not persist: description=%v", afterBody["description"])
	}

	// The OSS license boundary is enforced: more than one entity type is
	// rejected with 403. (Enterprise lifts this; the suite runs OSS.)
	overLimit := testServer.Put(t, "/datamasking-rules/"+ruleID, token, openapi.DataMaskingRuleRequest{
		Name:        ruleName,
		Description: "two entity types",
		SupportedEntityTypes: []openapi.SupportedEntityTypesEntry{{
			Name:        "PII",
			EntityTypes: []string{"EMAIL_ADDRESS", "PHONE_NUMBER"},
		}},
		ScoreThreshold: &score,
	})
	defer overLimit.Body.Close()
	if overLimit.StatusCode != http.StatusForbidden {
		t.Errorf("datamasking OSS entity-type limit: expected 403, got %d", overLimit.StatusCode)
	}

	// Delete, gone.
	del := testServer.Delete(t, "/datamasking-rules/"+ruleID, token)
	defer del.Body.Close()
	if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete datamasking rule: expected 200/204, got %d", del.StatusCode)
	}
	gone := testServer.Get(t, "/datamasking-rules/"+ruleID, token)
	defer gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("get deleted datamasking rule: expected 404, got %d", gone.StatusCode)
	}
}

// T17 — user groups: the built-in admin group is listed; custom groups can
// be created and deleted (webapp Users page).
func TestUserGroups(t *testing.T) {
	token := adminToken(t)

	list := testServer.Get(t, "/users/groups", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var groups []string
	testutil.DecodeJSON(t, list, &groups)
	hasAdmin := false
	for _, g := range groups {
		if g == "admin" {
			hasAdmin = true
			break
		}
	}
	if !hasAdmin {
		t.Errorf("groups listing: built-in admin group not found in %v", groups)
	}

	groupName := uniqueName("smoke-custom-group")
	created := testServer.Post(t, "/users/groups", token, openapi.UserGroup{Name: groupName})
	defer created.Body.Close()
	testutil.RequireStatus(t, created, http.StatusCreated)
	defer cleanupDelete(t, "/users/groups/"+groupName, token)

	// Duplicate is a conflict.
	dup := testServer.Post(t, "/users/groups", token, openapi.UserGroup{Name: groupName})
	defer dup.Body.Close()
	if dup.StatusCode != http.StatusConflict {
		t.Errorf("duplicate group: expected 409, got %d", dup.StatusCode)
	}

	del := testServer.Delete(t, "/users/groups/"+groupName, token)
	defer del.Body.Close()
	if del.StatusCode != http.StatusOK && del.StatusCode != http.StatusNoContent {
		t.Errorf("delete group: expected 200/204, got %d", del.StatusCode)
	}
}

// T18 — default plugin contract: a new org materializes the default plugin
// set (audit among them — models.defaultPluginNames); PUT updates an
// EXISTING plugin (the webapp's enable-for-connections flow) and is NOT an
// upsert — an unknown plugin name is a 404.
//
// State safety: the PUT sends an empty connections list, which is exactly
// the default state of a fresh org's audit plugin, so this test mutates
// nothing observable by other tests.
func TestDefaultPluginsAndUpdateContract(t *testing.T) {
	token := adminToken(t)

	// The default set is materialized for the org.
	list := testServer.Get(t, "/plugins", token)
	defer list.Body.Close()
	testutil.RequireStatus(t, list, http.StatusOK)
	var plugins []map[string]any
	testutil.DecodeJSON(t, list, &plugins)
	if !containsName(plugins, "audit") {
		t.Errorf("plugin listing: default audit plugin not materialized for a new org")
	}

	// PUT on an existing (default) plugin succeeds. The handler requires
	// the connections field, even when empty — the webapp always sends it.
	put := testServer.Put(t, "/plugins/audit", token, map[string]any{
		"name":        "audit",
		"connections": []any{},
	})
	defer put.Body.Close()
	testutil.RequireStatus(t, put, http.StatusOK)

	got := testServer.Get(t, "/plugins/audit", token)
	defer got.Body.Close()
	testutil.RequireStatus(t, got, http.StatusOK)
	var plugin map[string]any
	testutil.DecodeJSON(t, got, &plugin)
	if plugin["name"] != "audit" {
		t.Errorf("get plugin: expected name audit, got %v", plugin["name"])
	}

	// PUT is not an upsert: unknown plugin name → 404.
	unknown := testServer.Put(t, "/plugins/not-a-plugin", token, map[string]any{
		"name":        "not-a-plugin",
		"connections": []any{},
	})
	defer unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound {
		t.Errorf("PUT unknown plugin: expected 404 (non-upsert contract), got %d", unknown.StatusCode)
	}
}

// T19 — reviews listing (webapp Reviews page) returns an array without
// error on an org with no reviews.
func TestReviewsListing(t *testing.T) {
	token := adminToken(t)
	resp := testServer.Get(t, "/reviews", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
}

// T20 — feature flag toggle round trip (webapp admin Feature Flags page):
// enable a flag from the catalog, verify the state is reflected, then
// restore it to its original state.
func TestFeatureFlagToggle(t *testing.T) {
	token := adminToken(t)

	flags := listFeatureFlags(t, token)
	if len(flags) == 0 {
		t.Fatal("feature-flags: empty catalog, nothing to toggle")
	}
	name, _ := flags[0]["name"].(string)
	original, _ := flags[0]["enabled"].(bool)

	// Restore is registered BEFORE the toggle: any assertion failure below
	// must not leave the shared org's flag flipped for the rest of the
	// suite (flags are org-global and cached in-process).
	defer func() {
		restore := testServer.Put(t, "/feature-flags/"+name, token,
			openapi.FeatureFlagUpdateRequest{Enabled: original})
		defer restore.Body.Close()
		testutil.RequireStatus(t, restore, http.StatusOK)
	}()

	// Toggle to the opposite state.
	put := testServer.Put(t, "/feature-flags/"+name, token,
		openapi.FeatureFlagUpdateRequest{Enabled: !original})
	defer put.Body.Close()
	testutil.RequireStatus(t, put, http.StatusOK)

	// The catalog reflects the new state — and the flag must still be in
	// the catalog at all (a vanishing entry is a regression, not a pass).
	found := false
	for _, f := range listFeatureFlags(t, token) {
		if f["name"] == name {
			found = true
			if got, _ := f["enabled"].(bool); got != !original {
				t.Errorf("flag %q after toggle: expected enabled=%v, got %v", name, !original, got)
			}
		}
	}
	if !found {
		t.Fatalf("flag %q disappeared from the catalog after toggle", name)
	}
}

// listFeatureFlags fetches and decodes the feature-flag catalog.
func listFeatureFlags(t *testing.T, token string) []map[string]any {
	t.Helper()
	resp := testServer.Get(t, "/feature-flags", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	var flags []map[string]any
	testutil.DecodeJSON(t, resp, &flags)
	return flags
}
