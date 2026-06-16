package pgproto

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"golang.org/x/crypto/ssh"
)

// Handler is the PostgreSQL protocol handler for a single proxy connection.
// It owns the gRPC transport and channel registry, including the read loop.
type Handler struct {
	sid        string
	connID     string
	grpcClient pb.ClientTransport
	channels   sync.Map // pgConnID string → ssh.Channel
	channelWg  sync.WaitGroup
	ctx        context.Context
	cancelFn   func(msg string, a ...any)
}

// OpenSession sends SessionOpen over grpcClient, waits for SessionOpenOK, then
// starts the packet read goroutine and returns the ready Handler. It takes
// ownership of grpcClient and will close it via Close.
func OpenSession(sid, connID string, grpcClient pb.ClientTransport, ctx context.Context, cancelFn func(msg string, a ...any)) (*Handler, error) {
	if err := grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sid),
			pb.SpecClientConnectionID: []byte(connID),
		},
	}); err != nil {
		return nil, fmt.Errorf("failed sending SessionOpen: %w", err)
	}

	type result struct{ err error }
	resultCh := make(chan result, 1)
	go func() {
		for {
			pkt, err := grpcClient.Recv()
			if err != nil {
				resultCh <- result{err: err}
				return
			}
			if pkt == nil {
				resultCh <- result{err: fmt.Errorf("received nil packet during session open")}
				return
			}
			switch pb.PacketType(pkt.Type) {
			case pbclient.SessionOpenOK:
				resultCh <- result{}
				return
			case pbclient.SessionOpenWaitingApproval:
				resultCh <- result{err: fmt.Errorf("session with review is not supported")}
				return
			case pbclient.TCPConnectionClose, pbclient.SessionClose:
				resultCh <- result{err: fmt.Errorf("connection closed by server: %s", pkt.Payload)}
				return
			default:
				resultCh <- result{err: fmt.Errorf("unexpected packet type during handshake: %v", pkt.Type)}
				return
			}
		}
	}()

	select {
	case r := <-resultCh:
		if r.err != nil {
			return nil, r.err
		}
		h := &Handler{
			sid:        sid,
			connID:     connID,
			grpcClient: grpcClient,
			ctx:        ctx,
			cancelFn:   cancelFn,
		}
		go h.readLoop()
		return h, nil
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("session timed out before it was ready")
	}
}

func (h *Handler) readLoop() {
	for {
		pkt, err := h.grpcClient.Recv()
		if err != nil {
			h.cancelFn("received error processing grpc client, err=%v", err)
			return
		}
		if pkt == nil {
			h.cancelFn("received nil packet, closing connection")
			return
		}
		switch pb.PacketType(pkt.Type) {
		case pbclient.PGConnectionWrite:
			h.dispatchPacket(pkt)
		case pbclient.TCPConnectionClose, pbclient.SessionClose:
			h.cancelFn("connection closed by server, payload=%v", string(pkt.Payload))
			return
		default:
			h.cancelFn("received invalid packet type %v", pkt.Type)
			return
		}
	}
}

// AcceptAndServe accepts newCh and starts PG data forwarding goroutines.
func (h *Handler) AcceptAndServe(newCh ssh.NewChannel, channelID uint16) error {
	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		return fmt.Errorf("failed accepting pg channel: %w", err)
	}

	// Unique connection ID per channel so the agent creates separate Postgres
	// connections for each port-forward channel on this SSH session.
	pgConnID := fmt.Sprintf("%s-ch%d", h.connID, channelID)
	h.channels.Store(pgConnID, clientCh)

	// Forward raw bytes from the SSH channel to the agent via PG packets.
	// The first packet triggers the agent to open a new Postgres connection.
	h.channelWg.Go(func() {
		defer h.channels.Delete(pgConnID)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := clientCh.Read(buf)
			if n > 0 {
				if err := h.grpcClient.Send(&pb.Packet{
					Type:    pbagent.PGConnectionWrite,
					Payload: buf[:n],
					Spec: map[string][]byte{
						string(pb.SpecGatewaySessionID):   []byte(h.sid),
						string(pb.SpecClientConnectionID): []byte(pgConnID),
					},
				}); err != nil {
					h.cancelFn("pg: failed forwarding data to agent, err=%v", err)
					return
				}
			}
			if readErr != nil {
				break
			}
		}
	})

	// direct-tcpip channels do not carry session requests (pty-req, exec, etc.),
	// but drain the channel to avoid blocking the SSH mux.
	h.channelWg.Go(func() {
		for req := range clientRequests {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	})

	return nil
}

// RangeChannels calls fn for each registered channel, same semantics as sync.Map.Range.
func (h *Handler) RangeChannels(fn func(key, value any) bool) { h.channels.Range(fn) }

// Wait blocks until all channel goroutines complete.
func (h *Handler) Wait() { h.channelWg.Wait() }

// SendClose sends the SessionClose packet to the agent.
func (h *Handler) SendClose() error {
	return h.grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionClose,
		Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(h.sid)},
	})
}

// Close shuts down the underlying gRPC transport.
func (h *Handler) Close() error {
	_, err := h.grpcClient.Close()
	return err
}

func (h *Handler) dispatchPacket(pkt *pb.Packet) {
	chanKey := string(pkt.Spec[pb.SpecClientConnectionID])
	obj, _ := h.channels.Load(chanKey)
	clientCh, ok := obj.(ssh.Channel)
	if !ok {
		log.With("sid", h.sid, "conn", h.connID).Warnf("dropping PG data for unknown channel %q", chanKey)
		return
	}
	if _, err := clientCh.Write(pkt.Payload); err != nil {
		h.cancelFn("failed writing PG data to channel, err=%v", err)
	}
}
