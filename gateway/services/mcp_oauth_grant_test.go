package services

import (
	"encoding/base64"
	"strings"
	"testing"
)

func envs(remoteURL string) map[string]string {
	if remoteURL == "" {
		return map[string]string{}
	}
	return map[string]string{mcpProxyEndpointEnvKey: base64.StdEncoding.EncodeToString([]byte(remoteURL))}
}

// Adoption is the moment a login's refresh token becomes durable, and nothing
// else binds the flow to the connection: the admin authorizes against one
// server, edits the URL field, and saves — the flow id in the payload still
// points at the server that was authorized. Adopting it would attach server
// A's credential to a connection that talks to server B and auto-renew it
// forever, which is credential exfiltration performed by editing a form field.
func TestGrantAdoptionRefusesForeignEndpoint(t *testing.T) {
	err := checkGrantEndpointMatch("https://a.example.com/mcp", envs("https://b.example.com/mcp"))
	if err == nil {
		t.Fatal("adoption accepted a login for a different MCP server")
	}
	if !strings.Contains(err.Error(), "a.example.com") || !strings.Contains(err.Error(), "b.example.com") {
		t.Fatalf("error does not name both endpoints: %v", err)
	}
}

// A connection with no endpoint configured cannot be matched against anything,
// so there is nothing to validate the flow against and adoption is refused.
func TestGrantAdoptionRefusesEndpointlessConnection(t *testing.T) {
	if err := checkGrantEndpointMatch("https://a.example.com/mcp", envs("")); err == nil {
		t.Fatal("adoption accepted a login for a connection with no MCP endpoint")
	}
}

// The admin types the URL once and it comes back through several layers, so
// the differences that cannot change which server is reached must not fail an
// honest save.
func TestGrantAdoptionAcceptsEquivalentEndpoints(t *testing.T) {
	for _, tc := range []struct{ name, flow, conn string }{
		{"identical", "https://mcp.linear.app/mcp", "https://mcp.linear.app/mcp"},
		{"trailing slash", "https://mcp.linear.app/mcp/", "https://mcp.linear.app/mcp"},
		{"host case", "https://MCP.Linear.App/mcp", "https://mcp.linear.app/mcp"},
		{"default port", "https://mcp.linear.app:443/mcp", "https://mcp.linear.app/mcp"},
		{"query and fragment", "https://mcp.linear.app/mcp?x=1#f", "https://mcp.linear.app/mcp"},
		{"surrounding space", " https://mcp.linear.app/mcp ", "https://mcp.linear.app/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkGrantEndpointMatch(tc.flow, envs(tc.conn)); err != nil {
				t.Fatalf("adoption refused an equivalent endpoint: %v", err)
			}
		})
	}
}

// Everything that decides which server the bytes reach stays significant. A
// non-default port and a different path are different endpoints even when the
// host matches, and a scheme downgrade is how the credential would end up in
// cleartext.
func TestGrantAdoptionRefusesSignificantEndpointDifferences(t *testing.T) {
	for _, tc := range []struct{ name, flow, conn string }{
		{"path", "https://mcp.linear.app/mcp", "https://mcp.linear.app/other"},
		{"port", "https://mcp.linear.app:8443/mcp", "https://mcp.linear.app/mcp"},
		{"scheme", "https://mcp.linear.app/mcp", "http://mcp.linear.app/mcp"},
		{"subdomain", "https://mcp.linear.app/mcp", "https://evil.mcp.linear.app/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkGrantEndpointMatch(tc.flow, envs(tc.conn)); err == nil {
				t.Fatalf("adoption accepted a login for a different endpoint (%s vs %s)", tc.flow, tc.conn)
			}
		})
	}
}
