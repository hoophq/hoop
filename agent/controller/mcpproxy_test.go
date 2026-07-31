package controller

import (
	"encoding/base64"
	"strings"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

// mcpProxyEnvVars builds the envvar map shape term.NewEnvVarStore expects for
// a protocol-aware MCP connection.
func mcpProxyEnvVars(extra map[string]string) map[string]any {
	envs := map[string]any{}
	for k, v := range extra {
		envs["envvar:"+k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return envs
}

// A misconfigured MCP connection must fail at parse time. Otherwise the error
// surfaces from mcpproxy's backend factory on the first tool call, as an
// opaque session close long after the admin saved the connection.
func TestParseConnectionEnvVarsMcpProxyValidation(t *testing.T) {
	tests := []struct {
		name    string
		envs    map[string]string
		wantErr string
	}{
		{
			name: "stdio needs no remote url",
			envs: map[string]string{"MCP_TRANSPORT": "stdio"},
		},
		{
			name: "streamable-http with remote url",
			envs: map[string]string{"MCP_TRANSPORT": "streamable-http", "REMOTE_URL": "https://mcp.linear.app/mcp"},
		},
		{
			name: "sse with remote url",
			envs: map[string]string{"MCP_TRANSPORT": "sse", "REMOTE_URL": "https://mcp.atlassian.com/v1/sse"},
		},
		{
			name:    "streamable-http without remote url",
			envs:    map[string]string{"MCP_TRANSPORT": "streamable-http"},
			wantErr: "REMOTE_URL",
		},
		{
			name:    "sse without remote url",
			envs:    map[string]string{"MCP_TRANSPORT": "sse"},
			wantErr: "REMOTE_URL",
		},
		{
			name:    "missing transport",
			envs:    map[string]string{"REMOTE_URL": "https://mcp.linear.app/mcp"},
			wantErr: "MCP_TRANSPORT",
		},
		{
			name:    "unknown transport",
			envs:    map[string]string{"MCP_TRANSPORT": "websocket"},
			wantErr: "invalid MCP_TRANSPORT",
		},
		{
			// The agent supplies no outbound TokenSource, so these modes would
			// silently produce an unauthenticated backend rather than failing.
			name:    "oauth auth is refused until the token store lands",
			envs:    map[string]string{"MCP_TRANSPORT": "stdio", "MCP_AUTH": "oauth"},
			wantErr: "MCP_AUTH",
		},
		{
			name:    "passthrough auth is refused",
			envs:    map[string]string{"MCP_TRANSPORT": "stdio", "MCP_AUTH": "passthrough"},
			wantErr: "MCP_AUTH",
		},
		{
			name: "static auth is accepted",
			envs: map[string]string{"MCP_TRANSPORT": "stdio", "MCP_AUTH": "static"},
		},
		{
			name:    "unknown rug pull mode",
			envs:    map[string]string{"MCP_TRANSPORT": "stdio", "MCP_ON_RUG_PULL": "ignore"},
			wantErr: "MCP_ON_RUG_PULL",
		},
		{
			name: "alert rug pull mode is accepted",
			envs: map[string]string{"MCP_TRANSPORT": "stdio", "MCP_ON_RUG_PULL": "alert"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConnectionEnvVars(mcpProxyEnvVars(tt.envs), pb.ConnectionTypeMcpProxy)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected an error mentioning %q, got none", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// The env store preserves the original key, so headers arrive prefixed. The
// MCP backends call req.Header.Set verbatim: without normalization the
// upstream receives "HEADER_AUTHORIZATION" and no "Authorization" at all,
// breaking every static and frozen-token OAuth backend.
func TestMcpBackendHeadersNormalizesEnvKeys(t *testing.T) {
	got := mcpBackendHeaders(map[string]string{
		"HEADER_AUTHORIZATION": "Bearer token-value",
		"HEADER_X_API_KEY":     "  sk-secret  ",
	})
	want := map[string]string{
		"AUTHORIZATION": "Bearer token-value",
		// Header names may not contain underscores on the wire.
		"X-API-KEY": "sk-secret",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d headers, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("header %q = %q, want %q (full: %v)", k, got[k], v, got)
		}
	}
	if _, ok := got["HEADER_AUTHORIZATION"]; ok {
		t.Fatalf("prefixed key survived normalization: %v", got)
	}
}

// No configured headers must yield no header map rather than an empty one, so
// the backend config stays indistinguishable from "nothing was set".
func TestMcpBackendHeadersEmptyIsNil(t *testing.T) {
	if got := mcpBackendHeaders(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := mcpBackendHeaders(map[string]string{}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}
