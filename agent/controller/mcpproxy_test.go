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
			// OAuth is brokered by the gateway, which resolves the result into
			// HEADER_AUTHORIZATION before the session opens. Taking it here
			// would hand mcpproxy's outbound stack a backend with no
			// credential: silently unauthenticated.
			name:    "oauth auth is refused; the gateway resolves it into a header",
			envs:    map[string]string{"MCP_TRANSPORT": "stdio", "MCP_AUTH": "oauth"},
			wantErr: "MCP_AUTH",
		},
		{
			name: "passthrough auth is accepted on a remote transport",
			envs: map[string]string{
				"MCP_TRANSPORT": "streamable-http",
				"REMOTE_URL":    "https://api.githubcopilot.com/mcp",
				"MCP_AUTH":      "passthrough",
			},
		},
		{
			// A stdio child authenticates through its own environment and
			// never sees an HTTP header, so there is nothing to substitute
			// the caller's credential into. Accepting it would run the child
			// on its configured MCPENV_* while the admin believes each user
			// authenticates as themselves.
			name:    "passthrough auth is refused on stdio",
			envs:    map[string]string{"MCP_TRANSPORT": "stdio", "MCP_AUTH": "passthrough"},
			wantErr: "requires a remote transport",
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
		{
			// The reported bug. Anything non-empty that was not recognised as
			// true used to parse as an explicit false, and an explicit false
			// is the ONLY thing that opens these gates (mcpproxy
			// checks.boolOrTrue treats nil as block). A typo therefore
			// disabled the protection with no error anywhere.
			name:    "misspelled block sampling is rejected, not read as false",
			envs:    map[string]string{"MCP_TRANSPORT": "stdio", "MCP_BLOCK_SAMPLING": "flase"},
			wantErr: "invalid MCP_BLOCK_SAMPLING",
		},
		{
			name:    "misspelled block elicitation is rejected, not read as false",
			envs:    map[string]string{"MCP_TRANSPORT": "stdio", "MCP_BLOCK_ELICITATION": "disabled"},
			wantErr: "invalid MCP_BLOCK_ELICITATION",
		},
		{
			// "no" is a plausible hand-written value and a real false, so it
			// must opt out rather than fail the connection.
			name: "spelled-out false values are accepted",
			envs: map[string]string{
				"MCP_TRANSPORT":         "stdio",
				"MCP_BLOCK_SAMPLING":    "no",
				"MCP_BLOCK_ELICITATION": "off",
			},
		},
		{
			name: "surrounding whitespace and case do not matter",
			envs: map[string]string{
				"MCP_TRANSPORT":         "stdio",
				"MCP_BLOCK_SAMPLING":    "  False ",
				"MCP_BLOCK_ELICITATION": "TRUE",
			},
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

// What the connection settings resolve to in the policy the gateway enforces.
//
// The nil/false distinction is the whole point: mcpproxy resolves nil to
// "block" (checks.boolOrTrue) and only an explicit false opens the gate, which
// lets an MCP server ask the user's own client to run inference or prompt for
// input on its behalf. A parser that turned a typo into false silently gave
// away exactly that.
func TestMcpPolicyBlockGatesFailClosed(t *testing.T) {
	tests := []struct {
		name                  string
		envs                  map[string]string
		sampling, elicitation *bool
	}{
		{
			name:     "unset leaves both nil so the library blocks",
			envs:     map[string]string{"MCP_TRANSPORT": "stdio"},
			sampling: nil, elicitation: nil,
		},
		{
			name: "explicit false is the only opt-out",
			envs: map[string]string{
				"MCP_TRANSPORT":         "stdio",
				"MCP_BLOCK_SAMPLING":    "false",
				"MCP_BLOCK_ELICITATION": "false",
			},
			sampling: new(false), elicitation: new(false),
		},
		{
			name: "explicit true blocks",
			envs: map[string]string{
				"MCP_TRANSPORT":         "stdio",
				"MCP_BLOCK_SAMPLING":    "true",
				"MCP_BLOCK_ELICITATION": "1",
			},
			sampling: new(true), elicitation: new(true),
		},
		{
			name: "one toggle set does not disturb the other",
			envs: map[string]string{
				"MCP_TRANSPORT":      "stdio",
				"MCP_BLOCK_SAMPLING": "false",
			},
			sampling: new(false), elicitation: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := parseConnectionEnvVars(mcpProxyEnvVars(tt.envs), pb.ConnectionTypeMcpProxy)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			policy := env.mcpPolicy()
			assertGate(t, "BlockSampling", policy.BlockSampling, tt.sampling)
			assertGate(t, "BlockElicitation", policy.BlockElicitation, tt.elicitation)
		})
	}
}

// The two toggles are validated only for mcpproxy connections, because only
// mcpPolicy reads them. A database connection that happens to carry a stray
// MCP_* var — copied between connections, left behind by a template — must
// still open: failing a postgres session over an MCP setting nothing consumes
// would turn a safety check into an outage.
func TestMalformedMcpToggleDoesNotFailOtherConnectionTypes(t *testing.T) {
	envs := mcpProxyEnvVars(map[string]string{
		"HOST":               "127.0.0.1",
		"USER":               "app",
		"PASS":               "secret",
		"DB":                 "app",
		"MCP_BLOCK_SAMPLING": "flase",
	})
	if _, err := parseConnectionEnvVars(envs, pb.ConnectionTypePostgres); err != nil {
		t.Fatalf("a postgres connection was rejected over an unused MCP setting: %v", err)
	}
}

// assertGate compares a tri-state policy gate, keeping nil ("secure default")
// distinct from a pointer to false ("explicitly opened").
func assertGate(t *testing.T, name string, got, want *bool) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Fatalf("%s = %s, want %s", name, gateString(got), gateString(want))
	case *got != *want:
		t.Fatalf("%s = %s, want %s", name, gateString(got), gateString(want))
	}
}

func gateString(v *bool) string {
	if v == nil {
		return "nil (library blocks)"
	}
	if *v {
		return "true (blocked)"
	}
	return "false (OPEN)"
}

// The env store preserves the original key, so headers arrive prefixed. The
// MCP backends call req.Header.Set verbatim: without stripping, the upstream
// receives "HEADER_AUTHORIZATION" and no "Authorization" at all, breaking
// every static and frozen-token OAuth backend.
//
// Everything after the prefix must survive byte for byte. Catalog providers
// disagree on the separator — context7 requires CONTEXT7_API_KEY, google-maps
// requires X-Goog-Api-Key — and a rewritten name authenticates as nobody.
func TestMcpBackendHeadersStripsOnlyThePrefix(t *testing.T) {
	got := mcpBackendHeaders(map[string]string{
		"HEADER_Authorization":    "Bearer token-value",
		"HEADER_CONTEXT7_API_KEY": "  ctx-secret  ",
		"HEADER_X-Goog-Api-Key":   "gmaps-secret",
	})
	want := map[string]string{
		"Authorization": "Bearer token-value",
		// Underscores are legal in a header name and this provider requires
		// them; rewriting to CONTEXT7-API-KEY would break authentication.
		"CONTEXT7_API_KEY": "ctx-secret",
		"X-Goog-Api-Key":   "gmaps-secret",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d headers, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("header %q = %q, want %q (full: %v)", k, got[k], v, got)
		}
	}
	for k := range got {
		if strings.HasPrefix(strings.ToLower(k), "header_") {
			t.Fatalf("prefixed key survived stripping: %v", got)
		}
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
