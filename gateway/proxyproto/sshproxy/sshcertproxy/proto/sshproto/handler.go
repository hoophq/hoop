package sshproto

import (
	"context"
	"fmt"
	"io"
	"strings"
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

// Handler is the SSH protocol handler for a single proxy connection. It owns
// the gRPC transport, channel registry, and pending-reply state. OpenSession
// starts an internal goroutine that reads packets from the transport for the
// lifetime of the connection.
type Handler struct {
	sid             string
	connID          string
	grpcClient      pb.ClientTransport
	streamW         io.Writer
	channels        sync.Map // channelID string → ssh.Channel
	pendingRequests sync.Map // uint16 → *pendingReplyQueue
	channelWg       sync.WaitGroup
	ctx             context.Context
	cancelFn        func(msg string, a ...any)
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
		h.streamW = pb.NewStreamWriter(grpcClient, pbagent.SSHConnectionWrite, map[string][]byte{
			string(pb.SpecGatewaySessionID):   []byte(sid),
			string(pb.SpecClientConnectionID): []byte(connID),
		})
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
		case pbclient.SSHConnectionWrite:
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

// AcceptAndServe accepts newCh and starts bidirectional data forwarding goroutines.
func (h *Handler) AcceptAndServe(newCh ssh.NewChannel, channelID uint16) error {
	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		return fmt.Errorf("failed accepting ssh channel: %w", err)
	}

	h.channels.Store(fmt.Sprintf("%v", channelID), clientCh)

	openChData := (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      newCh.ChannelType(),
		ChannelExtraData: newCh.ExtraData(),
	}).Encode()
	if _, err := h.streamW.Write(openChData); err != nil {
		return fmt.Errorf("failed writing open channel to stream, err=%v", err)
	}

	// Raw session channels forward the client's exec/shell request and stdin data
	// as-is: gate stdin until the command request is forwarded so piped input
	// can't overtake it. Non-session channels (direct-tcpip) have no such request
	// and must stream immediately.
	h.startForwarding(clientCh, clientRequests, channelID, newCh.ChannelType() == "session")
	return nil
}

// ServeSession serves an already-accepted SSH session channel. preRequests are
// requests that were already received and replied to by the caller (e.g. pty-req
// pre-approved because the cert has permit-pty); they are forwarded to the agent
// with WantReply=false. execRequest is the exec from the client (used only to
// reply back); its reply is tied to the upstream session request. upstreamCommand
// is the command to execute on the upstream SSH server.
//
// When upstreamCommand is empty, stdin from the SSH channel is collected with a
// short deadline: if the client closes the write side (e.g. piped input via
// echo ... | ssh), the collected content is used as the exec command — identical
// to passing it as args. If the deadline expires first (interactive/empty stdin),
// a shell is started and subsequent stdin is forwarded as data.
func (h *Handler) ServeSession(
	clientCh ssh.Channel,
	clientRequests <-chan *ssh.Request,
	channelID uint16,
	preRequests []*ssh.Request,
	execRequest *ssh.Request,
	upstreamCommand string,
) error {
	h.channels.Store(fmt.Sprintf("%v", channelID), clientCh)

	openChData := (sshtypes.OpenChannel{
		ChannelID:   channelID,
		ChannelType: "session",
	}).Encode()
	if _, err := h.streamW.Write(openChData); err != nil {
		return fmt.Errorf("failed writing open channel to stream, err=%v", err)
	}

	// Forward pre-collected requests. WantReply=false because the caller already
	// sent a positive reply to the client for each of them.
	for _, req := range preRequests {
		data := (sshtypes.SSHRequest{
			ChannelID:   channelID,
			RequestType: req.Type,
			WantReply:   false,
			Payload:     req.Payload,
		}).Encode()
		if _, err := h.streamW.Write(data); err != nil {
			return fmt.Errorf("failed forwarding pre-exec request %q: %w", req.Type, err)
		}
	}

	// When no explicit command was given, try to collect piped stdin. A single
	// goroutine owns all reads from clientCh and feeds a pipe so that
	// startForwarding can safely consume whatever is left regardless of which
	// path is taken below.
	var stdinR io.Reader = clientCh
	if upstreamCommand == "" {
		pr, pw := io.Pipe()
		collectedCh := make(chan []byte, 1)

		go func() {
			var buf [32 * 1024]byte
			var collected []byte
			for {
				n, err := clientCh.Read(buf[:])
				if n > 0 {
					collected = append(collected, buf[:n]...)
					if _, werr := pw.Write(buf[:n]); werr != nil {
						// pr was closed — exec path consumed the data; stop.
						return
					}
				}
				if err != nil {
					collectedCh <- collected
					pw.Close()
					return
				}
			}
		}()

		select {
		case data := <-collectedCh:
			// clientCh EOF (pipe exhausted) — use content as exec command.
			upstreamCommand = strings.TrimRight(string(data), "\r\n")
			pr.Close() // goroutine already exited; safe to close
			stdinR = clientCh // clientCh is at EOF; startForwarding sees EOF immediately
		case <-time.After(100 * time.Millisecond):
			// No EOF yet — interactive/empty stdin. Feed startForwarding via pipe.
			stdinR = pr
		}
	}

	// Reply to the client immediately (same pattern as client/proxy/ssh.go).
	// The SSH client will not send stdin until it receives the exec reply.
	if execRequest.WantReply {
		_ = execRequest.Reply(true, nil)
	}

	upstreamReq := sshtypes.SSHRequest{
		ChannelID: channelID,
		WantReply: false, // we already replied to the client; no need for upstream to reply back
	}
	if upstreamCommand != "" {
		upstreamReq.RequestType = "exec"
		upstreamReq.Payload = ssh.Marshal(struct{ Command string }{upstreamCommand})
	} else {
		upstreamReq.RequestType = "shell"
	}
	if _, err := h.streamW.Write(upstreamReq.Encode()); err != nil {
		return fmt.Errorf("failed forwarding %s request: %w", upstreamReq.RequestType, err)
	}

	// The exec/shell request was already forwarded above, so stdin can flow
	// immediately — no gate needed.
	h.startForwarding(stdinR, clientRequests, channelID, false)
	return nil
}

// startForwarding launches goroutines that copy data and requests between
// the stdin reader / clientCh and the agent for the lifetime of the channel.
// stdinR is the source of client stdin data; it may be the ssh.Channel itself
// or a pipe fed by a pre-reading goroutine.
//
// When gateOnCommand is set, the stdin forwarder is held until the requests
// goroutine forwards the session's command request (exec/shell/subsystem). The
// go SSH server delivers channel requests and channel data on independent
// paths, so forwarding them from two goroutines races: piped stdin (e.g.
// `echo 'ls -l' | ssh host bash`) can overtake the exec request and reach the
// agent before the command exists, which drops the input. Callers that already
// forwarded the command themselves (ServeSession) pass false.
func (h *Handler) startForwarding(stdinR io.Reader, clientRequests <-chan *ssh.Request, channelID uint16, gateOnCommand bool) {
	sessionStarted := make(chan struct{})
	if !gateOnCommand {
		close(sessionStarted)
	}

	// Copy data from client to agent. We don't close clientCh here because
	// the client may still be waiting to receive data (e.g., git clone sends a command
	// then waits for the response). The channel will be closed when we receive
	// a CloseChannel message from the agent.
	h.channelWg.Go(func() {
		<-sessionStarted
		buf := make([]byte, 32*1024)
		for {
			n, readErr := stdinR.Read(buf)
			if n > 0 {
				log.With("sid", h.sid, "conn", h.connID, "ch", channelID).
					Debugf("read %d bytes from client, forwarding to agent", n)
				data := sshtypes.Data{ChannelID: channelID, Payload: buf[:n]}
				if _, writeErr := h.streamW.Write(data.Encode()); writeErr != nil {
					h.cancelFn("failed writing client data to agent, err=%v", writeErr)
					return
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					log.With("sid", h.sid, "conn", h.connID, "ch", channelID).
						Debugf("error reading from client: %v", readErr)
				} else {
					log.With("sid", h.sid, "conn", h.connID, "ch", channelID).
						Debugf("client closed write side (EOF), sending EOF to agent")
					eofData := sshtypes.EOF{ChannelID: channelID}
					if _, writeErr := h.streamW.Write(eofData.Encode()); writeErr != nil {
						log.With("sid", h.sid, "conn", h.connID, "ch", channelID).
							Debugf("failed sending EOF to agent: %v", writeErr)
					}
				}
				break
			}
		}
	})

	// Handle incoming requests from the client.
	h.channelWg.Go(func() {
		// released guards the one-time close of sessionStarted; only this
		// goroutine touches it, so no synchronization is required.
		released := !gateOnCommand
		release := func() {
			if !released {
				released = true
				close(sessionStarted)
			}
		}
		// unblock the stdin forwarder if the channel closes before any command
		// request arrives, so it can drain and exit rather than leak.
		defer release()

		for req := range clientRequests {
			log.With("sid", h.sid, "conn", h.connID, "ch", channelID, "type", req.Type).
				Debug("received client ssh request")

			data := (sshtypes.SSHRequest{
				ChannelID:   channelID,
				RequestType: req.Type,
				WantReply:   req.WantReply,
				Payload:     req.Payload,
			}).Encode()
			if _, err := h.streamW.Write(data); err != nil {
				h.cancelFn("failed writing to stream, err=%v", err)
				return
			}

			// The command request has now been forwarded ahead of any stdin
			// data: release the stdin forwarder (shell/exec start a session;
			// subsystem covers sftp).
			if gateOnCommand {
				switch req.Type {
				case "shell", "exec", "subsystem":
					release()
				}
			}

			if req.WantReply {
				replyChan := make(chan bool, 1)
				v, _ := h.pendingRequests.LoadOrStore(channelID, &pendingReplyQueue{})
				queue, ok := v.(*pendingReplyQueue)
				if !ok || queue == nil {
					log.With("sid", h.sid, "conn", h.connID, "ch", channelID).
						Warnf("pending reply queue missing or invalid, skipping")
					continue
				}
				queue.mu.Lock()
				queue.pending = append(queue.pending, &pendingSSHRequest{reply: replyChan})
				queue.mu.Unlock()

				go func(clientReq *ssh.Request, chID uint16) {
					select {
					case <-h.ctx.Done():
						return
					case ok := <-replyChan:
						if err := clientReq.Reply(ok, nil); err != nil {
							log.With("sid", h.sid, "conn", h.connID, "ch", chID, "type", clientReq.Type).
								Debugf("failed sending response to channel, err=%v", err)
						}
					case <-time.After(5 * time.Second):
						if err := clientReq.Reply(false, nil); err != nil {
							log.With("sid", h.sid, "conn", h.connID, "ch", chID, "type", clientReq.Type).
								Debugf("failed sending response to channel (timeout), err=%v", err)
						}
					}
				}(req, channelID)
			}
		}
		log.With("ch", channelID, "conn", h.connID).Debugf("done processing ssh client requests")
	})
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
	switch sshtypes.DecodeType(pkt.Payload) {
	case sshtypes.DataType:
		var data sshtypes.Data
		if err := sshtypes.Decode(pkt.Payload, &data); err != nil {
			h.cancelFn("failed decoding ssh data, err=%v", err)
			return
		}
		obj, _ := h.channels.Load(fmt.Sprintf("%v", data.ChannelID))
		clientCh, ok := obj.(io.WriteCloser)
		if !ok {
			h.cancelFn("local channel %q not found", data.ChannelID)
			return
		}
		log.With("sid", h.sid, "ch", data.ChannelID, "conn", h.connID).Debugf("received data type")
		if _, err := clientCh.Write(data.Payload); err != nil {
			h.cancelFn("failed writing ssh data, err=%v", err)
			return
		}

	case sshtypes.CloseChannelType:
		var cc sshtypes.CloseChannel
		if err := sshtypes.Decode(pkt.Payload, &cc); err != nil {
			h.cancelFn("failed decoding close channel, err=%v", err)
			return
		}
		obj, _ := h.channels.Load(fmt.Sprintf("%v", cc.ID))
		if clientCh, ok := obj.(io.Closer); ok {
			err := clientCh.Close()
			log.With("sid", h.sid, "ch", cc.ID, "conn", h.connID).
				Debugf("closing client ssh channel type=%v, err=%v", cc.Type, err)
		}

	case sshtypes.SSHRequestReplyType:
		var reply sshtypes.SSHRequestReply
		if err := sshtypes.Decode(pkt.Payload, &reply); err != nil {
			h.cancelFn("failed decoding ssh request reply, err=%v", err)
			return
		}
		log.With("sid", h.sid, "ch", reply.ChannelID, "conn", h.connID).
			Debugf("received ssh request reply, ok=%v", reply.OK)
		queue := h.loadPendingReplyQueue(reply.ChannelID)
		if queue == nil {
			log.With("sid", h.sid, "ch", reply.ChannelID, "conn", h.connID).
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
			log.With("sid", h.sid, "ch", reply.ChannelID, "conn", h.connID).
				Debugf("forwarded ssh request reply to client")
		case <-h.ctx.Done():
			return
		default:
			log.With("sid", h.sid, "ch", reply.ChannelID, "conn", h.connID).
				Infof("channel full or already handled (e.g. timeout), dropping request")
		}

	case sshtypes.ServerSSHRequestType:
		var serverReq sshtypes.ServerSSHRequest
		if err := sshtypes.Decode(pkt.Payload, &serverReq); err != nil {
			h.cancelFn("failed decoding server ssh request, err=%v", err)
			return
		}
		log.With("sid", h.sid, "ch", serverReq.ChannelID, "conn", h.connID).
			Debugf("received server ssh request type=%q, wantreply=%v, payload-length=%v",
				serverReq.RequestType, serverReq.WantReply, len(serverReq.Payload))
		obj, _ := h.channels.Load(fmt.Sprintf("%v", serverReq.ChannelID))
		clientCh, ok := obj.(ssh.Channel)
		if !ok {
			log.With("sid", h.sid, "ch", serverReq.ChannelID, "conn", h.connID).
				Warnf("local channel not found for server request")
			return
		}
		if _, err := clientCh.SendRequest(serverReq.RequestType, serverReq.WantReply, serverReq.Payload); err != nil {
			log.With("sid", h.sid, "ch", serverReq.ChannelID, "conn", h.connID).
				Debugf("failed sending server request to client: %v", err)
		}

	case sshtypes.EOFType:
		var eof sshtypes.EOF
		if err := sshtypes.Decode(pkt.Payload, &eof); err != nil {
			log.With("sid", h.sid, "ch", eof.ChannelID, "conn", h.connID).
				Infof("failed decoding ssh eof, err=%v", err)
			h.cancelFn("failed decoding ssh eof, err=%v", err)
			return
		}
		obj, _ := h.channels.Load(fmt.Sprintf("%v", eof.ChannelID))
		if ch, ok := obj.(interface{ CloseWrite() error }); ok {
			_ = ch.CloseWrite()
		}

	default:
		h.cancelFn("received unknown ssh message type (%v)", sshtypes.DecodeType(pkt.Payload))
	}
}

func (h *Handler) loadPendingReplyQueue(channelID uint16) *pendingReplyQueue {
	v, ok := h.pendingRequests.Load(channelID)
	if !ok {
		return nil
	}
	q, ok := v.(*pendingReplyQueue)
	if !ok {
		return nil
	}
	return q
}
