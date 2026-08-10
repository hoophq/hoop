//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/integration/testutil"
)

// secretFieldNames are keys that must never appear anywhere in the
// /connection-credentials payload. The list covers the field names used by
// every per-protocol credential struct in openapi.ConnectionCredentialsResponse
// (Postgres/SSH/RDP/SSM/HttpProxy) plus the raw column names.
var secretFieldNames = []string{
	"connection_credentials",
	"password",
	"secret_key",
	"secret_key_hash",
	"encrypted_secret_key",
	"proxy_token",
	"connection_string",
	"aws_secret_access_key",
}

// mintPersistentCredential issues a persistent (no access_duration_seconds)
// credential for connName and returns the decoded response.
func mintPersistentCredential(t *testing.T, token, connName string) map[string]any {
	t.Helper()
	resp := testServer.Post(t, "/connections/"+connName+"/credentials", token,
		openapi.ConnectionCredentialsRequest{})
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusCreated)
	var out map[string]any
	testutil.DecodeJSON(t, resp, &out)
	return out
}

func listActiveCredentials(t *testing.T, token string) (map[string]any, string) {
	t.Helper()
	resp := testServer.Get(t, "/connection-credentials", token)
	defer resp.Body.Close()
	testutil.RequireStatus(t, resp, http.StatusOK)
	raw := testutil.ReadBody(t, resp)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("list credentials: invalid JSON %q: %v", raw, err)
	}
	return out, raw
}

func findCredentialItem(items []any, connName string) map[string]any {
	for _, it := range items {
		m, ok := it.(map[string]any)
		if ok && m["connection_name"] == connName {
			return m
		}
	}
	return nil
}

// TestListActiveConnectionCredentials covers the contract the Native
// Connections drawer depends on: the caller's active credentials are listed
// with enough state to paint a row, the payload carries no secrets, and a
// closed session drops the link while keeping the credential listed.
func TestListActiveConnectionCredentials(t *testing.T) {
	token := adminToken(t)
	agentID := createAgentReturningID(t, token, "cred-list-agent")
	defer deleteAgent(t, token, "cred-list-agent")

	const connName = "smoke-cred-list"
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
	defer func() {
		del := testServer.Delete(t, "/connections/"+connName, token)
		del.Body.Close()
	}()

	// Before minting anything, the collection must serialise as an empty array
	// rather than null — the frontend maps over it unconditionally.
	before, rawBefore := listActiveCredentials(t, token)
	itemsBefore, ok := before["items"].([]any)
	if !ok {
		t.Fatalf("list credentials: expected items to be an array, got %q", rawBefore)
	}
	if findCredentialItem(itemsBefore, connName) != nil {
		t.Fatalf("list credentials: %q listed before any credential was issued", connName)
	}

	minted := mintPersistentCredential(t, token, connName)
	credentialID, _ := minted["id"].(string)
	if credentialID == "" {
		t.Fatalf("mint credential: response carries no id: %v", minted)
	}

	after, rawAfter := listActiveCredentials(t, token)
	items, ok := after["items"].([]any)
	if !ok {
		t.Fatalf("list credentials: expected items to be an array, got %q", rawAfter)
	}
	item := findCredentialItem(items, connName)
	if item == nil {
		t.Fatalf("list credentials: %q missing after minting a credential (body: %s)", connName, rawAfter)
	}

	if item["id"] != credentialID {
		t.Errorf("list credentials: expected id %q, got %v", credentialID, item["id"])
	}
	if item["connection_type"] != "database" {
		t.Errorf("list credentials: expected connection_type %q, got %v", "database", item["connection_type"])
	}
	if item["connection_subtype"] != "postgres" {
		t.Errorf("list credentials: expected connection_subtype %q, got %v", "postgres", item["connection_subtype"])
	}
	if item["connection_id"] == nil || item["connection_id"] == "" {
		t.Errorf("list credentials: connection_id is empty, got %v", item["connection_id"])
	}
	// A persistent credential (minted without access_duration_seconds) reports a
	// null expiration; the drawer branches on this to pick the countdown pill
	// versus the steady "connected" indicator.
	if item["expire_at"] != nil {
		t.Errorf("list credentials: expected null expire_at for a persistent credential, got %v", item["expire_at"])
	}
	if sid, _ := item["session_id"].(string); sid == "" {
		t.Errorf("list credentials: expected a linked session_id, got %v", item["session_id"])
	}

	// The whole point of this endpoint: it must never disclose a secret.
	lowered := strings.ToLower(rawAfter)
	for _, field := range secretFieldNames {
		if strings.Contains(lowered, field) {
			t.Errorf("list credentials: payload leaks secret field %q (body: %s)", field, rawAfter)
		}
	}

	// Closing the session unlinks the audit session but keeps the credential
	// usable under the stable-key contract, so the row must stay listed.
	closed := testServer.Post(t, "/connections/"+connName+"/credentials/"+credentialID+"/close", token, nil)
	defer closed.Body.Close()
	if closed.StatusCode != http.StatusOK && closed.StatusCode != http.StatusNoContent {
		t.Fatalf("close credential session: expected 200/204, got %d (body: %s)",
			closed.StatusCode, testutil.ReadBody(t, closed))
	}

	afterClose, rawAfterClose := listActiveCredentials(t, token)
	itemsAfterClose, _ := afterClose["items"].([]any)
	itemAfterClose := findCredentialItem(itemsAfterClose, connName)
	if itemAfterClose == nil {
		t.Fatalf("list credentials: %q dropped after closing its session (body: %s)", connName, rawAfterClose)
	}
	if sid, _ := itemAfterClose["session_id"].(string); sid != "" {
		t.Errorf("list credentials: expected session_id to be cleared after close, got %q", sid)
	}

	// Revoking removes it from the collection entirely.
	revoked := testServer.Post(t, "/connections/"+connName+"/credentials/"+credentialID+"/revoke", token, nil)
	defer revoked.Body.Close()
	if revoked.StatusCode != http.StatusOK && revoked.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke credential: expected 200/204, got %d (body: %s)",
			revoked.StatusCode, testutil.ReadBody(t, revoked))
	}

	afterRevoke, rawAfterRevoke := listActiveCredentials(t, token)
	itemsAfterRevoke, _ := afterRevoke["items"].([]any)
	if findCredentialItem(itemsAfterRevoke, connName) != nil {
		t.Errorf("list credentials: %q still listed after revoke (body: %s)", connName, rawAfterRevoke)
	}
}
