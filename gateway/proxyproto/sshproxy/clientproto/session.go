package clientproto

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	sshtypes "libhoop/proxy/ssh/types"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"golang.org/x/crypto/ssh"
)

type pendingSSHRequest struct {
	reply chan bool
}

type pendingReplyQueue struct {
	mu      sync.Mutex
	pending []*pendingSSHRequest
}

// Session manages the Hoop packet protocol for a single SSH proxy connection.
// It owns the gRPC transport, channel registry, pending-reply queues, and all
// packet encoding/decoding details so that the parent sshproxy package deals
// only with SSH connection lifecycle concerns.
type Session struct {
	sid             string
	connID          string
	grpcClient      pb.ClientTransport
	streamW         io.Writer  // pre-built StreamWriter for SSHConnectionWrite packets
	sshChannels     sync.Map   // key → any (io.WriteCloser or ssh.Channel)
	pendingRequests sync.Map   // uint16 → *pendingReplyQueue
	connType        pb.ConnectionType
	channelWg       sync.WaitGroup
	ctx             context.Context
	cancelFn        func(msg string, a ...any)
}

// New creates a Session for the given gRPC transport.
func New(sid, connID string, grpcClient pb.ClientTransport, ctx context.Context, cancelFn func(msg string, a ...any)) *Session {
	s := &Session{
		sid:        sid,
		connID:     connID,
		grpcClient: grpcClient,
		ctx:        ctx,
		cancelFn:   cancelFn,
	}
	s.streamW = pb.NewStreamWriter(grpcClient, pbagent.SSHConnectionWrite, map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(sid),
		string(pb.SpecClientConnectionID): []byte(connID),
	})
	return s
}

// Open sends SessionOpen to the agent, starts the inbound packet dispatch
// goroutine, and blocks until SessionOpenOK is received, an error occurs,
// or the 5-second startup timeout expires.
func (s *Session) Open() {
	if err := s.grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(s.sid),
			pb.SpecClientConnectionID: []byte(s.connID),
		},
	}); err != nil {
		s.cancelFn("failed sending open session packet, err=%v", err)
		return
	}

	startupCh := make(chan struct{})
	go func() {
		defer func() { startupCh <- struct{}{}; close(startupCh) }()
		for {
			pkt, err := s.grpcClient.Recv()
			if err != nil {
				s.cancelFn("received error processing grpc client, err=%v", err)
				return
			}
			if pkt == nil {
				s.cancelFn("received nil packet, closing connection")
				return
			}
			switch pb.PacketType(pkt.Type) {
			case pbclient.SessionOpenOK:
				s.connType = pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
				log.With("sid", s.sid).Infof("session opened successfully")
				startupCh <- struct{}{}

			case pbclient.SSHConnectionWrite:
				s.dispatchSSHPacket(pkt)

			case pbclient.PGConnectionWrite:
				chanKey := string(pkt.Spec[pb.SpecClientConnectionID])
				obj, _ := s.sshChannels.Load(chanKey)
				clientCh, ok := obj.(io.WriteCloser)
				if !ok {
					log.With("sid", s.sid, "conn", s.connID).Warnf("dropping PG data for unknown channel %q", chanKey)
					continue
				}
				if _, err := clientCh.Write(pkt.Payload); err != nil {
					s.cancelFn("failed writing PG data to channel, err=%v", err)
					return
				}

			case pbclient.SessionOpenWaitingApproval:
				s.cancelFn("session with review are not implemented yet, closing connection")
				return

			case pbclient.TCPConnectionClose, pbclient.SessionClose:
				s.cancelFn("connection closed by server, payload=%v", string(pkt.Payload))
				return

			default:
				s.cancelFn(`received invalid packet type %T`, pkt.Type)
				return
			}
		}
	}()

	readyTimeout, cancelReady := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelReady()
	select {
	case <-startupCh:
	case <-readyTimeout.Done():
		s.cancelFn("session timed out before it was ready")
	}
}

func (s *Session) dispatchSSHPacket(pkt *pb.Packet) {
	switch sshtypes.DecodeType(pkt.Payload) {
	case sshtypes.DataType:
		var data sshtypes.Data
		if err := sshtypes.Decode(pkt.Payload, &data); err != nil {
			s.cancelFn("failed decoding ssh data, err=%v", err)
			return
		}
		obj, _ := s.sshChannels.Load(fmt.Sprintf("%v", data.ChannelID))
		clientCh, ok := obj.(io.WriteCloser)
		if !ok {
			s.cancelFn("local channel %q not found", data.ChannelID)
			return
		}
		log.With("sid", s.sid, "ch", data.ChannelID, "conn", s.connID).Debugf("received data type")
		if _, err := clientCh.Write(data.Payload); err != nil {
			s.cancelFn("failed writing ssh data, err=%v", err)
			return
		}

	case sshtypes.CloseChannelType:
		var cc sshtypes.CloseChannel
		if err := sshtypes.Decode(pkt.Payload, &cc); err != nil {
			s.cancelFn("failed decoding close channel, err=%v", err)
			return
		}
		obj, _ := s.sshChannels.Load(fmt.Sprintf("%v", cc.ID))
		if clientCh, ok := obj.(io.Closer); ok {
			err := clientCh.Close()
			log.With("sid", s.sid, "ch", cc.ID, "conn", s.connID).
				Debugf("closing client ssh channel type=%v, err=%v", cc.Type, err)
		}

	case sshtypes.SSHRequestReplyType:
		var reply sshtypes.SSHRequestReply
		if err := sshtypes.Decode(pkt.Payload, &reply); err != nil {
			s.cancelFn("failed decoding ssh request reply, err=%v", err)
			return
		}
		log.With("sid", s.sid, "ch", reply.ChannelID, "conn", s.connID).
			Debugf("received ssh request reply, ok=%v", reply.OK)
		queue := s.loadPendingReplyQueue(reply.ChannelID)
		if queue == nil {
			log.With("sid", s.sid, "ch", reply.ChannelID, "conn", s.connID).
				Infof("pending reply queue missing or invalid, dropping reply")
			return
		}
		queue.mu.Lock()
		if len(queue.pending) == 0 {
			queue.mu.Unlock()
			return
		}
		pendingReq := queue.pending[0]
		queue.pending = queue.pending[1:]
		queue.mu.Unlock()
		select {
		case pendingReq.reply <- reply.OK:
			log.With("sid", s.sid, "ch", reply.ChannelID, "conn", s.connID).
				Debugf("forwarded ssh request reply to client")
		case <-s.ctx.Done():
			return
		default:
			log.With("sid", s.sid, "ch", reply.ChannelID, "conn", s.connID).
				Infof("channel full or already handled (e.g. timeout), dropping request")
		}

	case sshtypes.ServerSSHRequestType:
		var serverReq sshtypes.ServerSSHRequest
		if err := sshtypes.Decode(pkt.Payload, &serverReq); err != nil {
			s.cancelFn("failed decoding server ssh request, err=%v", err)
			return
		}
		log.With("sid", s.sid, "ch", serverReq.ChannelID, "conn", s.connID).
			Debugf("received server ssh request type=%q, wantreply=%v, payload-length=%v",
				serverReq.RequestType, serverReq.WantReply, len(serverReq.Payload))
		obj, _ := s.sshChannels.Load(fmt.Sprintf("%v", serverReq.ChannelID))
		clientCh, ok := obj.(ssh.Channel)
		if !ok {
			log.With("sid", s.sid, "ch", serverReq.ChannelID, "conn", s.connID).
				Warnf("local channel not found for server request")
			return
		}
		if _, err := clientCh.SendRequest(serverReq.RequestType, serverReq.WantReply, serverReq.Payload); err != nil {
			log.With("sid", s.sid, "ch", serverReq.ChannelID, "conn", s.connID).
				Debugf("failed sending server request to client: %v", err)
		}

	case sshtypes.EOFType:
		var eof sshtypes.EOF
		if err := sshtypes.Decode(pkt.Payload, &eof); err != nil {
			log.With("sid", s.sid, "ch", eof.ChannelID, "conn", s.connID).
				Infof("failed decoding ssh eof, err=%v", err)
			s.cancelFn("failed decoding ssh eof, err=%v", err)
			return
		}
		obj, _ := s.sshChannels.Load(fmt.Sprintf("%v", eof.ChannelID))
		if ch, ok := obj.(interface{ CloseWrite() error }); ok {
			_ = ch.CloseWrite()
		}

	default:
		s.cancelFn("received unknown ssh message type (%v)", sshtypes.DecodeType(pkt.Payload))
	}
}

func (s *Session) loadPendingReplyQueue(channelID uint16) *pendingReplyQueue {
	v, ok := s.pendingRequests.Load(channelID)
	if !ok {
		return nil
	}
	q, ok := v.(*pendingReplyQueue)
	if !ok {
		return nil
	}
	return q
}

// ConnectionType returns the Hoop connection type resolved from SessionOpenOK.
// Only valid after Open returns without cancelling the context.
func (s *Session) ConnectionType() pb.ConnectionType { return s.connType }

// GRPCClient returns the underlying transport.
func (s *Session) GRPCClient() pb.ClientTransport { return s.grpcClient }

// RangeChannels calls fn for each registered channel, same semantics as sync.Map.Range.
func (s *Session) RangeChannels(fn func(key, value any) bool) { s.sshChannels.Range(fn) }

// AcceptAndServeChannel accepts newCh and sets up full bidirectional data forwarding.
// It dispatches to SSH or PostgreSQL handling based on the session's connection
// type resolved at Open time.
func (s *Session) AcceptAndServeChannel(newCh ssh.NewChannel, channelID uint16) error {
	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		return fmt.Errorf("failed accepting channel: %w", err)
	}
	if s.connType == pb.ConnectionTypePostgres {
		s.servePGChannel(clientCh, clientRequests, channelID)
		return nil
	}
	s.serveSSHChannel(clientCh, clientRequests, newCh.ChannelType(), newCh.ExtraData(), channelID)
	return nil
}

// serveSSHChannel registers the channel and starts bidirectional data forwarding
// goroutines for SSH tunnel channels (session, direct-tcpip).
func (s *Session) serveSSHChannel(clientCh ssh.Channel, clientRequests <-chan *ssh.Request, channelType string, extraData []byte, channelID uint16) {
	s.sshChannels.Store(fmt.Sprintf("%v", channelID), clientCh)

	openChData := (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      channelType,
		ChannelExtraData: extraData,
	}).Encode()
	if _, err := s.streamW.Write(openChData); err != nil {
		s.cancelFn("failed writing open channel to stream, err=%v", err)
		return
	}

	// Copy data from client to agent. Note: We don't close clientCh here because
	// the client may still be waiting to receive data (e.g., git clone sends a command
	// then waits for the response). The channel will be closed when we receive
	// a CloseChannel message from the agent.
	s.channelWg.Go(func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := clientCh.Read(buf)
			if n > 0 {
				log.With("sid", s.sid, "conn", s.connID, "ch", channelID).
					Debugf("read %d bytes from client, forwarding to agent", n)
				data := sshtypes.Data{ChannelID: channelID, Payload: buf[:n]}
				if _, writeErr := s.streamW.Write(data.Encode()); writeErr != nil {
					s.cancelFn("failed writing client data to agent, err=%v", writeErr)
					return
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					log.With("sid", s.sid, "conn", s.connID, "ch", channelID).
						Debugf("error reading from client: %v", readErr)
				} else {
					log.With("sid", s.sid, "conn", s.connID, "ch", channelID).
						Debugf("client closed write side (EOF), sending EOF to agent")
					// Signal to the agent that the client has closed its write side
					eofData := sshtypes.EOF{ChannelID: channelID}
					if _, writeErr := s.streamW.Write(eofData.Encode()); writeErr != nil {
						log.With("sid", s.sid, "conn", s.connID, "ch", channelID).
							Debugf("failed sending EOF to agent: %v", writeErr)
					}
				}
				break
			}
		}
	})

	// Handle incoming requests from the client.
	s.channelWg.Go(func() {
		for req := range clientRequests {
			log.With("sid", s.sid, "conn", s.connID, "ch", channelID, "type", req.Type).
				Debug("received client ssh request")

			data := (sshtypes.SSHRequest{
				ChannelID:   channelID,
				RequestType: req.Type,
				WantReply:   req.WantReply,
				Payload:     req.Payload,
			}).Encode()
			if _, err := s.streamW.Write(data); err != nil {
				s.cancelFn("failed writing to stream, err=%v", err)
				return
			}

			if req.WantReply {
				replyChan := make(chan bool, 1)
				v, _ := s.pendingRequests.LoadOrStore(channelID, &pendingReplyQueue{})
				queue, ok := v.(*pendingReplyQueue)
				if !ok || queue == nil {
					log.With("sid", s.sid, "conn", s.connID, "ch", channelID).
						Warnf("pending reply queue missing or invalid, skipping")
					continue
				}
				queue.mu.Lock()
				queue.pending = append(queue.pending, &pendingSSHRequest{reply: replyChan})
				queue.mu.Unlock()

				// Wait for the agent's reply in a separate goroutine to avoid blocking.
				go func(clientReq *ssh.Request, chID uint16) {
					select {
					case <-s.ctx.Done():
						return
					case ok := <-replyChan:
						if err := clientReq.Reply(ok, nil); err != nil {
							log.With("sid", s.sid, "conn", s.connID, "ch", chID, "type", clientReq.Type).
								Debugf("failed sending response to channel, err=%v", err)
						}
					case <-time.After(5 * time.Second):
						// Timeout waiting for agent reply, assume failure.
						if err := clientReq.Reply(false, nil); err != nil {
							log.With("sid", s.sid, "conn", s.connID, "ch", chID, "type", clientReq.Type).
								Debugf("failed sending response to channel (timeout), err=%v", err)
						}
					}
				}(req, channelID)
			}
		}
		log.With("ch", channelID, "conn", s.connID).Debugf("done processing ssh client requests")
	})
}

// servePGChannel registers the channel and starts data forwarding goroutines
// for PostgreSQL port-forwarding channels. Each channel gets a unique per-channel
// connection ID so the agent maintains a separate backend postgres connection per channel.
func (s *Session) servePGChannel(clientCh ssh.Channel, clientRequests <-chan *ssh.Request, channelID uint16) {
	// Unique connection ID per channel so the agent creates separate postgres
	// connections for each port-forward channel on this SSH session.
	pgConnID := fmt.Sprintf("%s-ch%d", s.connID, channelID)
	s.sshChannels.Store(pgConnID, clientCh)

	// Forward raw bytes from the SSH channel to the agent via PG packets.
	// The first packet triggers the agent to open a new postgres connection.
	s.channelWg.Go(func() {
		defer s.sshChannels.Delete(pgConnID)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := clientCh.Read(buf)
			if n > 0 {
				if err := s.grpcClient.Send(&pb.Packet{
					Type:    pbagent.PGConnectionWrite,
					Payload: buf[:n],
					Spec: map[string][]byte{
						string(pb.SpecGatewaySessionID):   []byte(s.sid),
						string(pb.SpecClientConnectionID): []byte(pgConnID),
					},
				}); err != nil {
					s.cancelFn("cert-pg: failed forwarding data to agent, err=%v", err)
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
	s.channelWg.Go(func() {
		for req := range clientRequests {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	})
}

// Wait blocks until all goroutines started by channel serving complete.
func (s *Session) Wait() { s.channelWg.Wait() }

// SendClose sends the SessionClose packet to the agent.
func (s *Session) SendClose() error {
	return s.grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionClose,
		Spec: map[string][]byte{pb.SpecGatewaySessionID: []byte(s.sid)},
	})
}
