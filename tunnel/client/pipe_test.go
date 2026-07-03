package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// fakeTransport is an in-memory pb.ClientTransport. Anything the pipe
// Send()s is appended to sent for later inspection. Anything the test
// wants the pipe to Recv() is queued via push().
type fakeTransport struct {
	mu   sync.Mutex
	sent []*pb.Packet

	// recvQ delivers packets to Recv(). Closing it makes Recv return EOF.
	recvQ chan *pb.Packet

	// closed is closed when Close() is called.
	closed   chan struct{}
	closeOne sync.Once

	// recvErr, if set, is returned by Recv after recvQ drains.
	recvErr error
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		recvQ:   make(chan *pb.Packet, 16),
		closed:  make(chan struct{}),
		recvErr: io.EOF,
	}
}

func (f *fakeTransport) Send(p *pb.Packet) error {
	select {
	case <-f.closed:
		return errors.New("transport closed")
	default:
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy spec map so later mutations by the producer don't race readers.
	specCopy := make(map[string][]byte, len(p.Spec))
	for k, v := range p.Spec {
		specCopy[k] = append([]byte(nil), v...)
	}
	f.sent = append(f.sent, &pb.Packet{
		Type:    p.Type,
		Spec:    specCopy,
		Payload: append([]byte(nil), p.Payload...),
	})
	return nil
}

func (f *fakeTransport) Recv() (*pb.Packet, error) {
	select {
	case pkt, ok := <-f.recvQ:
		if !ok {
			return nil, f.recvErr
		}
		return pkt, nil
	case <-f.closed:
		return nil, errors.New("transport closed")
	}
}

func (f *fakeTransport) StreamContext() context.Context { return context.Background() }
func (f *fakeTransport) StartKeepAlive()                {}
func (f *fakeTransport) Close() (error, error) {
	f.closeOne.Do(func() { close(f.closed); close(f.recvQ) })
	return nil, nil
}

func (f *fakeTransport) push(p *pb.Packet) { f.recvQ <- p }

func (f *fakeTransport) sentPackets() []*pb.Packet {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*pb.Packet, len(f.sent))
	copy(out, f.sent)
	return out
}

// fakeLocal is an io.ReadWriteCloser backed by two byte buffers. The
// "client app" side (what the user app would write) pushes bytes via
// the toRead reader; the pipe reads from it as if it were a net.Conn.
// Bytes the pipe writes are appended to written.
type fakeLocal struct {
	mu       sync.Mutex
	toRead   *bytes.Buffer
	written  bytes.Buffer
	readWait chan struct{} // closed when toRead is exhausted
	closed   chan struct{}
	closeOne sync.Once
}

func newFakeLocal(toRead []byte) *fakeLocal {
	return &fakeLocal{
		toRead:   bytes.NewBuffer(toRead),
		readWait: make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (f *fakeLocal) Read(p []byte) (int, error) {
	f.mu.Lock()
	if f.toRead.Len() > 0 {
		n, err := f.toRead.Read(p)
		f.mu.Unlock()
		return n, err
	}
	// Mark drained, then block until Close() so the pipe doesn't see
	// an immediate EOF — production code would only see EOF when the
	// client app actually closed its socket.
	select {
	case <-f.readWait:
	default:
		close(f.readWait)
	}
	f.mu.Unlock()
	<-f.closed
	return 0, io.EOF
}

func (f *fakeLocal) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written.Write(p)
}

func (f *fakeLocal) Close() error {
	f.closeOne.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeLocal) writtenBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.written.Bytes()...)
}

// waitForSent blocks until at least n packets have been Send()ed to the
// transport, or the deadline elapses.
func waitForSent(t *testing.T, ft *fakeTransport, n int, within time.Duration) []*pb.Packet {
	t.Helper()
	deadline := time.After(within)
	for {
		pkts := ft.sentPackets()
		if len(pkts) >= n {
			return pkts
		}
		select {
		case <-deadline:
			t.Fatalf("expected %d packets within %s, got %d", n, within, len(pkts))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// --- tests ---

func TestRunPipe_HappyPath_PostgresEcho(t *testing.T) {
	ft := newFakeTransport()
	const sessionID = "sess-123"
	const wantClientBytes = "PING"
	const wantServerBytes = "PONG"

	local := newFakeLocal([]byte(wantClientBytes))

	// Drive the gateway's side of the protocol in a goroutine.
	go func() {
		// Step 1: pipe will Send SessionOpen. Wait for it.
		pkts := waitForSent(t, ft, 1, 2*time.Second)
		if pb.PacketType(pkts[0].Type) != pbagent.SessionOpen {
			t.Errorf("first packet: want SessionOpen, got %s", pkts[0].Type)
		}

		// Step 2: respond with SessionOpenOK for a postgres connection.
		ft.push(&pb.Packet{
			Type: pbclient.SessionOpenOK,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID: []byte(sessionID),
				pb.SpecConnectionType:   []byte(pb.ConnectionTypePostgres.String()),
			},
		})

		// Step 3: pipe will Send the TCPConnectionWrite "open" packet
		// (with SpecTCPServerConnectKey). Then bytes from local.
		// Wait until both arrive: 1 SessionOpen + 1 open + 1+ payload.
		var openIdx = -1
		var payloadSeen bool
		deadline := time.After(2 * time.Second)
		for {
			pkts := ft.sentPackets()
			for i, p := range pkts {
				if pb.PacketType(p.Type) == pbagent.TCPConnectionWrite {
					if _, ok := p.Spec[pb.SpecTCPServerConnectKey]; ok && openIdx < 0 {
						openIdx = i
						continue
					}
					if string(p.Payload) == wantClientBytes {
						payloadSeen = true
					}
				}
			}
			if openIdx >= 0 && payloadSeen {
				break
			}
			select {
			case <-deadline:
				t.Errorf("timeout waiting for open + payload; got %d packets", len(pkts))
				return
			case <-time.After(5 * time.Millisecond):
			}
		}

		// Step 4: send a server response, then close the connection.
		ft.push(&pb.Packet{
			Type: pbclient.TCPConnectionWrite,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(sessionID),
				pb.SpecClientConnectionID: []byte(connectionIDOnPipe),
			},
			Payload: []byte(wantServerBytes),
		})
		ft.push(&pb.Packet{
			Type: pbclient.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID:   []byte(sessionID),
				pb.SpecClientConnectionID: []byte(connectionIDOnPipe),
			},
		})
	}()

	opts := PipeOptions{
		ConnectionName:     "pg-prod",
		SessionOpenTimeout: 2 * time.Second,
	}
	err := runPipe(context.Background(), ft, local, opts)
	if err != nil {
		t.Fatalf("runPipe returned error: %v", err)
	}

	// Verify the server bytes landed in local.
	if got := string(local.writtenBytes()); got != wantServerBytes {
		t.Errorf("local writes: want %q, got %q", wantServerBytes, got)
	}

	// Verify the pipe sent the right initial packets in order.
	pkts := ft.sentPackets()
	if len(pkts) < 3 {
		t.Fatalf("expected at least 3 sent packets, got %d", len(pkts))
	}
	if pb.PacketType(pkts[0].Type) != pbagent.SessionOpen {
		t.Errorf("packet[0]: want SessionOpen, got %s", pkts[0].Type)
	}
	if pb.PacketType(pkts[1].Type) != pbagent.TCPConnectionWrite {
		t.Errorf("packet[1]: want TCPConnectionWrite, got %s", pkts[1].Type)
	}
	if _, ok := pkts[1].Spec[pb.SpecTCPServerConnectKey]; !ok {
		t.Errorf("packet[1] missing SpecTCPServerConnectKey")
	}
	if string(pkts[1].Spec[pb.SpecGatewaySessionID]) != sessionID {
		t.Errorf("packet[1] sessionID: want %q, got %q", sessionID, pkts[1].Spec[pb.SpecGatewaySessionID])
	}

	// The pipe MUST signal close after local EOFs.
	sawClose := false
	for _, p := range pkts {
		if pb.PacketType(p.Type) == pbagent.TCPConnectionClose {
			sawClose = true
			break
		}
	}
	if !sawClose {
		t.Errorf("pipe did not send TCPConnectionClose to the gateway")
	}
}

func TestRunPipe_RejectedConnectionType(t *testing.T) {
	ft := newFakeTransport()
	go func() {
		_ = waitForSent(t, ft, 1, 2*time.Second)
		ft.push(&pb.Packet{
			Type: pbclient.SessionOpenOK,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID: []byte("sess-x"),
				pb.SpecConnectionType:   []byte(pb.ConnectionTypeSSH.String()),
			},
		})
	}()

	local := newFakeLocal(nil)
	defer local.Close()

	err := runPipe(context.Background(), ft, local, PipeOptions{
		ConnectionName:     "ssh-prod",
		SessionOpenTimeout: 2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for non-tunnelable connection type, got nil")
	}
	if got := err.Error(); !contains(got, "not tunnelable") {
		t.Errorf("error %q does not mention tunnelable", got)
	}
}

func TestRunPipe_AgentOffline(t *testing.T) {
	ft := newFakeTransport()
	go func() {
		_ = waitForSent(t, ft, 1, 2*time.Second)
		ft.push(&pb.Packet{Type: pbclient.SessionOpenAgentOffline})
	}()

	local := newFakeLocal(nil)
	defer local.Close()

	err := runPipe(context.Background(), ft, local, PipeOptions{
		ConnectionName:     "pg-prod",
		SessionOpenTimeout: 2 * time.Second,
	})
	if err == nil || !contains(err.Error(), "agent is offline") {
		t.Fatalf("want 'agent is offline' error, got %v", err)
	}
}

func TestRunPipe_SessionOpenTimeout(t *testing.T) {
	ft := newFakeTransport()
	// Never push anything; we expect the pipe to time out.

	local := newFakeLocal(nil)
	defer local.Close()

	start := time.Now()
	err := runPipe(context.Background(), ft, local, PipeOptions{
		ConnectionName:     "pg-prod",
		SessionOpenTimeout: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !contains(err.Error(), "timeout") {
		t.Errorf("error %q does not mention timeout", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("timeout took too long: %s", elapsed)
	}
}

func TestRunPipe_ReviewRequired(t *testing.T) {
	ft := newFakeTransport()
	go func() {
		_ = waitForSent(t, ft, 1, 2*time.Second)
		ft.push(&pb.Packet{
			Type:    pbclient.SessionOpenWaitingApproval,
			Payload: []byte("review url here"),
		})
	}()

	local := newFakeLocal(nil)
	defer local.Close()

	err := runPipe(context.Background(), ft, local, PipeOptions{
		ConnectionName:     "pg-prod",
		SessionOpenTimeout: 2 * time.Second,
	})
	if err == nil || !contains(err.Error(), "requires review") {
		t.Fatalf("want 'requires review' error, got %v", err)
	}
}

// small helper since strings.Contains is the simplest readable check
// and the test file otherwise has zero strings imports.
func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
