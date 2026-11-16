package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

const mcpPassthroughLane = `{"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://h/mcp","mcp_gateway":{"passthrough_header":"x-hoop-upstream-authorization"}}`

func TestMCPPassthroughHeaderValidation(t *testing.T) {
	const ok = `"name":"m","protocol":"mcp","listen":"127.0.0.1:1","upstream":"https://h/mcp"`
	for _, listener := range []string{
		mcpPassthroughLane,
		`{` + ok + `,"mcp_gateway":{"passthrough_header":"Authorization"}}`,
		`{` + ok + `,"mcp_gateway":{"transport":"streamable-http","passthrough_header":"X-GitHub-Token"}}`,
		`{"name":"m","protocol":"mcp","listen":"0.0.0.0:1","upstream":"https://h/mcp","caller_token":{"file":"/t","name":"a"},"mcp_gateway":{"passthrough_header":"x-up"}}`,
		`{` + ok + `,"identity_header":"x-user","mcp_gateway":{"passthrough_header":"x-up"}}`,
	} {
		if p := mcpProblems(t, listener); p != "" {
			t.Errorf("%s\n  refused: %s", listener, p)
		}
	}
	for _, tc := range []struct{ extra, header, want string }{
		{"", "x up", "is not a header name"},
		{"", " ", "is not a header name"},
		{"", "Host", "already uses"},
		{"", "Proxy-Authorization", "already uses"},
		{"", "cookie", "already uses"},
		{"", "Mcp-Session-Id", "already uses"},
		{"", "mcp-protocol-version", "already uses"},
		{"", "Last-Event-ID", "already uses"},
		{"", "Accept", "already uses"},
		{"", "Content-Type", "already uses"},
		{"", "Origin", "already uses"},
		{"", "User-Agent", "already uses"},
		{"", "X-Forwarded-For", "already uses"},
		{"", "Forwarded", "already uses"},
		{"", analyzer.HeaderReviewMode, "already uses"},
		{`,"caller_token":{"file":"/t","name":"a"}`, "X-Hoop-Token", "is the caller_token header"},
		{`,"caller_token":{"header":"x-agent","file":"/t","name":"a"}`, "X-Agent", "is the caller_token header"},
		{`,"identity_header":"x-user"`, "X-User", "is the identity_header"},
	} {
		listener := `{` + ok + tc.extra + `,"mcp_gateway":{"passthrough_header":"` + tc.header + `"}}`
		if p := mcpProblems(t, listener); !strings.Contains(p, "mcp_gateway.passthrough_header") || !strings.Contains(p, tc.want) {
			t.Errorf("%s\n  problems %q lack %q", listener, p, tc.want)
		}
	}
	p := mcpProblems(t, `{`+ok+`,"mcp_gateway":{"transport":"sse","passthrough_header":"x-up"}}`)
	if !strings.Contains(p, "mcp_gateway.passthrough_header needs transport streamable-http") {
		t.Errorf("sse: %q", p)
	}
}

// The plane serves mcp_gateway.passthrough_header only to a build that reads it.
func TestMCPPassthroughNeedsTheCapability(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"listeners":[` + mcpPassthroughLane + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	old := Handshake{Capabilities: []string{"protocol:mcp", "mcp_gateway", "address_url"}}
	if err := CheckServable(*cfg, old); err == nil || !strings.Contains(err.Error(), "mcp_passthrough") {
		t.Fatalf("a build without mcp_passthrough was served the field: %v", err)
	}
	if err := CheckServable(*cfg, Handshake{Capabilities: append(slices.Clone(old.Capabilities), "mcp_passthrough")}); err != nil {
		t.Fatalf("a build with mcp_passthrough was refused: %v", err)
	}
}
