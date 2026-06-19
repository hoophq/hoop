package pgproto

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pgtypes "github.com/hoophq/hoop/common/pgtypes"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"golang.org/x/crypto/ssh"
)

// pgChannel holds a single port-forward channel and the BackendKeyData the
// server sent for it, used to route CancelRequest messages to the right channel.
type pgChannel struct {
	ch             ssh.Channel
	backendKeyData *pgtypes.BackendKeyData
}

// Handler is the PostgreSQL protocol handler for a single proxy connection.
// It owns the gRPC transport and channel registry, including the read loop.
type Handler struct {
	sid        string
	connID     string
	grpcClient pb.ClientTransport
	channels   sync.Map // pgConnID string → *pgChannel
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
	pgChan := &pgChannel{ch: clientCh}
	h.channels.Store(pgConnID, pgChan)

	w := pb.NewStreamWriter(h.grpcClient, pbagent.PGConnectionWrite, map[string][]byte{
		pb.SpecGatewaySessionID:   []byte(h.sid),
		pb.SpecClientConnectionID: []byte(pgConnID),
	})

	// Forward raw bytes from the SSH channel to the agent via PG packets.
	// The first packet triggers the agent to open a new Postgres connection.
	h.channelWg.Go(func() {
		defer func() {
			h.channels.Delete(pgConnID)
			// Notify the agent to close its Postgres server connection for this channel.
			_ = h.grpcClient.Send(&pb.Packet{
				Type: pbagent.TCPConnectionClose,
				Spec: map[string][]byte{
					pb.SpecGatewaySessionID:   []byte(h.sid),
					pb.SpecClientConnectionID: []byte(pgConnID),
				},
			})
		}()
		// A cancel request is sent by a second connection; the response must
		// reach the connection identified by the backend PID.
		// See: https://www.postgresql.org/docs/current/protocol-flow.html#PROTOCOL-FLOW-CANCELING-REQUESTS
		written, err := pgtypes.CopyBuffer(w, clientCh, func(pid uint32) bool {
			targetConnID, targetChan := h.findChannelByPID(pid)
			if targetChan != nil {
				// Swap: responses for this connection ID should now reach the
				// query connection's SSH channel.
				h.channels.Store(pgConnID, targetChan)
				log.With("sid", h.sid, "conn", pgConnID).
					Infof("pg: cancel request for pid=%v, swapped to conn=%s", pid, targetConnID)
				// Give the cancel time to propagate, then close the cancel channel.
				go func() {
					time.Sleep(4 * time.Second)
					_ = clientCh.Close()
				}()
				return true
			}
			log.With("sid", h.sid, "conn", pgConnID).
				Infof("pg: cancel request for pid=%v, no matching connection found", pid)
			return false
		})
		if err != nil {
			h.cancelFn("pg: failed copying buffer for channel %v, written=%v, err=%v", channelID, written, err)
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
// The value passed to fn is always an ssh.Channel (unwrapped from the internal pgChannel),
// so callers such as notifyOpenChannels can use value.(ssh.Channel) safely.
func (h *Handler) RangeChannels(fn func(key, value any) bool) {
	h.channels.Range(func(k, v any) bool {
		pgChan, ok := v.(*pgChannel)
		if !ok || pgChan == nil {
			return true
		}
		return fn(k, pgChan.ch)
	})
}

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
	pgChan, ok := obj.(*pgChannel)
	if !ok || pgChan == nil {
		log.With("sid", h.sid, "conn", h.connID).Warnf("dropping PG data for unknown channel %q", chanKey)
		return
	}

	// Capture BackendKeyData (type 'K') so we can route CancelRequest messages.
	// The packet is exactly 13 bytes: 1 type + 4 length + 4 PID + 4 secret.
	if len(pkt.Payload) >= 13 && pgtypes.PacketType(pkt.Payload[0]) == pgtypes.ServerBackendKeyData {
		pgPid := binary.BigEndian.Uint32(pkt.Payload[5:9])
		pgSecret := binary.BigEndian.Uint32(pkt.Payload[9:13])
		log.With("sid", h.sid, "conn", chanKey).
			Infof("pg: backend process started, pid=%v", pgPid)
		pgChan.backendKeyData = &pgtypes.BackendKeyData{Pid: pgPid, SecretKey: pgSecret}
		h.channels.Store(chanKey, pgChan)
	}

	if _, err := pgChan.ch.Write(pkt.Payload); err != nil {
		h.cancelFn("failed writing PG data to channel, err=%v", err)
	}
}

// findChannelByPID returns the connection ID and pgChannel whose BackendKeyData
// matches pid. Returns empty string and nil if no match is found.
func (h *Handler) findChannelByPID(pid uint32) (string, *pgChannel) {
	var foundID string
	var foundChan *pgChannel
	h.channels.Range(func(k, v any) bool {
		ch, ok := v.(*pgChannel)
		if !ok || ch == nil || ch.backendKeyData == nil {
			return true
		}
		if ch.backendKeyData.Pid == pid {
			foundID = k.(string)
			foundChan = ch
			return false
		}
		return true
	})
	return foundID, foundChan
}
