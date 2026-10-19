package transport

import (
	"strings"
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbgateway "github.com/hoophq/hoop/common/proto/gateway"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestListenClientMessagesExecVerb is the regression test for ENG-766.
//
// The gateway picks the access rule from the verb the client sends. A client
// that declares exec and then sends proxy packets got an interactive session
// past a jit-only rule. No agent is online here, so a packet that passes every
// gate ends at SendToAgent with ErrAgentOffline: that error proves the packet
// was forwarded, and PermissionDenied proves it was stopped before.
func TestListenClientMessagesExecVerb(t *testing.T) {
	previous := plugintypes.RegisteredPlugins
	plugintypes.RegisteredPlugins = nil
	t.Cleanup(func() { plugintypes.RegisteredPlugins = previous })

	for _, tt := range []struct {
		name          string
		verb          string
		origin        string
		impersonation string
		before        []string
		pktType       string
		wantRefused   bool
	}{
		{name: "postgres packet on exec stream", verb: pb.ClientVerbExec, pktType: pbagent.PGConnectionWrite, wantRefused: true},
		{name: "tcp packet on exec stream", verb: pb.ClientVerbExec, pktType: pbagent.TCPConnectionWrite, wantRefused: true},
		{name: "terminal packet on exec stream", verb: pb.ClientVerbExec, pktType: pbagent.TerminalWriteStdin, wantRefused: true},
		{name: "forged impersonation key", verb: pb.ClientVerbExec, impersonation: "forged", pktType: pbagent.PGConnectionWrite, wantRefused: true},
		// subscribeClient does not close an API-origin stream on return.
		{name: "postgres packet on api exec stream", verb: pb.ClientVerbExec, origin: pb.ConnectionOriginClientAPI, pktType: pbagent.PGConnectionWrite, wantRefused: true},
		{name: "exec stdin on exec stream", verb: pb.ClientVerbExec, pktType: pbagent.ExecWriteStdin},
		// hoop exec sends a KeepAlive first. The system packet filter must run before the gate.
		{name: "keepalive then exec stdin on exec stream", verb: pb.ClientVerbExec, before: []string{pbgateway.KeepAlive}, pktType: pbagent.ExecWriteStdin},
		{name: "session close on exec stream", verb: pb.ClientVerbExec, pktType: pbagent.SessionClose},
		{name: "postgres packet on connect stream", verb: pb.ClientVerbConnect, pktType: pbagent.PGConnectionWrite},
		{name: "gateway proxy on exec stream", verb: pb.ClientVerbExec, impersonation: grpckey.ImpersonateSecretKey, pktType: pbagent.TerminalWriteStdin},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sid := "sid-execverb-" + strings.ReplaceAll(tt.name, " ", "-")
			origin := tt.origin
			if origin == "" {
				origin = pb.ConnectionOriginClient
			}
			md := metadata.Pairs("session-id", sid, "verb", tt.verb, "origin", origin)
			if tt.impersonation != "" {
				md.Set(grpckey.ImpersonateAuthKeyHeaderKey, tt.impersonation)
			}
			pctx := &plugintypes.Context{
				Context:                  t.Context(),
				OrgID:                    "org-1",
				UserID:                   "user-1",
				ConnectionID:             "conn-1",
				ConnectionName:           "pg-prod",
				ConnectionType:           "database",
				ConnectionSubType:        "postgres",
				AgentID:                  "agent-1",
				ParamsData:               plugintypes.GenericMap{},
				ExtensionsOnDisconnectFn: func(string) {},
			}
			var recv []*pb.Packet
			for _, pktType := range append(tt.before, tt.pktType) {
				recv = append(recv, &pb.Packet{Type: pktType, Payload: []byte("payload")})
			}
			transport := &fakeConnectServer{ctx: metadata.NewIncomingContext(t.Context(), md), recv: recv}
			proxy := streamclient.NewProxy(pctx, transport)
			if err := proxy.Save(); err != nil {
				t.Fatalf("failed registering the proxy stream: %v", err)
			}
			t.Cleanup(func() { _ = proxy.Close(nil) })

			err := (&Server{}).listenClientMessages(proxy)

			if tt.wantRefused {
				if status.Code(err) != codes.PermissionDenied {
					t.Fatalf("want PermissionDenied before the agent, got %v", err)
				}
				if !strings.Contains(err.Error(), tt.pktType) {
					t.Errorf("the reason must name the packet type, got %q", err)
				}
				if streamclient.GetProxyStream(sid) != nil {
					t.Errorf("the refused session is still stored: the agent never gets SessionClose")
				}
				return
			}
			if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), pb.ErrAgentOffline.Error()) {
				t.Fatalf("want the packet forwarded to the agent (ErrAgentOffline), got %v", err)
			}
		})
	}
}

// SessionOpen is the first packet of every exec client. Past the gate it
// needs a database, so this test checks the gate itself.
func TestExecPacketsPassTheGate(t *testing.T) {
	for _, pktType := range []string{pbagent.SessionOpen, pbagent.ExecWriteStdin, pbagent.SessionClose} {
		if !isExecPacket(pktType) {
			t.Errorf("an exec client packet %v would close the session", pktType)
		}
	}
}
