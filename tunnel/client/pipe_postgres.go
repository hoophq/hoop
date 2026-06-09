// PoC: protocol-aware Postgres piping for the tunnel.
//
// # Why this exists
//
// The generic byte pump (pumpBytes / pbagent.TCPConnectionWrite) makes the
// agent open a *raw* TCP socket to the upstream Postgres and forward bytes
// verbatim. On that path the agent performs NO credential injection: the
// user's psql startup packet — including whatever user/password they typed —
// reaches the real Postgres unchanged. That forces tunnel users to know the
// real upstream credentials, which defeats the point of hoop.
//
// The native-port gateway proxy (gateway/proxyproto/postgresproxy) instead
// speaks the Postgres wire protocol to the agent via pbagent.PGConnectionWrite.
// On THAT path the agent runs libhoop.NewDBCore(...).Postgres(), which strips
// the client's credentials and re-authenticates to the upstream with the REAL
// credentials fetched from the gateway DB. The native-port proxy only needs
// the client-supplied "user" field as a lookup key to resolve which connection
// + identity is being requested.
//
// In the tunnel we already know both:
//   - which connection: the virtual IP -> connection-name gRPC metadata.
//   - the user's identity: the bearer token on the gRPC stream.
//
// So this pump mirrors the gateway proxy's client-side protocol handling
// (postgresproxy.handleClientWrite / handleServerWrite) MINUS the secret-key
// lookup. The user may type any user/password (e.g. hoop/hoop): the agent
// discards it and injects the real upstream credentials.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hoophq/hoop/common/log"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// pumpPostgres drives the Postgres wire protocol between the local psql
// connection and the agent's libhoop-backed upstream, using
// pbagent.PGConnectionWrite so the agent injects the real upstream
// credentials.
//
// Flow (mirrors gateway/proxyproto/postgresproxy):
//  1. Read the Postgres startup packet from local. If the client requested
//     SSL (SSLRequest), reply 'N' (SSL not supported on the tunnel hop —
//     the agent->upstream leg handles real TLS) and read the real startup
//     packet next.
//  2. Send pbagent.PGConnectionWrite with the startup packet as the first
//     payload. The agent opens the libhoop Postgres core on first write.
//  3. Pump bidirectionally:
//     local  -> agent : decode each PG message from local, forward as
//     pbagent.PGConnectionWrite.
//     agent  -> local : on pbclient.PGConnectionWrite, write payload to local.
func pumpPostgres(ctx context.Context, transport pb.ClientTransport, local io.ReadWriteCloser, sessionID string) error {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID:   []byte(sessionID),
		pb.SpecClientConnectionID: []byte(connectionIDOnPipe),
	}

	// Step 1: read the startup packet, transparently handling an initial
	// SSLRequest the same way a Postgres server without TLS would.
	startupPkt, err := readStartupPacket(local)
	if err != nil {
		return fmt.Errorf("read startup packet: %w", err)
	}

	// Step 2: send the startup packet to the agent. The first
	// PGConnectionWrite triggers libhoop.NewDBCore(...).Postgres() on the
	// agent, which authenticates upstream with the real credentials.
	if err := transport.Send(&pb.Packet{
		Type:    pbagent.PGConnectionWrite,
		Payload: startupPkt,
		Spec:    spec,
	}); err != nil {
		return fmt.Errorf("send startup PGConnectionWrite: %w", err)
	}

	transport.StartKeepAlive()

	var (
		once    sync.Once
		exitErr error
	)
	finish := func(err error) { once.Do(func() { exitErr = err }) }

	pumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// local -> agent: decode each PG message and forward it.
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			if err := pumpCtx.Err(); err != nil {
				return
			}
			pkt, err := pgtypes.Decode(local)
			if err != nil {
				if !errors.Is(err, io.EOF) && !isClosedConnErr(err) {
					finish(fmt.Errorf("local->agent decode: %w", err))
				}
				// Tell the agent the client is done.
				_ = transport.Send(&pb.Packet{
					Type: pbagent.TCPConnectionClose,
					Spec: spec,
				})
				return
			}
			if err := transport.Send(&pb.Packet{
				Type:    pbagent.PGConnectionWrite,
				Payload: pkt.Encode(),
				Spec:    spec,
			}); err != nil {
				if !isClosedConnErr(err) {
					finish(fmt.Errorf("local->agent send: %w", err))
				}
				return
			}
		}
	}()

	// agent -> local: write PGConnectionWrite payloads back to psql.
	go func() {
		defer wg.Done()
		defer cancel()
		err := readPostgresFromGateway(pumpCtx, transport, local)
		if err != nil && !errors.Is(err, io.EOF) && !isClosedConnErr(err) {
			finish(fmt.Errorf("agent->local: %w", err))
		}
		_ = local.Close()
	}()

	wg.Wait()
	return exitErr
}

// readPostgresFromGateway loops on Recv() and writes PGConnectionWrite
// payloads to local until the stream ends or the session closes.
func readPostgresFromGateway(ctx context.Context, transport pb.ClientTransport, local io.Writer) error {
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
		case pbclient.PGConnectionWrite:
			if len(pkt.Payload) == 0 {
				continue
			}
			if _, werr := local.Write(pkt.Payload); werr != nil {
				return werr
			}
		case pbclient.TCPConnectionClose:
			return io.EOF
		case pbclient.SessionClose:
			msg := string(pkt.Payload)
			if msg == "" {
				return io.EOF
			}
			return errors.New(msg)
		default:
			// Ignore unmodelled packet types.
		}
	}
}

// readStartupPacket reads the Postgres startup packet from local. If the
// client opens with an SSLRequest, it replies 'N' (the tunnel hop is
// plaintext; the agent->upstream leg negotiates real TLS) and reads the
// subsequent real startup packet.
func readStartupPacket(local io.ReadWriter) ([]byte, error) {
	pkt, err := pgtypes.Decode(local)
	if err != nil {
		return nil, err
	}
	if pkt.IsCancelRequest() {
		return nil, errors.New("cancel request not supported on tunnel")
	}
	if pkt.IsFrontendSSLRequest() {
		// Decline SSL on the local hop. psql will fall back to plaintext
		// for the tunnel connection; the real TLS to the upstream is the
		// agent's responsibility.
		if _, err := local.Write([]byte{pgtypes.ServerSSLNotSupported.Byte()}); err != nil {
			return nil, fmt.Errorf("write SSL-not-supported: %w", err)
		}
		log.Debugf("tunnel postgres: declined client SSLRequest, awaiting plaintext startup")
		pkt, err = pgtypes.Decode(local)
		if err != nil {
			return nil, fmt.Errorf("decode startup after SSL decline: %w", err)
		}
		if pkt.IsCancelRequest() {
			return nil, errors.New("cancel request not supported on tunnel")
		}
	}
	return pkt.Encode(), nil
}
