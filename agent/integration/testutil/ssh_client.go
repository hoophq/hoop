//go:build integration

package testutil

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	sshtypes "libhoop/proxy/ssh/types"

	"golang.org/x/crypto/ssh"
)

// PipedSSHClient runs an x/crypto/ssh.Client whose underlying transport
// is bridged to a MockTransport via net.Pipe and a sshtypes envelope
// translator. From the test's perspective it's a regular SSH client:
// New a session, run Exec or Shell, get output back.
//
// # Why this exists
//
// libhoop's SSH proxy speaks hoop's custom envelope on the agent ←→
// gateway wire, not raw SSH. So tests can't simply wrap MockTransport
// in a net.Conn and call ssh.NewClient — the agent would receive raw
// SSH bytes and reject them with "unknown packet type". The
// PipedSSHClient mirrors the client/proxy/ssh.go pattern from the
// real hoop CLI: it stands up an ssh.ServerConn on a net.Pipe, the
// test's SSH client connects to it as if it were any other SSH
// server, and the bridging layer translates SSH channel/request/data
// events into sshtypes envelopes shipped over the MockTransport.
//
// Wire flow
//
//	test                  PipedSSHClient                MockTransport / Agent
//	──────                ──────────────                 ────────────────────
//	ssh.NewSession()      SSH server-side translator     sshtypes.OpenChannel
//	  → channel open  ──► net.Pipe ──► ssh.ServerConn ──► encode ──► Inject()
//	                                                      │
//	  ← channel data   ◄── net.Pipe ◄── ssh.ServerConn ◄── decode ◄── Recv (demux)
//
// PipedSSHClient is not safe for use by multiple goroutines other
// than the parallelism naturally provided by *ssh.Session and *ssh.Channel
// (each test should drive one PipedSSHClient and use the SSH library's
// own multiplexing for concurrent channels).
type PipedSSHClient struct {
	// Client is the configured ssh.Client. Tests call Client.NewSession(),
	// Client.Dial(), etc. directly.
	Client *ssh.Client

	sessionID string
	connID    string
	tr        *MockTransport
	demux     *RecvDemux

	// pipe sides
	clientConn net.Conn // the test's SSH client side
	serverConn net.Conn // the translator's SSH server side

	// SSH server-side state
	srvConn *ssh.ServerConn

	// Bridge: maps hoop channel IDs ↔ ssh.Channel on the server side so
	// inbound Data packets can find the channel to write to.
	chanMu     sync.Mutex
	channels   map[uint16]*serverChannelState
	nextChanID atomic.Uint32

	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// serverChannelState tracks the SSH channel we accepted on the server
// side plus any pending request replies indexed by request type.
type serverChannelState struct {
	ch        ssh.Channel
	chType    string
	closeOnce sync.Once
}

// DialPipedSSH builds the net.Pipe-bridged SSH server/translator and
// returns a PipedSSHClient with a connected ssh.Client ready for the
// test to drive. The session must already be open at the agent (call
// OpenSSHSession before StartRecvDemux + DialPipedSSH).
//
// clientCfg drives the test's local SSH client (auth method, host-key
// callback). sshSrvCfg drives the translator's SSH server side (host
// key, auth callbacks); it can be nil for sensible defaults that
// accept any auth.
//
// Cleanup is automatic via t.Cleanup; tests don't need to defer Close.
func DialPipedSSH(t *testing.T, tr *MockTransport, demux *RecvDemux,
	sid, connID string,
	clientCfg *ssh.ClientConfig, sshSrvCfg *ssh.ServerConfig) (*PipedSSHClient, error) {
	t.Helper()

	// We use a real TCP loopback listener instead of net.Pipe because
	// net.Pipe is synchronous (Write blocks until the other side
	// Reads), which deadlocks SSH's exchangeVersions step: both sides
	// do Write(banner) then Read(banner) serially and neither
	// completes its Write because the other isn't yet Reading. A real
	// TCP socket has kernel-level send buffers that absorb the small
	// banner write without blocking.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("piped ssh listen: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &PipedSSHClient{
		sessionID: sid,
		connID:    connID,
		tr:        tr,
		demux:     demux,
		channels:  make(map[uint16]*serverChannelState),
		cancel:    cancel,
		done:      make(chan struct{}),
	}

	if sshSrvCfg == nil {
		sshSrvCfg = defaultSSHServerConfig(t)
	}

	// Server-side handshake runs in a goroutine because both sides of
	// an SSH handshake need to be reading/writing concurrently;
	// ssh.NewServerConn and ssh.NewClientConn each block until the
	// handshake completes.
	srvErrCh := make(chan error, 1)
	srvReadyCh := make(chan struct{})
	go func() {
		defer listener.Close()
		conn, err := listener.Accept()
		if err != nil {
			srvErrCh <- err
			close(srvReadyCh)
			return
		}
		p.serverConn = conn
		srvConn, newChans, reqs, err := ssh.NewServerConn(conn, sshSrvCfg)
		if err != nil {
			srvErrCh <- err
			close(srvReadyCh)
			return
		}
		p.srvConn = srvConn
		close(srvReadyCh)
		go ssh.DiscardRequests(reqs)
		p.serveNewChannels(ctx, newChans)
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("piped ssh dial loopback: %w", err)
	}
	p.clientConn = clientConn

	// The inbound-packet pump runs concurrently with the handshake so
	// the agent's SSHConnectionWrite responses (e.g. SSHRequestReply
	// after the client sends a request) reach the translator. The
	// handshake itself is in raw-SSH bytes over net.Pipe, not over
	// MockTransport, so the inbound pump matters only after the first
	// channel opens.
	go p.inboundPump(ctx)

	// Session-close watcher: when the agent ends the session (upstream
	// failure, guardrails block, or the OSS libhoop stub's "missing
	// protocol hoop library"), tear the piped transport down so every
	// blocked x/crypto/ssh call errors out immediately. Without this,
	// a SessionClose is invisible to the SSH client — it has no
	// per-connection ID so the demux never routes it here — and the
	// test hangs until the suite's global timeout (DEP-57).
	go func() {
		select {
		case <-ctx.Done():
		case <-demux.SessionCloseChan(sid):
			reason, _ := demux.SessionCloseReason(sid)
			// Not t.Logf: this goroutine can outlive the test body and
			// logging via t after completion panics. Stderr is safe and
			// still lands in the test output.
			fmt.Fprintf(os.Stderr, "testutil: agent closed session %s: %s — closing piped SSH transport\n", sid, reason)
			p.Close()
		}
	}()

	// Run the test's SSH client handshake. ssh.NewClientConn returns
	// after KEX + auth completes successfully (or fails).
	cConn, cChans, cReqs, err := ssh.NewClientConn(p.clientConn, "piped", clientCfg)
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("ssh client handshake: %w", err)
	}

	// Wait briefly for the server-side handshake to also complete so
	// p.srvConn is set before we return.
	select {
	case err := <-srvErrCh:
		p.Close()
		return nil, fmt.Errorf("ssh server-side handshake: %w", err)
	case <-srvReadyCh:
	case <-time.After(10 * time.Second):
		p.Close()
		return nil, fmt.Errorf("ssh server-side handshake: timed out")
	}

	p.Client = ssh.NewClient(cConn, cChans, cReqs)

	t.Cleanup(func() {
		p.Close()
	})
	return p, nil
}

// Close tears down both sides of the pipe and the inbound pump. Safe
// to call multiple times.
func (p *PipedSSHClient) Close() {
	p.closeOnce.Do(func() {
		p.cancel()
		if p.Client != nil {
			_ = p.Client.Close()
		}
		if p.srvConn != nil {
			_ = p.srvConn.Close()
		}
		_ = p.clientConn.Close()
		_ = p.serverConn.Close()
		close(p.done)
	})
}

// SessionID returns the agent-side session ID, useful for sending
// SessionClose packets in teardown tests.
func (p *PipedSSHClient) SessionID() string { return p.sessionID }

// ConnID returns the gateway-side connection ID this client occupies.
func (p *PipedSSHClient) ConnID() string { return p.connID }

// serveNewChannels handles each new channel request from the test's
// SSH client by accepting it on the server side, allocating a hoop
// channel ID, and forwarding it to the agent as sshtypes.OpenChannel.
// Once accepted, the channel's I/O is bridged to sshtypes.Data /
// sshtypes.SSHRequest packets in dedicated goroutines.
func (p *PipedSSHClient) serveNewChannels(ctx context.Context, newChans <-chan ssh.NewChannel) {
	for {
		select {
		case <-ctx.Done():
			return
		case newCh, ok := <-newChans:
			if !ok {
				return
			}
			p.handleNewChannel(ctx, newCh)
		}
	}
}

func (p *PipedSSHClient) handleNewChannel(ctx context.Context, newCh ssh.NewChannel) {
	chID := uint16(p.nextChanID.Add(1))
	chType := newCh.ChannelType()
	chExtra := newCh.ExtraData()

	srvCh, reqs, err := newCh.Accept()
	if err != nil {
		return
	}

	p.chanMu.Lock()
	p.channels[chID] = &serverChannelState{ch: srvCh, chType: chType}
	p.chanMu.Unlock()

	// Tell the agent to open the same channel against the upstream sshd.
	p.sendPacket((sshtypes.OpenChannel{
		ChannelID:        chID,
		ChannelType:      chType,
		ChannelExtraData: chExtra,
	}).Encode())

	// Pump channel data from the test's SSH client → sshtypes.Data
	// packets bound for the agent. We do NOT close srvCh on Read
	// returning EOF — closing the local channel would tear down the
	// channel on the test's side, and many tests open a channel and
	// only read from it (exec, port-forward) without writing any
	// stdin. EOF here just means "no more outbound stdin from the
	// test," which we signal to the upstream so commands see stdin
	// closed.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := srvCh.Read(buf)
			if n > 0 {
				select {
				case <-ctx.Done():
					return
				default:
				}
				p.sendPacket((sshtypes.Data{
					ChannelID: chID,
					Payload:   append([]byte(nil), buf[:n]...),
				}).Encode())
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					p.sendPacket((sshtypes.EOF{ChannelID: chID}).Encode())
				}
				return
			}
		}
	}()

	// Pump channel requests (pty-req, shell, exec, signal, etc.) as
	// sshtypes.SSHRequest packets bound for the agent.
	go func() {
		for req := range reqs {
			p.sendPacket((sshtypes.SSHRequest{
				ChannelID:   chID,
				RequestType: req.Type,
				WantReply:   req.WantReply,
				Payload:     req.Payload,
			}).Encode())
			// We reply locally with success because the real
			// reply will arrive asynchronously from the agent
			// as sshtypes.SSHRequestReply (which the inbound
			// pump consumes); x/crypto/ssh requires reply by
			// the time the request handler returns, so we
			// can't wait for the real one. This matches the
			// pattern in client/proxy/ssh.go.
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		}
	}()
}

// inboundPump consumes SSHConnectionWrite packets the agent emits on
// behalf of this connection and dispatches them to the right local
// SSH channel.
func (p *PipedSSHClient) inboundPump(ctx context.Context) {
	if p.demux == nil {
		return
	}
	ch := p.demux.Channel(p.connID)
	for {
		select {
		case <-ctx.Done():
			return
		case pkt, ok := <-ch:
			if !ok {
				return
			}
			p.handleInboundPacket(pkt)
		}
	}
}

func (p *PipedSSHClient) handleInboundPacket(pkt *pb.Packet) {
	if len(pkt.Payload) == 0 {
		return
	}
	switch sshtypes.DecodeType(pkt.Payload) {
	case sshtypes.DataType:
		var d sshtypes.Data
		if err := sshtypes.Decode(pkt.Payload, &d); err != nil {
			return
		}
		if state := p.lookupChannel(d.ChannelID); state != nil {
			_, _ = state.ch.Write(d.Payload)
		}
	case sshtypes.CloseChannelType:
		var cc sshtypes.CloseChannel
		if err := sshtypes.Decode(pkt.Payload, &cc); err != nil {
			return
		}
		p.chanMu.Lock()
		state := p.channels[cc.ID]
		delete(p.channels, cc.ID)
		p.chanMu.Unlock()
		if state != nil {
			state.closeOnce.Do(func() { _ = state.ch.Close() })
		}
	case sshtypes.EOFType:
		var eof sshtypes.EOF
		if err := sshtypes.Decode(pkt.Payload, &eof); err != nil {
			return
		}
		if state := p.lookupChannel(eof.ChannelID); state != nil {
			_ = state.ch.CloseWrite()
		}
	case sshtypes.ServerSSHRequestType:
		var sreq sshtypes.ServerSSHRequest
		if err := sshtypes.Decode(pkt.Payload, &sreq); err != nil {
			return
		}
		state := p.lookupChannel(sreq.ChannelID)
		if state == nil {
			return
		}
		// Forward the server's request (e.g. exit-status) to the
		// test's local SSH client side as a channel request.
		_, _ = state.ch.SendRequest(sreq.RequestType, sreq.WantReply, sreq.Payload)
	case sshtypes.SSHRequestReplyType:
		// We replied locally to wantReply=true when forwarding the
		// outbound request; the upstream's reply is informational
		// here. Dropping it is fine for tests that don't need to
		// distinguish ok=true vs ok=false on requests.
		return
	}
}

func (p *PipedSSHClient) lookupChannel(chID uint16) *serverChannelState {
	p.chanMu.Lock()
	defer p.chanMu.Unlock()
	return p.channels[chID]
}

// sendPacket injects an SSHConnectionWrite packet at the agent on
// behalf of the bridged SSH client. Guards against the test having
// already canceled the bridge (the agent shut down, MockTransport's
// recvCh is closed) — in that case, packets that would have been
// queued by a goroutine still mid-flight are silently dropped. The
// alternative is a process panic from "send on closed channel" inside
// tr.Inject during teardown.
func (p *PipedSSHClient) sendPacket(payload []byte) {
	select {
	case <-p.done:
		return
	default:
	}
	defer func() {
		// Last-ditch defense against the narrow window where Inject
		// races a transport Close that happens between the select
		// above and the channel send inside Inject.
		_ = recover()
	}()
	SendSSHWrite(noopT{}, p.tr, p.sessionID, p.connID, payload)
}

// defaultSSHServerConfig returns a minimal SSH server config suitable
// for the translator side of the pipe: any password / pubkey / none
// auth is accepted because the actual auth happens out at the real
// sshd container; the in-pipe handshake just needs to complete.
func defaultSSHServerConfig(t *testing.T) *ssh.ServerConfig {
	t.Helper()
	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("piped-ssh: failed to generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatalf("piped-ssh: failed to build host key signer: %v", err)
	}
	cfg := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	cfg.AddHostKey(signer)
	return cfg
}

// noopT is a minimal T implementation used internally by the bridge
// goroutines that call testutil helpers (which expect a T for
// Helper(), Fatalf(), and Cleanup()). The bridge is best-effort
// during teardown — if a packet inject fails we don't want to fail
// the test from a goroutine the test no longer owns.
type noopT struct{}

func (noopT) Helper()                           {}
func (noopT) Fatalf(format string, args ...any) {}
func (noopT) Cleanup(func())                    {}

// Compile-time assertion that noopT satisfies the T interface.
var _ T = noopT{}
