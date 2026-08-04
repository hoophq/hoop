package httpproxy

import (
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// A session with no explicit packet types is a plain httpproxy relay. Getting
// this wrong would silently route every existing connection's traffic to the
// agent's MCP adapter.
func TestSessionPacketTypesDefaultToHttpProxy(t *testing.T) {
	sess := &httpProxySession{}
	if got := sess.agentWriteType(); got != pbagent.HttpProxyConnectionWrite {
		t.Fatalf("agent write type = %q, want %q", got, pbagent.HttpProxyConnectionWrite)
	}
	if got := sess.clientWriteType(); got != pbclient.HttpProxyConnectionWrite {
		t.Fatalf("client write type = %q, want %q", got, pbclient.HttpProxyConnectionWrite)
	}
}

// A protocol-aware MCP session overrides both directions so the agent
// dispatches to the MCP adapter instead of the byte relay.
func TestSessionPacketTypesMCPOverride(t *testing.T) {
	sess := &httpProxySession{
		agentWritePacketType:  pbagent.MCPProxyConnectionWrite,
		clientWritePacketType: pbclient.MCPProxyConnectionWrite,
	}
	if got := sess.agentWriteType(); got != pbagent.MCPProxyConnectionWrite {
		t.Fatalf("agent write type = %q, want %q", got, pbagent.MCPProxyConnectionWrite)
	}
	if got := sess.clientWriteType(); got != pbclient.MCPProxyConnectionWrite {
		t.Fatalf("client write type = %q, want %q", got, pbclient.MCPProxyConnectionWrite)
	}
}

// A protocol-aware MCP session can park a tool call on a human reviewer, so
// the gateway must wait at least as long as the agent will hold that call.
// A gateway that gives up first answers a bare 504 and orphans a call the
// agent is still holding, which is what a hardcoded five minutes did the
// moment reviews were enabled on an MCP connection.
func TestResponseWaitCoversTheAgentHeldCallBudget(t *testing.T) {
	mcp := &httpProxySession{
		agentWritePacketType:  pbagent.MCPProxyConnectionWrite,
		clientWritePacketType: pbclient.MCPProxyConnectionWrite,
	}
	if got := mcp.responseWaitTimeout(); got <= pb.MCPHeldCallBudget {
		t.Fatalf("mcp response wait = %v, want more than the agent's held-call budget %v", got, pb.MCPHeldCallBudget)
	}

	// The byte relay never parks on a human, so it keeps the machine timeout.
	relay := &httpProxySession{}
	if got := relay.responseWaitTimeout(); got != httpProxyResponseWait {
		t.Fatalf("httpproxy response wait = %v, want %v", got, httpProxyResponseWait)
	}
}

// The two MCP packet types must differ from their httpproxy counterparts;
// colliding values would route MCP traffic through the opaque relay.
func TestMCPPacketTypesAreDistinct(t *testing.T) {
	if pbagent.MCPProxyConnectionWrite == pbagent.HttpProxyConnectionWrite {
		t.Fatal("agent MCP packet type collides with httpproxy")
	}
	if pbclient.MCPProxyConnectionWrite == pbclient.HttpProxyConnectionWrite {
		t.Fatal("client MCP packet type collides with httpproxy")
	}
}

// The mcpproxy connection type must resolve from its subtype under every
// parent type the UI can file it under, and must not collide with the legacy
// "mcp" httpproxy alias.
//
// The httpproxy parent is the load-bearing case: every MCP surface in the
// webapp files connections there (that is where the legacy "mcp" subtype
// lives), and the parent used to short-circuit before reading the subtype.
// A regression is silent — the session resolves to the byte relay, the agent
// never reaches its MCP adapter, and no policy or audit event is produced.
func TestMcpProxyConnectionTypeResolution(t *testing.T) {
	for _, parent := range []string{"application", "custom", "httpproxy"} {
		if got := pb.ToConnectionType(parent, "mcpproxy"); got != pb.ConnectionTypeMcpProxy {
			t.Fatalf("ToConnectionType(%q, mcpproxy) = %q, want %q", parent, got, pb.ConnectionTypeMcpProxy)
		}
	}
	// The legacy alias must keep resolving to httpproxy: ADR-0004 leaves it
	// untouched so the new path carries zero regression risk.
	if got := pb.ToConnectionType("httpproxy", "mcp"); got != pb.ConnectionTypeHttpProxy {
		t.Fatalf("legacy mcp subtype = %q, want %q", got, pb.ConnectionTypeHttpProxy)
	}
	// A plain httpproxy connection (no subtype, or any other subtype) must
	// keep taking the byte relay.
	for _, subtype := range []string{"", "httpproxy", "grafana"} {
		if got := pb.ToConnectionType("httpproxy", subtype); got != pb.ConnectionTypeHttpProxy {
			t.Fatalf("ToConnectionType(httpproxy, %q) = %q, want %q", subtype, got, pb.ConnectionTypeHttpProxy)
		}
	}
}
