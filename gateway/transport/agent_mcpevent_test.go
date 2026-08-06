package transport

import (
	"context"
	"fmt"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/models"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// fakeConnectServer is a pb.Transport_ConnectServer that replays a scripted
// list of packets and records everything written back. It stands in for both
// halves of a session: the agent side feeds recv, the proxy side collects sent.
type fakeConnectServer struct {
	grpc.ServerStream
	ctx  context.Context
	recv []*pb.Packet
	sent []*pb.Packet
}

func (f *fakeConnectServer) Context() context.Context { return f.ctx }

func (f *fakeConnectServer) Send(pkt *pb.Packet) error {
	f.sent = append(f.sent, pkt)
	return nil
}

func (f *fakeConnectServer) Recv() (*pb.Packet, error) {
	if len(f.recv) == 0 {
		// Not a gRPC status error, so listenAgentMessages treats it as a
		// stream failure and returns instead of looping forever.
		return nil, fmt.Errorf("no more packets")
	}
	pkt := f.recv[0]
	f.recv = f.recv[1:]
	return pkt, nil
}

// TestListenAgentMessagesDropsMCPEventPackets is the regression test for MCP
// audit records leaking into a client's response stream.
//
// An MCPProxyConnectionWrite tagged with SpecMCPEventKey is one JSON audit
// line, not response bytes, and its spec is copied from the request that
// produced it — so it carries a live SpecClientConnectionID and routes like a
// real response. Every client proxy reads from this one forward path, so a
// filter installed in only one of them (the gateway's httpproxy listener) let
// `hoop connect` write audit JSON straight into an MCP client's keep-alive
// body and wreck its HTTP framing.
//
// The drop therefore has to happen here, at the single fan-out point.
func TestListenAgentMessagesDropsMCPEventPackets(t *testing.T) {
	const sid = "sid-mcp-event"

	// No plugins registered: loadRuntimePlugins then touches no database, so
	// the transport loop runs for real without one.
	previous := plugintypes.RegisteredPlugins
	plugintypes.RegisteredPlugins = nil
	t.Cleanup(func() { plugintypes.RegisteredPlugins = previous })

	pctx := &plugintypes.Context{
		SID:                      sid,
		OrgID:                    "org-1",
		UserID:                   "user-1",
		ConnectionID:             "conn-1",
		ConnectionName:           "mcp-linear",
		ConnectionType:           "application",
		AgentID:                  "agent-1",
		ClientVerb:               pb.ClientVerbConnect,
		ClientOrigin:             pb.ConnectionOriginClient,
		ParamsData:               plugintypes.GenericMap{},
		ExtensionsOnDisconnectFn: func(string) {},
	}

	proxyTransport := &fakeConnectServer{ctx: metadata.NewIncomingContext(
		t.Context(), metadata.Pairs("session-id", sid, "verb", pb.ClientVerbConnect, "origin", pb.ConnectionOriginClient))}
	proxy := streamclient.NewProxy(pctx, proxyTransport)
	if err := proxy.Save(); err != nil {
		t.Fatalf("failed registering the proxy stream: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Close(nil) })

	// Same spec on both packets: the event record is a copy of the request's,
	// which is exactly why the marker is the only thing that can tell them
	// apart at this point.
	spec := func(extra map[string][]byte) map[string][]byte {
		s := map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sid),
			pb.SpecClientConnectionID: []byte("7"),
		}
		for k, v := range extra {
			s[k] = v
		}
		return s
	}

	agentTransport := &fakeConnectServer{
		ctx: metadata.NewIncomingContext(t.Context(), metadata.Pairs("connection-name", "mcp-linear")),
		recv: []*pb.Packet{
			{
				Type:    pbclient.MCPProxyConnectionWrite,
				Spec:    spec(map[string][]byte{pb.SpecMCPEventKey: []byte("1")}),
				Payload: []byte(`{"event":"tool_call","tool":"create_issue"}` + "\n"),
			},
			{
				Type:    pbclient.MCPProxyConnectionWrite,
				Spec:    spec(nil),
				Payload: []byte("HTTP/1.1 200 OK\r\n\r\n"),
			},
		},
	}
	agent := streamclient.NewAgent(models.Agent{ID: "agent-1", OrgID: "org-1", Name: "agent-1"}, agentTransport)

	// Returns once the fake agent stream runs out of packets.
	_ = (&Server{}).listenAgentMessages(pctx, agent)

	if len(proxyTransport.sent) != 1 {
		t.Fatalf("proxy received %d packets, want only the response packet: %+v", len(proxyTransport.sent), proxyTransport.sent)
	}
	forwarded := proxyTransport.sent[0]
	if len(forwarded.Spec[pb.SpecMCPEventKey]) > 0 {
		t.Fatalf("an MCP audit event reached the client proxy stream: %s", forwarded.Payload)
	}
	if got, want := string(forwarded.Payload), "HTTP/1.1 200 OK\r\n\r\n"; got != want {
		t.Fatalf("forwarded payload = %q, want the response bytes %q", got, want)
	}
}
