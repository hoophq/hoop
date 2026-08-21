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
//	  3. For TCP-style connections, sends the initial
//	     pbagent.TCPConnectionWrite with SpecTCPServerConnectKey to ask
//	     the agent to open its upstream TCP socket. httpproxy
//	     connections skip this step: the agent builds its HTTP proxy
//	     lazily on the first data packet.
//	  4. Pumps bytes in both directions until either side closes, using
//	     the packet family for the connection type (see packetProfile):
//	      local -> gateway via pbagent.{TCP,HttpProxy}ConnectionWrite
//	      gateway -> local via pbclient.{TCP,HttpProxy}ConnectionWrite
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
	"github.com/hoophq/hoop/common/mongotypes"
	"github.com/hoophq/hoop/common/mssqltypes"
	"github.com/hoophq/hoop/common/mysqltypes"
	"github.com/hoophq/hoop/common/pgtypes"
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

	// HttpProxyBaseURL is the URL clients use to reach this connection
	// through the tunnel (e.g. "http://api-prod.hoop"). Only consumed
	// when the session resolves to an httpproxy connection: libhoop
	// rewrites absolute URLs / Location headers in upstream responses
	// so redirects keep pointing at the proxy. Optional for TCP-style
	// connections.
	HttpProxyBaseURL string

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

	// We only support connection types whose wire format is an opaque
	// byte stream from the tunnel's point of view: TCP-style protocols
	// and httpproxy (plain HTTP bytes; the agent parses and forwards
	// them). SSH, terminals, kubernetes, etc. need protocol-specific
	// clients and have no place in a transparent IP tunnel.
	prof, ok := profileFor(connType)
	if !ok {
		return fmt.Errorf("connection %q has type %q which is not tunnelable; supported: postgres, mysql, mssql, mongodb, oracledb, tcp, httpproxy",
			opts.ConnectionName, connType)
	}

	log.With("connection", opts.ConnectionName, "session", sessionID, "type", connType).
		Debugf("tunnel pipe established")

	return pumpBytes(ctx, transport, local, sessionID, prof, opts)
}

// packetProfile captures how a connection type maps onto the gateway's
// packet families, and how the client's byte stream must be framed onto
// them.
//
// The choice of family decides who authenticates to the upstream:
//
//   - The protocol families (PG/MySQL/MSSQL/MongoDB/Oracle) route to the
//     agent's libhoop proxy, which terminates the client's authentication
//     locally and re-authenticates upstream with the connection's stored
//     secrets. That is what lets a client present the fixed local
//     noop/noop placeholder, and what makes DLP, guardrails and
//     query-level audit work.
//   - The raw TCP family routes to the agent's byte relay, which dials the
//     upstream and copies verbatim. The client faces the database's own
//     auth challenge, so it must hold real credentials. Correct for the
//     `tcp` subtype (an opaque user-defined upstream), wrong for anything
//     hoop knows the protocol of.
//
// httpproxy has its own family (the agent routes it to the HTTP-parsing
// libhoop proxy instead of a raw upstream socket).
type packetProfile struct {
	// agentWrite is the packet type for local -> gateway data.
	agentWrite pb.PacketType
	// clientWrite is the packet type for gateway -> local data.
	clientWrite pb.PacketType
	// sendTCPOpen indicates the agent needs an explicit "dial your
	// upstream now" packet (SpecTCPServerConnectKey) before any data.
	// Only the raw TCP relay has this handshake; the protocol proxies and
	// the httpproxy handler build their upstream lazily on first data.
	sendTCPOpen bool
	// isHTTPProxy marks sessions whose spec must carry
	// SpecHttpProxyBaseUrl on every write.
	isHTTPProxy bool
	// initWrite, when true, sends one empty data packet right after session
	// open. The MySQL proxy is server-speaks-first: it must be constructed
	// before it can emit the server greeting the client waits for.
	initWrite bool
	// frame re-frames the local->gateway byte stream into whole protocol
	// packets, one per gateway packet. Nil means forward raw chunks.
	//
	// This matters because the agent's protocol proxies decode one packet
	// from each write they receive; feeding them arbitrary TCP chunks
	// desynchronises the decoder. Mirrors what the `hoop connect` local
	// proxies do (client/proxy).
	frame func(dst io.Writer, src io.Reader) error
}

// profileFor returns the packet profile for a hoop connection type, or
// ok=false when the type is not tunnelable.
func profileFor(connType string) (packetProfile, bool) {
	switch pb.ConnectionType(connType) {
	case pb.ConnectionTypePostgres:
		return packetProfile{
			agentWrite:  pbagent.PGConnectionWrite,
			clientWrite: pbclient.PGConnectionWrite,
			frame: func(dst io.Writer, src io.Reader) error {
				// The cancel-request hook is for the `hoop connect` proxy,
				// which multiplexes many client connections over one
				// session and has to match a cancel to its target backend.
				// A tunnel pipe is one flow, so there is nothing to match:
				// forward the cancel request as-is.
				_, err := pgtypes.CopyBuffer(dst, src, nil)
				return err
			},
		}, true
	case pb.ConnectionTypeMySQL:
		return packetProfile{
			agentWrite:  pbagent.MySQLConnectionWrite,
			clientWrite: pbclient.MySQLConnectionWrite,
			initWrite:   true,
			frame:       mysqltypes.CopyBuffer,
		}, true
	case pb.ConnectionTypeMSSQL:
		return packetProfile{
			agentWrite:  pbagent.MSSQLConnectionWrite,
			clientWrite: pbclient.MSSQLConnectionWrite,
			frame:       mssqltypes.CopyBuffer,
		}, true
	case pb.ConnectionTypeMongoDB:
		return packetProfile{
			agentWrite:  pbagent.MongoDBConnectionWrite,
			clientWrite: pbclient.MongoDBConnectionWrite,
			frame:       mongotypes.CopyBuffer,
		}, true
	case pb.ConnectionTypeOracleDB:
		// The Oracle proxy re-frames TNS packets itself (it buffers partial
		// packets across writes), so the relay forwards raw chunks.
		return packetProfile{
			agentWrite:  pbagent.OracleConnectionWrite,
			clientWrite: pbclient.OracleConnectionWrite,
		}, true
	case pb.ConnectionTypeTCP:
		// A generic TCP connection has no protocol hoop can parse and no
		// credentials to inject: the byte relay is the correct handler.
		return packetProfile{
			agentWrite:  pbagent.TCPConnectionWrite,
			clientWrite: pbclient.TCPConnectionWrite,
			sendTCPOpen: true,
		}, true
	case pb.ConnectionTypeHttpProxy:
		return packetProfile{
			agentWrite:  pbagent.HttpProxyConnectionWrite,
			clientWrite: pbclient.HttpProxyConnectionWrite,
			isHTTPProxy: true,
		}, true
	}
	return packetProfile{}, false
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
func pumpBytes(ctx context.Context, transport pb.ClientTransport, local io.ReadWriteCloser, sessionID string, prof packetProfile, opts PipeOptions) error {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID:   []byte(sessionID),
		pb.SpecClientConnectionID: []byte(connectionIDOnPipe),
	}
	if prof.isHTTPProxy && opts.HttpProxyBaseURL != "" {
		spec[pb.SpecHttpProxyBaseUrl] = []byte(opts.HttpProxyBaseURL)
	}

	if prof.sendTCPOpen {
		// Tell the agent to open its upstream socket. The
		// TCPServerConnectKey spec marks this as a no-op write so the
		// agent does not forward an empty payload to the database.
		openSpec := make(map[string][]byte, len(spec)+1)
		for k, v := range spec {
			openSpec[k] = v
		}
		openSpec[pb.SpecTCPServerConnectKey] = nil
		if err := transport.Send(&pb.Packet{
			Type: prof.agentWrite.String(),
			Spec: openSpec,
		}); err != nil {
			return fmt.Errorf("send %s open: %w", prof.agentWrite, err)
		}
	}

	if prof.initWrite {
		// Construct the agent-side proxy before the client says anything.
		// MySQL is server-speaks-first: the client waits for the server
		// greeting, which only exists once the proxy has connected
		// upstream. Without this the flow deadlocks on both sides waiting
		// to read. Mirrors the `hoop connect` MySQL proxy's initial
		// zero-length write.
		if err := transport.Send(&pb.Packet{
			Type: prof.agentWrite.String(),
			Spec: spec,
		}); err != nil {
			return fmt.Errorf("send %s init: %w", prof.agentWrite, err)
		}
	}

	transport.StartKeepAlive()

	// Record the first terminating outcome; later ones are ignored so the
	// surfaced error is the one that actually ended the pipe.
	var (
		once    sync.Once
		exitErr error
	)
	finish := func(err error) {
		once.Do(func() { exitErr = err })
	}

	pumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// sendClose tells the agent to tear its upstream down. It must reach the
	// gateway exactly once on every exit path, including the one where the
	// gateway side ends first and this function returns before the writer
	// goroutine has finished — hence the Once rather than a plain call in the
	// writer. Send is mutex-guarded (common/grpc.mutexClient), so racing
	// goroutines are safe; if the stream is already gone the Send is a
	// harmless no-op.
	var closeOnce sync.Once
	sendClose := func() {
		closeOnce.Do(func() {
			_ = transport.Send(&pb.Packet{
				Type: pbagent.TCPConnectionClose,
				Spec: spec,
			})
		})
	}

	// local -> gateway
	//
	// A clean end here is a TCP half-close: the client shut its write side
	// after sending a request and is still waiting to read the answer. It is
	// therefore NOT a reason to end the pipe — only a hard write error is.
	writeErr := make(chan error, 1)
	go func() {
		writer := pb.NewStreamWriter(transport, prof.agentWrite, spec)
		// Protocol families need whole packets per write (see
		// packetProfile.frame); the raw relay and Oracle take chunks
		// as they come.
		var err error
		if prof.frame != nil {
			err = prof.frame(writer, local)
		} else {
			_, err = io.Copy(writer, local)
		}
		// Signal regardless of how the copy ended (EOF, error, or peer close
		// cancelling our context): the gateway needs a definitive signal that
		// the client side is done sending.
		sendClose()
		if err != nil && !isClosedConnErr(err) {
			writeErr <- fmt.Errorf("local->gateway: %w", err)
			return
		}
		writeErr <- nil
	}()

	// gateway -> local
	//
	// The reader owns termination: it returns when the gateway ends the
	// exchange (TCPConnectionClose / SessionClose / stream EOF), which is the
	// only signal that no more response bytes are coming.
	readDone := make(chan error, 1)
	go func() {
		err := readFromGateway(pumpCtx, transport, local, prof.clientWrite)
		// Closing the local conn unblocks the local->gateway copy when the
		// gateway side died first.
		_ = local.Close()
		if err != nil && !errors.Is(err, io.EOF) && !isClosedConnErr(err) {
			readDone <- fmt.Errorf("gateway->local: %w", err)
			return
		}
		readDone <- nil
	}()

	// Wait for the reader, a hard write error, or cancellation.
	//
	// We deliberately do NOT wait for the writer's clean finish: that is the
	// half-close above, and returning on it would close the socket while the
	// gateway's response is still in flight.
	//
	// We also do not wait for BOTH goroutines. The reader parks in
	// transport.Recv(), which honours neither ctx nor a half-closed session,
	// so on the cancellation path the caller's deferred transport.Close() is
	// what releases it — and that cannot happen while we block here.
	select {
	case err := <-readDone:
		finish(err)
	case err := <-writeErr:
		if err != nil {
			finish(err)
			break
		}
		// Clean half-close: keep draining until the gateway is done, the
		// caller cancels, or the reader fails.
		select {
		case rerr := <-readDone:
			finish(rerr)
		case <-ctx.Done():
			finish(ctx.Err())
		}
	case <-ctx.Done():
		finish(ctx.Err())
	}

	// Unblock whichever goroutine is still running and respects pumpCtx, and
	// make sure the agent heard about the close even if the writer never got
	// that far.
	cancel()
	_ = local.Close()
	sendClose()
	return exitErr
}

// readFromGateway loops on Recv() and writes packet payloads to local.
// It returns when the stream ends or a non-recoverable packet arrives.
// dataType is the packet family carrying gateway -> local data for this
// session (the connection's protocol family, raw TCP, or httpproxy writes).
//
// TCPConnectionClose is the close signal for every family, protocol proxies
// included: the agent keys its connection store by sessionID:connectionID
// regardless of which proxy owns the entry.
func readFromGateway(ctx context.Context, transport pb.ClientTransport, local io.Writer, dataType pb.PacketType) error {
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
		case dataType:
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
