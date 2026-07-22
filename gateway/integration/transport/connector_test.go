//go:build integration

package transport

import (
	"context"
	"time"

	commongrpc "github.com/hoophq/hoop/common/grpc"
	pb "github.com/hoophq/hoop/common/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Connector is the transport-agnostic seam the scenarios are written against.
// It exposes the four transport operations a client or agent performs against
// the gateway — the unary PreConnect/HealthCheck RPCs and the two bidirectional
// Connect stream variants (agent-origin and client-origin) — without leaking
// the concrete wire type.
//
// gRPC is the only implementation today (grpcConnector). When the WebSocket
// transport lands (DEP-40/41) it implements this same interface and is added
// to transports(), so every scenario runs against both wires unchanged. This
// is what makes the suite a behavioral-parity baseline rather than a
// gRPC-specific test.
//
// The seam is defined over pb.ClientTransport — the same packet-stream
// abstraction the agent controller already consumes — so it is "wire-agnostic
// over the Packet protocol", not protocol-neutral. That is exactly the contract
// the WebSocket transport must satisfy anyway (it has to implement
// pb.ClientTransport to plug into the controller), so reusing it here is
// deliberate, not a leak.
type Connector interface {
	// Name identifies the wire, used as the subtest name.
	Name() string
	// HealthCheck performs the unary HealthCheck RPC.
	HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error)
	// PreConnect performs the unary PreConnect RPC authenticated as the agent
	// identified by token (a DSN).
	PreConnect(ctx context.Context, token string, req *pb.PreConnectRequest) (*pb.PreConnectResponse, error)
	// DialAgent opens an agent-origin Connect stream authenticated with the
	// given DSN token. The caller owns the returned transport and must Close it.
	DialAgent(ctx context.Context, token string) (pb.ClientTransport, error)
	// DialClient opens a client-origin Connect stream for the connection, verb
	// and access token in cfg. The caller owns the returned transport.
	DialClient(ctx context.Context, cfg ClientDialConfig) (pb.ClientTransport, error)
}

// ClientDialConfig carries the parameters a client sets when opening a
// Connect stream: the user access token, the target connection name, and the
// client verb (connect/exec/...).
type ClientDialConfig struct {
	Token          string
	ConnectionName string
	Verb           string
}

// grpcConnector implements Connector over the production gRPC dialers in
// common/grpc, targeting the harness's ephemeral gateway address. It is the
// reference wire the WebSocket transport is validated against.
type grpcConnector struct {
	addr string
}

func newGRPCConnector(addr string) *grpcConnector { return &grpcConnector{addr: addr} }

func (c *grpcConnector) Name() string { return "grpc" }

func (c *grpcConnector) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	// HealthCheck is not wrapped by common/grpc (agents/clients never call it),
	// so dial directly. Plaintext loopback matches the harness listener.
	conn, err := grpc.DialContext(ctx, c.addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return pb.NewTransportClient(conn).HealthCheck(ctx, req)
}

func (c *grpcConnector) PreConnect(_ context.Context, token string, req *pb.PreConnectRequest) (*pb.PreConnectResponse, error) {
	return commongrpc.PreConnectRPC(c.clientConfig(token), req)
}

func (c *grpcConnector) DialAgent(_ context.Context, token string) (pb.ClientTransport, error) {
	// Advertise the native MSSQL guardrails capability, matching a current agent
	// linked against a libhoop that enforces it (the harness links the real
	// libhoop). Tests that need to simulate an older, incapable agent use
	// dialAgentWithoutCapabilities instead.
	return commongrpc.Connect(c.clientConfig(token),
		commongrpc.WithOption("origin", pb.ConnectionOriginAgent),
		commongrpc.WithOption(commongrpc.OptionKey(pb.GRPCMetaAgentCapabilities), pb.AgentCapabilityMSSQLGuardRails))
}

// dialAgentWithoutCapabilities dials an agent stream that advertises no
// capabilities, standing in for an agent older than capability advertisement.
func (c *grpcConnector) dialAgentWithoutCapabilities(token string) (pb.ClientTransport, error) {
	return commongrpc.Connect(c.clientConfig(token),
		commongrpc.WithOption("origin", pb.ConnectionOriginAgent))
}

func (c *grpcConnector) DialClient(_ context.Context, cfg ClientDialConfig) (pb.ClientTransport, error) {
	return commongrpc.Connect(c.clientConfig(cfg.Token),
		commongrpc.WithOption(commongrpc.OptionConnectionName, cfg.ConnectionName),
		commongrpc.WithOption("origin", pb.ConnectionOriginClient),
		commongrpc.WithOption("verb", cfg.Verb))
}

// clientConfig builds the plaintext-loopback ClientConfig every gRPC dial in
// this harness shares. The context on Dial* is unused here because
// common/grpc manages its own dial timeout; it is retained on the interface
// for transports (WebSocket) that honor it.
func (c *grpcConnector) clientConfig(token string) commongrpc.ClientConfig {
	return commongrpc.ClientConfig{
		ServerAddress: c.addr,
		Token:         token,
		UserAgent:     "hoop-transport-itest",
		Insecure:      true,
	}
}

// transports returns every wire implementation the scenarios run against.
// Today that is gRPC only; appending a WebSocket Connector here is the single
// change that makes the whole suite validate the new transport for parity.
func transports() []Connector {
	return []Connector{newGRPCConnector(gw.GRPCAddr)}
}

// recvUntil reads packets from tr until one of the wanted types arrives, the
// timeout expires, or the stream errors. It returns the matching packet. This
// is the transport-agnostic primitive the scenarios use to assert on the
// packet stream regardless of the underlying wire.
//
// Recv has no deadline, so a background goroutine performs the blocking reads.
// On timeout the transport is closed to unblock that goroutine deterministically
// (rather than leaking it): every caller treats a recvUntil timeout as fatal, so
// closing the stream here is safe. The result channel is buffered so a late
// delivery after the timeout never blocks the goroutine.
func recvUntil(tr pb.ClientTransport, timeout time.Duration, wanted ...string) (*pb.Packet, error) {
	want := make(map[string]struct{}, len(wanted))
	for _, w := range wanted {
		want[w] = struct{}{}
	}
	type result struct {
		pkt *pb.Packet
		err error
	}
	resc := make(chan result, 1)
	go func() {
		for {
			pkt, err := tr.Recv()
			if err != nil {
				resc <- result{nil, err}
				return
			}
			if _, ok := want[pkt.Type]; ok {
				resc <- result{pkt, nil}
				return
			}
		}
	}()
	select {
	case r := <-resc:
		return r.pkt, r.err
	case <-time.After(timeout):
		_, _ = tr.Close()
		return nil, context.DeadlineExceeded
	}
}
