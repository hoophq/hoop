package apiconnections

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

// mcpProxyCredentialFixture builds the inputs buildConnectionCredentialsResponse
// receives for a protocol-aware MCP connection created through the UI, which
// files it under the httpproxy parent beside the legacy "mcp" subtype.
func mcpProxyCredentialFixture() (*models.ConnectionCredentials, *models.Connection, *models.ServerMiscConfig) {
	cred := &models.ConnectionCredentials{
		ID:             "0f2f0f9c-2d0f-4d0a-9f1c-0f2f0f9c2d0f",
		ConnectionName: "linear-mcp",
		// Persisted as proto.ToConnectionType(type, subtype).String().
		ConnectionType: "mcpproxy",
		SessionID:      "3a1c9f6e-1d2b-4a5c-8e7f-3a1c9f6e1d2b",
	}
	conn := &models.Connection{
		Name:    "linear-mcp",
		Type:    "httpproxy",
		SubType: sql.NullString{String: "mcpproxy", Valid: true},
	}
	serverConf := &models.ServerMiscConfig{
		HttpProxyServerConfig: &models.HttpProxyServerConfig{ListenAddress: "0.0.0.0:18888"},
	}
	return cred, conn, serverConf
}

// An mcpproxy connection must yield usable credentials. Before the type had
// its own arm the switch fell through to `default: return nil`, and the
// handler answered 201 with a JSON `null` body — the user got no proxy token
// and no error explaining why.
func TestMcpProxyCredentialsResponseCarriesProxyToken(t *testing.T) {
	cred, conn, serverConf := mcpProxyCredentialFixture()
	const secretKey = "mcpproxy-Zm9vYmFyYmF6"

	resp := buildConnectionCredentialsResponse(cred, conn, serverConf, secretKey, false, "")
	if resp == nil {
		t.Fatal("mcpproxy credentials response is nil; the API would return a null body")
	}
	if resp.ConnectionType != "mcpproxy" {
		t.Fatalf("connection type = %q, want mcpproxy", resp.ConnectionType)
	}

	info, ok := resp.ConnectionCredentials.(*openapi.HttpProxyConnectionInfo)
	if !ok {
		t.Fatalf("credentials payload is %T, want *openapi.HttpProxyConnectionInfo", resp.ConnectionCredentials)
	}
	if info.ProxyToken != secretKey {
		t.Fatalf("proxy token = %q, want %q", info.ProxyToken, secretKey)
	}
	// The listener port must be resolved from the shared HTTP proxy config;
	// a missing arm in getServerHostAndPort leaves these empty.
	if info.Port != "18888" {
		t.Fatalf("port = %q, want 18888 (resolved from the http proxy listen address)", info.Port)
	}
	if info.Hostname == "" || info.Hostname == "0.0.0.0" {
		t.Fatalf("hostname = %q, want a dialable host", info.Hostname)
	}
}

// The agent's MCP gateway serves exactly one endpoint, "/mcp". Handing the
// user the httpproxy root or subdomain URL would produce a link that 404s, so
// the command payload must name the real endpoint.
func TestMcpProxyCredentialsAdvertiseMCPEndpoint(t *testing.T) {
	cred, conn, serverConf := mcpProxyCredentialFixture()
	const secretKey = "mcpproxy-Zm9vYmFyYmF6"

	resp := buildConnectionCredentialsResponse(cred, conn, serverConf, secretKey, false, "")
	info := resp.ConnectionCredentials.(*openapi.HttpProxyConnectionInfo)

	var commands map[string]string
	if err := json.Unmarshal([]byte(info.Command), &commands); err != nil {
		t.Fatalf("command payload is not valid JSON (%v): %s", err, info.Command)
	}
	endpoint, ok := commands["mcp"]
	if !ok {
		t.Fatalf("command payload has no mcp endpoint: %v", commands)
	}
	if !strings.HasSuffix(endpoint, "/mcp") {
		t.Fatalf("mcp endpoint = %q, want it to address the gateway's /mcp path", endpoint)
	}
	if !strings.Contains(endpoint, "18888") {
		t.Fatalf("mcp endpoint = %q, want the http proxy listener port", endpoint)
	}
	// The browser bootstrap URL embeds the secret in the path and only works
	// for cookie-based browsing; an MCP client authenticates by header.
	if _, ok := commands["browser"]; ok {
		t.Fatalf("mcpproxy must not advertise the browser bootstrap URL: %v", commands)
	}
	if !strings.Contains(commands["curl"], secretKey) {
		t.Fatalf("curl command must carry the proxy token: %q", commands["curl"])
	}
}

// A plain httpproxy connection keeps the browser/subdomain bootstrap it has
// always had. The mcpproxy arm was carved out of this switch, so a mistake
// there would silently strip those URLs from every existing connection.
func TestHttpProxyCredentialsKeepBrowserBootstrap(t *testing.T) {
	cred, conn, serverConf := mcpProxyCredentialFixture()
	cred.ConnectionType = "httpproxy"
	conn.SubType = sql.NullString{String: "httpproxy", Valid: true}
	const secretKey = "httpproxy-Zm9vYmFyYmF6"

	resp := buildConnectionCredentialsResponse(cred, conn, serverConf, secretKey, false, "")
	if resp == nil {
		t.Fatal("httpproxy credentials response is nil")
	}
	info := resp.ConnectionCredentials.(*openapi.HttpProxyConnectionInfo)

	var commands map[string]string
	if err := json.Unmarshal([]byte(info.Command), &commands); err != nil {
		t.Fatalf("command payload is not valid JSON (%v): %s", err, info.Command)
	}
	for _, key := range []string{"curl", "browser", "subdomain"} {
		if commands[key] == "" {
			t.Fatalf("httpproxy command payload lost the %q entry: %v", key, commands)
		}
	}
}
