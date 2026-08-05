package controller

import (
	"encoding/base64"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

// The webapp's MCP Gateway form writes lowercase credential keys, which
// helpers/config->json upper-cases into connection env vars. These fixtures
// are the literal output of that transform, captured by running the real
// ClojureScript (process-role-secret) against a filled-in form:
//
//	webapp $ node emit-out/emit.js
//
// They exist so a rename on either side fails here instead of silently
// producing a connection the agent ignores. If the webapp form changes,
// re-capture rather than hand-editing.
var (
	webappRemoteFormEnvs = map[string]string{
		"MCP_TRANSPORT":        "streamable-http",
		"REMOTE_URL":           "https://mcp.linear.app/mcp",
		"MCP_AUTH":             "static",
		"MCP_DENIED_TOOLS":     "delete_*",
		"MCP_MAX_CALLS":        "50",
		"INSECURE":             "false",
		"HEADER_AUTHORIZATION": "Bearer tok",
	}
	webappStdioFormEnvs = map[string]string{
		"MCP_TRANSPORT":      "stdio",
		"MCP_ON_RUG_PULL":    "alert",
		"MCPENV_FIGMA_TOKEN": "sk-1",
	}
	// The client-stdio form emits the same shape as stdio: the command goes
	// into the connection's command array, never an env var, so the only
	// difference on the wire is the transport value itself.
	webappClientStdioFormEnvs = map[string]string{
		"MCP_TRANSPORT":       "client-stdio",
		"MCP_DENIED_TOOLS":    "delete_*",
		"MCPENV_GITHUB_TOKEN": "ghp-1",
	}
)

func encodeEnvs(envs map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range envs {
		out["envvar:"+k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	return out
}

// A remote MCP connection created through the webapp must parse into exactly
// the settings the form expressed. A key the agent does not read is not an
// error anywhere — it is silently ignored — so this asserts the values landed.
func TestWebappRemoteFormParses(t *testing.T) {
	env, err := parseConnectionEnvVars(encodeEnvs(webappRemoteFormEnvs), pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("webapp remote form rejected by the agent: %v", err)
	}
	if env.mcpTransport != "streamable-http" {
		t.Fatalf("transport = %q, want streamable-http", env.mcpTransport)
	}
	if env.httpProxyRemoteURL != "https://mcp.linear.app/mcp" {
		t.Fatalf("remote url = %q", env.httpProxyRemoteURL)
	}
	if env.mcpAuth != "static" {
		t.Fatalf("auth = %q, want static", env.mcpAuth)
	}
	if len(env.mcpDeniedTools) != 1 || env.mcpDeniedTools[0] != "delete_*" {
		t.Fatalf("denied tools = %v, want [delete_*]", env.mcpDeniedTools)
	}
	if env.mcpMaxCalls != 50 {
		t.Fatalf("max calls = %d, want 50", env.mcpMaxCalls)
	}

	// The form's Authorization header must reach the backend under its real
	// HTTP name, not the HEADER_-prefixed env var name.
	headers := mcpBackendHeaders(env.httpProxyHeaders)
	if headers["AUTHORIZATION"] != "Bearer tok" {
		t.Fatalf("backend headers = %v, want AUTHORIZATION set", headers)
	}
}

// A stdio connection created through the webapp must parse, and its child
// environment must arrive with the MCPENV_ carve-out stripped: the form writes
// MCPENV_FIGMA_TOKEN so it is distinguishable from a connection setting, but
// the subprocess expects FIGMA_TOKEN.
func TestWebappStdioFormParses(t *testing.T) {
	env, err := parseConnectionEnvVars(encodeEnvs(webappStdioFormEnvs), pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("webapp stdio form rejected by the agent: %v", err)
	}
	if env.mcpTransport != "stdio" {
		t.Fatalf("transport = %q, want stdio", env.mcpTransport)
	}
	if env.mcpOnRugPull != "alert" {
		t.Fatalf("on rug pull = %q, want alert", env.mcpOnRugPull)
	}
	if got := env.mcpEnv["FIGMA_TOKEN"]; got != "sk-1" {
		t.Fatalf("child env = %v, want FIGMA_TOKEN=sk-1", env.mcpEnv)
	}
	// A stdio backend has no URL; the form drops it rather than sending a
	// stale value from a previous transport selection.
	if env.httpProxyRemoteURL != "" {
		t.Fatalf("stdio connection carries a remote url: %q", env.httpProxyRemoteURL)
	}
}

// Every value the form's dropdowns can produce must be one the agent accepts.
// The form offers three transports and two rug-pull modes; if validation grows
// stricter without the form narrowing, a user could save an unusable
// connection.
func TestWebappFormOptionsAreAllAccepted(t *testing.T) {
	for _, transport := range []string{"stdio", "streamable-http", "sse"} {
		envs := map[string]string{"MCP_TRANSPORT": transport}
		if transport != "stdio" {
			envs["REMOTE_URL"] = "https://example.com/mcp"
		}
		if _, err := parseConnectionEnvVars(encodeEnvs(envs), pb.ConnectionTypeMcpProxy); err != nil {
			t.Fatalf("form offers transport %q but the agent rejects it: %v", transport, err)
		}
	}
	for _, mode := range []string{"kill", "alert"} {
		envs := map[string]string{"MCP_TRANSPORT": "stdio", "MCP_ON_RUG_PULL": mode}
		if _, err := parseConnectionEnvVars(encodeEnvs(envs), pb.ConnectionTypeMcpProxy); err != nil {
			t.Fatalf("form offers rug-pull mode %q but the agent rejects it: %v", mode, err)
		}
	}
	// The catalog picker maps every non-"none" entry (18 of 32 are oauth) to
	// "static", because hoop brokers OAuth itself. Both must be accepted.
	for _, auth := range []string{"none", "static"} {
		envs := map[string]string{"MCP_TRANSPORT": "stdio", "MCP_AUTH": auth}
		if _, err := parseConnectionEnvVars(encodeEnvs(envs), pb.ConnectionTypeMcpProxy); err != nil {
			t.Fatalf("picker can emit auth %q but the agent rejects it: %v", auth, err)
		}
	}
}

// A static-auth catalog server sends its key under the header that provider
// documents, which is often not "Authorization". These are the literal env
// vars the webapp emits after picking context7 and google-maps, captured by
// running the real ClojureScript. The header name must survive to the wire
// byte for byte — a rewritten name authenticates as nobody.
func TestWebappStaticTokenHeadersReachBackendVerbatim(t *testing.T) {
	for _, tt := range []struct {
		server string
		envs   map[string]string
		header string
		value  string
	}{
		{
			server: "context7",
			envs: map[string]string{
				"MCP_TRANSPORT":           "streamable-http",
				"MCP_AUTH":                "static",
				"REMOTE_URL":              "https://mcp.context7.com/mcp",
				"HEADER_CONTEXT7_API_KEY": "ctx-secret-value",
			},
			header: "CONTEXT7_API_KEY",
			value:  "ctx-secret-value",
		},
		{
			server: "google-maps",
			envs: map[string]string{
				"MCP_TRANSPORT":         "streamable-http",
				"MCP_AUTH":              "static",
				"REMOTE_URL":            "https://maps.example/mcp",
				"HEADER_X-Goog-Api-Key": "gmaps-secret",
			},
			header: "X-Goog-Api-Key",
			value:  "gmaps-secret",
		},
	} {
		t.Run(tt.server, func(t *testing.T) {
			env, err := parseConnectionEnvVars(encodeEnvs(tt.envs), pb.ConnectionTypeMcpProxy)
			if err != nil {
				t.Fatalf("%s connection rejected: %v", tt.server, err)
			}
			headers := mcpBackendHeaders(env.httpProxyHeaders)
			if got := headers[tt.header]; got != tt.value {
				t.Fatalf("%s: header %q = %q, want %q (full: %v)",
					tt.server, tt.header, got, tt.value, headers)
			}
		})
	}
}

// A client-stdio connection built in the webapp must reach the agent intact.
// The transport string is the whole switch: a mismatch between the form's
// value and the agent's constant routes the connection to a local child on
// the agent host, which is the exact behaviour this transport exists to
// avoid — and it would look like it worked.
func TestWebappClientStdioFormParses(t *testing.T) {
	env, err := parseConnectionEnvVars(encodeEnvs(webappClientStdioFormEnvs), pb.ConnectionTypeMcpProxy)
	if err != nil {
		t.Fatalf("webapp client-stdio form rejected by the agent: %v", err)
	}
	if env.mcpTransport != mcpTransportClientStdio {
		t.Fatalf("transport = %q, want %q", env.mcpTransport, mcpTransportClientStdio)
	}
	// No REMOTE_URL is required, unlike the HTTP transports.
	if env.httpProxyRemoteURL != "" {
		t.Fatalf("remote url = %q, want empty for a stdio transport", env.httpProxyRemoteURL)
	}
	// MCPENV_* must land in the child's environment with the prefix stripped;
	// this is how a client-hosted server receives its tokens.
	if got := env.mcpEnv["GITHUB_TOKEN"]; got != "ghp-1" {
		t.Fatalf("child env GITHUB_TOKEN = %q, want ghp-1 (env = %v)", got, env.mcpEnv)
	}
	if len(env.mcpDeniedTools) != 1 || env.mcpDeniedTools[0] != "delete_*" {
		t.Fatalf("denied tools = %v, want [delete_*]", env.mcpDeniedTools)
	}
}
