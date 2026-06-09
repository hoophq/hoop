// Package client implements the per-flow gRPC pipe that backs every TCP
// connection accepted by the tunnel's userspace netstack.
//
// Architecture:
//
//	gVisor TCP forwarder accepts a connection inside the user's
//	tunnel address space (e.g. fd...:pg-prod:443). For each accepted
//	connection, DialAndPipe is called with:
//	  - the local net.Conn (one end of the user's TCP flow)
//	  - the connection name (resolved from the destination IP)
//	  - the gateway's gRPC ClientConfig
//	  - the JIT timeout (passed as SpecJitTimeout on the SessionOpen)
//
//	DialAndPipe then:
//	  1. Opens a NEW bidirectional gRPC stream to the gateway with the
//	     "connection-name" metadata header set. The gateway's auth +
//	     plugin pipeline treats this stream as a plain `hoop connect`
//	     session.
//	  2. Sends pbagent.SessionOpen and waits for pbclient.SessionOpenOK.
//	  3. Sends the initial pbagent.TCPConnectionWrite with
//	     SpecTCPServerConnectKey to ask the agent to open its upstream
//	     TCP socket.
//	  4. Pumps bytes in both directions until either side closes:
//	      local -> gateway via pbagent.TCPConnectionWrite packets
//	      gateway -> local via pbclient.TCPConnectionWrite packets
//	  5. On any termination, sends pbagent.TCPConnectionClose and tears
//	     down the gRPC stream.
//
// Each call to DialAndPipe creates its own gRPC stream. There is no
// connection pooling or stream multiplexing: one TCP flow == one gRPC
// stream. The gateway already handles thousands of concurrent client
// streams; we lean on that rather than reinventing the wheel.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// connectionIDOnPipe is the static client-side connection id we use on
// every pipe. Because each gRPC stream backs exactly one TCP flow, there
// is no need to disambiguate multiple flows within a single stream — the
// agent keys its connection store by sessionID:connectionID, and both are
// unique to this pipe.
const connectionIDOnPipe = "1"

// PipeOptions is everything DialAndPipe needs that isn't the bytes
// themselves. All fields are required unless otherwise stated.
type PipeOptions struct {
	// GatewayConfig is the gRPC client config for the gateway. The
	// caller must populate ServerAddress, Token, and TLS fields exactly
	// as the `hoop connect` CLI would.
	GatewayConfig grpc.ClientConfig

	// ConnectionName is the hoop connection (e.g. "pg-prod") that the
	// gateway should route this stream to. Sent both as a gRPC metadata
	// header and as the verb spec on SessionOpen.
	ConnectionName string

	// CorrelationID is an optional opaque ID the caller may set so logs
	// on the gateway can be tied back to a single tunnel session.
	CorrelationID string

	// UserAgent is sent on the gRPC dial.
	UserAgent string

	// SessionOpenTimeout is how long to wait for SessionOpenOK after
	// sending SessionOpen. Defaults to 30s.
	SessionOpenTimeout time.Duration
}

// Dialer opens a fresh gRPC bidirectional stream to the gateway. It is
// abstracted so tests can supply an in-memory transport. Production code
// uses dialGateway.
type Dialer func(opts PipeOptions) (pb.ClientTransport, error)

// dialGateway is the production Dialer: it calls common/grpc.Connect with
// the connection-name / origin / verb / correlation-id metadata headers
// the gateway's auth interceptor expects.
func dialGateway(opts PipeOptions) (pb.ClientTransport, error) {
	grpcOpts := []*grpc.ClientOptions{
		grpc.WithOption(grpc.OptionConnectionName, opts.ConnectionName),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
	}
	if opts.CorrelationID != "" {
		grpcOpts = append(grpcOpts, grpc.WithOption("correlation-id", opts.CorrelationID))
	}

	cfg := opts.GatewayConfig
	if opts.UserAgent != "" {
		cfg.UserAgent = opts.UserAgent
	}
	return grpc.Connect(cfg, grpcOpts...)
}

// DialAndPipe opens a gRPC stream to the gateway, performs the session
// open handshake, and pumps bytes between local and the agent's upstream
// TCP socket until either side closes.
//
// It blocks until the pipe terminates. The local net.Conn is NOT closed
// by DialAndPipe (the caller owns it).
//
// On any error before the byte pump starts (dial failed, session open
// rejected, etc.), it returns immediately with the error and no bytes
// have been written to local.
func DialAndPipe(ctx context.Context, local io.ReadWriteCloser, opts PipeOptions) error {
	return dialAndPipeWith(ctx, local, opts, dialGateway)
}

// dialAndPipeWith is DialAndPipe parameterized by Dialer for testing.
func dialAndPipeWith(ctx context.Context, local io.ReadWriteCloser, opts PipeOptions, dial Dialer) error {
	if opts.ConnectionName == "" {
		return errors.New("client.DialAndPipe: ConnectionName is required")
	}
	if opts.SessionOpenTimeout == 0 {
		opts.SessionOpenTimeout = 30 * time.Second
	}

	transport, err := dial(opts)
	if err != nil {
		return fmt.Errorf("dial gateway: %w", err)
	}
	defer func() {
		// transport.Close returns (streamCloseErr, connCloseErr); we
		// don't care about either on the teardown path.
		_, _ = transport.Close()
	}()

	return runPipe(ctx, transport, local, opts)
}

// runPipe drives the SessionOpen handshake and the byte pump on an
// already-open transport. Exported (within the package) so tests can
// drive it with a mocked transport.
func runPipe(ctx context.Context, transport pb.ClientTransport, local io.ReadWriteCloser, opts PipeOptions) error {
	// Step 1: ask the gateway to open a session for this connection.
	if err := transport.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{},
	}); err != nil {
		return fmt.Errorf("send SessionOpen: %w", err)
	}

	// Step 2: wait for SessionOpenOK (or a terminal failure packet).
	sessionID, connType, err := awaitSessionOpen(ctx, transport, opts.SessionOpenTimeout)
	if err != nil {
		return err
	}

	// We only support TCP-style protocols on the tunnel. SSH, terminals,
	// kubernetes/http proxies, etc. need protocol-specific clients and
	// have no place in a transparent IP tunnel.
	if !isTunnelableType(connType) {
		return fmt.Errorf("connection %q has type %q which is not tunnelable; supported: postgres, mysql, mssql, mongodb, tcp",
			opts.ConnectionName, connType)
	}

	log.With("connection", opts.ConnectionName, "session", sessionID, "type", connType).
		Debugf("tunnel pipe established")

	// Protocol-aware piping: for Postgres we speak the PG wire protocol to
	// the agent (pbagent.PGConnectionWrite) so the agent injects the real
	// upstream credentials via libhoop. The user may authenticate with any
	// user/password (e.g. hoop/hoop) — it is discarded by the agent. All
	// other tunnelable types still use the transparent raw-TCP pump.
	switch pb.ConnectionType(connType) {
	case pb.ConnectionTypePostgres:
		return pumpPostgres(ctx, transport, local, sessionID)
	default:
		return pumpBytes(ctx, transport, local, sessionID)
	}
}

// awaitSessionOpen reads packets from the transport until either a
// terminal session-open response arrives or the timeout fires.
//
// We must NOT leave a goroutine recv-looping after we return: subsequent
// reads in pumpBytes need to see every packet the gateway sends. So each
// iteration spawns a fresh single-shot goroutine; if the timeout/ctx
// fires we abandon that goroutine but it will exit on the next Recv
// (typically EOF when the transport is torn down).
func awaitSessionOpen(ctx context.Context, transport pb.ClientTransport, timeout time.Duration) (sessionID, connType string, err error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	type recvResult struct {
		pkt *pb.Packet
		err error
	}
	for {
		ch := make(chan recvResult, 1)
		go func() {
			pkt, err := transport.Recv()
			ch <- recvResult{pkt, err}
		}()

		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-deadline.C:
			return "", "", fmt.Errorf("timeout waiting for SessionOpenOK after %s", timeout)
		case r := <-ch:
			if r.err != nil {
				return "", "", fmt.Errorf("recv during session open: %w", r.err)
			}
			if r.pkt == nil {
				continue
			}
			switch pb.PacketType(r.pkt.Type) {
			case pbclient.SessionOpenOK:
				sid := string(r.pkt.Spec[pb.SpecGatewaySessionID])
				if sid == "" {
					return "", "", errors.New("SessionOpenOK without session id")
				}
				return sid, string(r.pkt.Spec[pb.SpecConnectionType]), nil
			case pbclient.SessionOpenWaitingApproval:
				// In a tunnel context, JIT review prompts on a per-flow
				// basis are not usable: there is no user-facing UI tied
				// to this individual TCP connection. We fail fast and let
				// the user request access out-of-band.
				return "", "", fmt.Errorf("connection requires review: %s", string(r.pkt.Payload))
			case pbclient.SessionOpenTimeout:
				return "", "", fmt.Errorf("session open timeout: %s", string(r.pkt.Payload))
			case pbclient.SessionOpenAgentOffline:
				return "", "", errors.New("agent is offline")
			case pbclient.SessionClose:
				msg := string(r.pkt.Payload)
				if msg == "" {
					msg = "session closed before open"
				}
				return "", "", errors.New(msg)
			}
			// Any other packet type before SessionOpenOK is unexpected.
			// Drop it and keep waiting.
		}
	}
}

// pumpBytes runs the bidirectional byte pump. It returns when either
// direction terminates.
func pumpBytes(ctx context.Context, transport pb.ClientTransport, local io.ReadWriteCloser, sessionID string) error {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID:   []byte(sessionID),
		pb.SpecClientConnectionID: []byte(connectionIDOnPipe),
	}

	// Tell the agent to open its upstream socket. The TCPServerConnectKey
	// spec marks this as a no-op write so the agent does not forward an
	// empty payload to the database.
	openSpec := make(map[string][]byte, len(spec)+1)
	for k, v := range spec {
		openSpec[k] = v
	}
	openSpec[pb.SpecTCPServerConnectKey] = nil
	if err := transport.Send(&pb.Packet{
		Type: pbagent.TCPConnectionWrite,
		Spec: openSpec,
	}); err != nil {
		return fmt.Errorf("send TCPConnectionWrite open: %w", err)
	}

	transport.StartKeepAlive()

	// Track who finished first so we know what error (if any) to surface.
	var (
		once    sync.Once
		exitErr error
	)
	finish := func(err error) {
		once.Do(func() { exitErr = err })
	}

	pumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// local -> gateway
	go func() {
		defer wg.Done()
		writer := pb.NewStreamWriter(transport, pbagent.TCPConnectionWrite, spec)
		_, err := io.Copy(writer, local)
		// Tell the agent to close its upstream socket. We send this
		// regardless of how io.Copy ended (EOF, error, or peer close
		// canceled our context): the gateway needs a definitive signal
		// that the client side is done. If the gateway already closed
		// the stream, the Send is a harmless no-op.
		_ = transport.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: spec,
		})
		if err != nil && !isClosedConnErr(err) {
			finish(fmt.Errorf("local->gateway: %w", err))
		}
		cancel()
	}()

	// gateway -> local
	go func() {
		defer wg.Done()
		err := readFromGateway(pumpCtx, transport, local)
		if err != nil && !errors.Is(err, io.EOF) && !isClosedConnErr(err) {
			finish(fmt.Errorf("gateway->local: %w", err))
		}
		// Closing the local conn unblocks the local->gateway io.Copy
		// when the gateway side died first.
		_ = local.Close()
		cancel()
	}()

	wg.Wait()
	return exitErr
}

// readFromGateway loops on Recv() and writes packet payloads to local.
// It returns when the stream ends or a non-recoverable packet arrives.
func readFromGateway(ctx context.Context, transport pb.ClientTransport, local io.Writer) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pkt, err := transport.Recv()
		if err != nil {
			return err
		}
		if pkt == nil {
			continue
		}
		switch pb.PacketType(pkt.Type) {
		case pbclient.TCPConnectionWrite:
			if len(pkt.Payload) == 0 {
				continue
			}
			if _, werr := local.Write(pkt.Payload); werr != nil {
				return werr
			}
		case pbclient.TCPConnectionClose:
			// Agent half-closed; we're done reading. Returning io.EOF
			// is the standard signal for "remote closed cleanly".
			return io.EOF
		case pbclient.SessionClose:
			msg := string(pkt.Payload)
			if msg == "" {
				return io.EOF
			}
			return errors.New(msg)
		default:
			// Ignore packet types we don't model (e.g. PG/MySQL
			// protocol-specific writes won't be sent for tcp-type
			// connections; we'd just drop them anyway).
		}
	}
}

// isTunnelableType reports whether a hoop connection type is suitable
// for transparent IP-level tunneling. SSH/HTTP/Kubernetes connections
// need protocol-aware clients and are excluded.
func isTunnelableType(t string) bool {
	switch pb.ConnectionType(t) {
	case pb.ConnectionTypePostgres,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMSSQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeOracleDB,
		pb.ConnectionTypeTCP:
		return true
	}
	return false
}

// isClosedConnErr suppresses noise from the routine close races between
// io.Copy goroutines and transport.Close().
func isClosedConnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	// gRPC closes surface as "use of closed network connection" or
	// "transport is closing" depending on timing; both are benign here.
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "transport is closing") ||
		strings.Contains(msg, "context canceled")
}
