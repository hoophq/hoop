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

// The MCP path multiplexes response bytes and structured protocol events on
// one packet type. Only the spec key separates them, and forwarding an event
// into the response stream corrupts the client's HTTP framing — so the marker
// must be a distinct, non-empty spec value.
func TestMCPEventSpecKeyDistinguishesPayloads(t *testing.T) {
	responsePkt := &pb.Packet{
		Type: pbclient.MCPProxyConnectionWrite,
		Spec: map[string][]byte{pb.SpecClientConnectionID: []byte("1")},
	}
	if len(responsePkt.Spec[pb.SpecMCPEventKey]) != 0 {
		t.Fatal("a response packet must not carry the MCP event marker")
	}

	eventPkt := &pb.Packet{
		Type: pbclient.MCPProxyConnectionWrite,
		Spec: map[string][]byte{
			pb.SpecClientConnectionID: []byte("1"),
			pb.SpecMCPEventKey:        []byte("1"),
		},
	}
	if len(eventPkt.Spec[pb.SpecMCPEventKey]) == 0 {
		t.Fatal("an event packet must carry the MCP event marker")
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

// The mcpproxy connection type must resolve from its subtype under both parent
// types, and must not collide with the legacy "mcp" httpproxy alias.
func TestMcpProxyConnectionTypeResolution(t *testing.T) {
	for _, parent := range []string{"application", "custom"} {
		if got := pb.ToConnectionType(parent, "mcpproxy"); got != pb.ConnectionTypeMcpProxy {
			t.Fatalf("ToConnectionType(%q, mcpproxy) = %q, want %q", parent, got, pb.ConnectionTypeMcpProxy)
		}
	}
	// The legacy alias must keep resolving to httpproxy: ADR-0004 leaves it
	// untouched so the new path carries zero regression risk.
	if got := pb.ToConnectionType("httpproxy", "mcp"); got != pb.ConnectionTypeHttpProxy {
		t.Fatalf("legacy mcp subtype = %q, want %q", got, pb.ConnectionTypeHttpProxy)
	}
}
