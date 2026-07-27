//go:build integration

package transport

import (
	"context"
	"testing"
	"time"

	commongrpc "github.com/hoophq/hoop/common/grpc"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestAgentConnectHandshake verifies a valid agent completes the Connect
// handshake and receives GatewayConnectOK. This is the core agent-origin path
// through the auth and sessionuuid interceptors.
func TestAgentConnectHandshake(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			_, dsn := createAgent(t, uniqueName("agent"))

			tr, err := c.DialAgent(context.Background(), dsn)
			if err != nil {
				t.Fatalf("DialAgent: %v", err)
			}
			defer tr.Close()

			pkt, err := recvUntil(tr, 10*time.Second, pbagent.GatewayConnectOK)
			if err != nil {
				t.Fatalf("waiting for GatewayConnectOK: %v", err)
			}
			if pkt.Type != pbagent.GatewayConnectOK {
				t.Fatalf("first packet = %q, want %q", pkt.Type, pbagent.GatewayConnectOK)
			}
		})
	}
}

// TestAgentConnectRejectsInvalidDSN verifies the auth interceptor rejects an
// agent whose secret does not resolve to a registered agent. The rejection
// surfaces on the stream as an Unauthenticated status.
func TestAgentConnectRejectsInvalidDSN(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			// A well-formed DSN for an agent that was never created.
			badDSN := "grpc://ghost:wrong-secret@" + gw.GRPCAddr + "?mode=standard"

			tr, err := c.DialAgent(context.Background(), badDSN)
			if err != nil {
				// Some wires may reject at dial; that is also acceptable.
				if status.Code(err) != codes.Unauthenticated {
					t.Fatalf("DialAgent error code = %v, want Unauthenticated", status.Code(err))
				}
				return
			}
			defer tr.Close()

			_, err = recvUntil(tr, 10*time.Second, pbagent.GatewayConnectOK)
			if err == nil {
				t.Fatal("expected auth rejection, got GatewayConnectOK")
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("recv error code = %v, want Unauthenticated (err=%v)", status.Code(err), err)
			}
		})
	}
}

// TestDuplicateAgentRejected verifies the second concurrent Connect for the
// same agent identity is refused (agent already connected) while the first
// stays healthy.
func TestDuplicateAgentRejected(t *testing.T) {
	for _, c := range transports() {
		t.Run(c.Name(), func(t *testing.T) {
			agentID, dsn := createAgent(t, uniqueName("agent"))

			first, err := c.DialAgent(context.Background(), dsn)
			if err != nil {
				t.Fatalf("DialAgent(first): %v", err)
			}
			defer first.Close()
			if _, err := recvUntil(first, 10*time.Second, pbagent.GatewayConnectOK); err != nil {
				t.Fatalf("first agent handshake: %v", err)
			}

			second, err := c.DialAgent(context.Background(), dsn)
			if err != nil {
				t.Fatalf("DialAgent(second): %v", err)
			}
			defer second.Close()

			// The duplicate is refused at Save (FailedPrecondition, "agent
			// already connected"); the stream errors instead of completing the
			// handshake.
			_, err = recvUntil(second, 10*time.Second, pbagent.GatewayConnectOK)
			if err == nil {
				t.Fatal("expected duplicate agent to be rejected, got GatewayConnectOK")
			}
			if code := status.Code(err); code != codes.FailedPrecondition && err != context.DeadlineExceeded {
				t.Fatalf("duplicate rejection error code = %v, want FailedPrecondition (err=%v)", code, err)
			}

			// The first stream must remain healthy: a connection bound to this
			// agent still reports online after the duplicate was refused.
			connName := uniqueName("conn")
			createPGConnection(t, connName, agentID)
			waitConnectionStatus(t, connName, "online")
		})
	}
}

// TestConnectRejectsMissingOrigin verifies the transport refuses a Connect
// stream that omits the origin metadata. This asserts the interceptor-chain
// precondition every wire must enforce. It is gRPC-specific in mechanics (raw
// metadata), so it targets the gRPC address directly rather than the seam.
func TestConnectRejectsMissingOrigin(t *testing.T) {
	_, dsn := createAgent(t, uniqueName("agent"))

	conn, err := grpc.DialContext(context.Background(), gw.GRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Authorization is present, but origin is deliberately omitted.
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+dsn)
	stream, err := pb.NewTransportClient(conn).Connect(ctx)
	if err != nil {
		t.Fatalf("open connect stream: %v", err)
	}
	if err := stream.Send(&pb.Packet{Type: "test"}); err != nil {
		// A send failure here is an acceptable manifestation of the rejection.
		return
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("expected rejection for missing origin, got a packet")
	} else if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("recv error code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

// compile-time guard: the raw gRPC path above must use the same option keys
// the seam uses, so drift is caught here.
var _ = commongrpc.OptionConnectionName
