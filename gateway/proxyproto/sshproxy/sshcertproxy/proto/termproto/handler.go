package termproto

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"golang.org/x/crypto/ssh"
)

// Handler is the command-line terminal handler for a single proxy connection.
// It owns the gRPC transport and serves PTY or exec sessions over it.
type Handler struct {
	sid        string
	connID     string
	grpcClient pb.ClientTransport
	channels   sync.Map // fmt.Sprintf("%v", channelID) → ssh.Channel
	channelWg  sync.WaitGroup
	ctx        context.Context
	cancelFn   func(msg string, a ...any)
}

// OpenSession sends SessionOpen, waits for SessionOpenOK, starts the read
// loop, and returns the ready Handler. It takes ownership of grpcClient and
// will close it via Close.
func OpenSession(sid, connID string, grpcClient pb.ClientTransport, ctx context.Context, cancelFn func(msg string, a ...any)) (*Handler, error) {
	spec := map[string][]byte{
		pb.SpecGatewaySessionID:   []byte(sid),
		pb.SpecClientConnectionID: []byte(connID),
	}
	if err := grpcClient.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: spec,
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

// ServeSession serves an already-accepted SSH session channel.
//
// Three distinct modes based on pty-req presence and upstreamCommand:
//
//   - PTY + no args  → connect: interactive terminal (TerminalWriteStdin).
//   - PTY + args     → exec: args sent as ExecWriteStdin payload.
//   - no PTY         → exec: empty ExecWriteStdin payload (command runs one-shot).
func (h *Handler) ServeSession(
	clientCh ssh.Channel,
	clientRequests <-chan *ssh.Request,
	channelID uint16,
	preRequests []*ssh.Request,
	execRequest *ssh.Request,
	upstreamCommand string,
) error {
	h.channels.Store(fmt.Sprintf("%v", channelID), clientCh)
	spec := map[string][]byte{pb.SpecGatewaySessionID: []byte(h.sid)}

	isPTY := len(preRequests) > 0

	if isPTY && upstreamCommand == "" {
		// Interactive PTY session — extract initial window size from the first
		// pty-req. SSH wire: TERM(string), Width/cols(uint32), Height/rows(uint32),
		// XPixels(uint32), YPixels(uint32), Modes(string).
		var ptyPayload struct {
			Term              string
			Width, Height     uint32
			XPixels, YPixels  uint32
			Modes             string
		}
		if len(preRequests[0].Payload) > 0 {
			_ = ssh.Unmarshal(preRequests[0].Payload, &ptyPayload)
		}
		rows, cols := ptyPayload.Height, ptyPayload.Width
		if rows == 0 {
			rows = 24
		}
		if cols == 0 {
			cols = 80
		}
		resizeMsg := fmt.Sprintf("%d,%d,0,0", rows, cols)
		if _, err := pb.NewStreamWriter(h.grpcClient, pbagent.TerminalResizeTTY, spec).Write([]byte(resizeMsg)); err != nil {
			return fmt.Errorf("failed sending initial TerminalResizeTTY: %w", err)
		}
		if execRequest.WantReply {
			_ = execRequest.Reply(true, nil)
		}
		h.startPTYForwarding(clientCh, clientRequests, channelID, spec)
	} else {
		// Exec session (PTY+args or no PTY).
		if execRequest.WantReply {
			_ = execRequest.Reply(true, nil)
		}

		// Drain SSH requests in the background (e.g. window-change when -t was
		// set alongside args).
		h.channelWg.Go(func() {
			for req := range clientRequests {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		})

		// Collect stdin from the SSH channel. Piped input (echo ... | ssh ...)
		// arrives immediately and the channel's write-side closes (EOF) as soon
		// as the source is exhausted. For empty/interactive stdin nothing arrives
		// before the deadline and we proceed with whatever we have, keeping
		// command startup prompt.
		type stdinResult struct{ data []byte }
		stdinCh := make(chan stdinResult, 1)
		h.channelWg.Go(func() {
			data, _ := io.ReadAll(clientCh)
			stdinCh <- stdinResult{data}
		})

		var stdinData []byte
		select {
		case r := <-stdinCh:
			stdinData = r.data
		case <-time.After(100 * time.Millisecond):
		}

		// Build the exec payload: optional command followed by any piped stdin.
		var payload []byte
		if upstreamCommand != "" {
			payload = []byte(upstreamCommand + "\n")
		}
		payload = append(payload, stdinData...)

		if err := h.grpcClient.Send(&pb.Packet{
			Type:    pbagent.ExecWriteStdin,
			Payload: payload,
			Spec:    spec,
		}); err != nil {
			return fmt.Errorf("failed sending ExecWriteStdin: %w", err)
		}
	}
	return nil
}

func (h *Handler) startPTYForwarding(clientCh ssh.Channel, clientRequests <-chan *ssh.Request, channelID uint16, spec map[string][]byte) {
	stdinW := pb.NewStreamWriter(h.grpcClient, pbagent.TerminalWriteStdin, spec)

	// Send an initial enter keystroke to trigger the shell prompt, mirroring
	// what the hoop CLI terminal proxy does (client/proxy/terminal.go).
	_, _ = stdinW.Write([]byte{'\n'})

	h.channelWg.Go(func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := clientCh.Read(buf)
			if n > 0 {
				if _, writeErr := stdinW.Write(buf[:n]); writeErr != nil {
					h.cancelFn("term: failed forwarding stdin to agent, err=%v", writeErr)
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.With("sid", h.sid, "conn", h.connID, "ch", channelID).
						Debugf("error reading stdin from client: %v", err)
				}
				break
			}
		}
	})

	h.channelWg.Go(func() {
		for req := range clientRequests {
			if req.Type == "window-change" {
				// SSH wire: cols(uint32), rows(uint32), xpixels(uint32), ypixels(uint32)
				var wc struct{ Cols, Rows, XPixels, YPixels uint32 }
				if err := ssh.Unmarshal(req.Payload, &wc); err != nil {
					log.With("sid", h.sid, "conn", h.connID, "ch", channelID).
						Debugf("failed parsing window-change: %v", err)
				} else {
					resizeMsg := fmt.Sprintf("%d,%d,%d,%d", wc.Rows, wc.Cols, wc.XPixels, wc.YPixels)
					_, _ = pb.NewStreamWriter(h.grpcClient, pbagent.TerminalResizeTTY, spec).Write([]byte(resizeMsg))
				}
			}
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	})
}

func (h *Handler) readLoop() {
	for {
		pkt, err := h.grpcClient.Recv()
		if err != nil {
			h.cancelFn("term: received error from grpc, err=%v", err)
			return
		}
		if pkt == nil {
			h.cancelFn("term: received nil packet, closing connection")
			return
		}
		switch pb.PacketType(pkt.Type) {
		case pbclient.WriteStdout:
			h.writeToAllChannels(pkt.Payload, false)
		case pbclient.WriteStderr:
			h.writeToAllChannels(pkt.Payload, true)
		case pbclient.SessionClose:
			h.onSessionClose(pkt)
			return
		default:
			h.cancelFn("term: received unexpected packet type %v", pkt.Type)
			return
		}
	}
}

func (h *Handler) writeToAllChannels(data []byte, isStderr bool) {
	h.channels.Range(func(_, value any) bool {
		ch, ok := value.(ssh.Channel)
		if !ok {
			return true
		}
		var err error
		if isStderr {
			_, err = ch.Stderr().Write(data)
		} else {
			_, err = ch.Write(data)
		}
		if err != nil {
			log.With("sid", h.sid, "conn", h.connID).Debugf("failed writing to ssh channel: %v", err)
		}
		return true
	})
}

func (h *Handler) onSessionClose(pkt *pb.Packet) {
	exitCode := uint32(0)
	if codeBytes, ok := pkt.Spec[pb.SpecClientExitCodeKey]; ok {
		if n, err := strconv.Atoi(string(codeBytes)); err == nil && n >= 0 {
			exitCode = uint32(n)
		}
	}

	h.channels.Range(func(_, value any) bool {
		ch, ok := value.(ssh.Channel)
		if !ok {
			return true
		}
		exitPayload := ssh.Marshal(struct{ ExitStatus uint32 }{exitCode})
		_, _ = ch.SendRequest("exit-status", false, exitPayload)
		_ = ch.Close()
		return true
	})

	h.cancelFn("term: session closed by agent (exit=%d)", exitCode)
}

// AcceptAndServe is not applicable to command-line terminal connections;
// session channels are handled by ServeSession. This satisfies ChannelHandler.
func (h *Handler) AcceptAndServe(newCh ssh.NewChannel, _ uint16) error {
	_ = newCh.Reject(ssh.Prohibited, "hoop: direct-tcpip not supported on command-line connections")
	return fmt.Errorf("termproto.Handler does not support direct-tcpip channels")
}

// RangeChannels calls fn for each registered channel.
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
