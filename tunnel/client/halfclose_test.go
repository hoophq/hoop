package client

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

// halfClosedLocal models a client that sends a request and then shuts down
// only its write direction: reads return EOF immediately while the socket
// stays open for the response.
//
// fakeLocal cannot express this — its Read blocks until Close, so the pump
// never observes EOF while the connection is still live. That is exactly the
// state a real `psql`/`mysql` client reaches after issuing a query.
type halfClosedLocal struct {
	mu      sync.Mutex
	toRead  *bytes.Buffer
	written bytes.Buffer
	closed  chan struct{}
	once    sync.Once
}

func newHalfClosedLocal(request []byte) *halfClosedLocal {
	return &halfClosedLocal{
		toRead: bytes.NewBuffer(request),
		closed: make(chan struct{}),
	}
}

func (h *halfClosedLocal) Read(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.toRead.Len() > 0 {
		return h.toRead.Read(p)
	}
	return 0, io.EOF // write side shut down; socket still readable by peer
}

func (h *halfClosedLocal) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	return h.written.Write(p)
}

func (h *halfClosedLocal) Close() error {
	h.once.Do(func() { close(h.closed) })
	return nil
}

func (h *halfClosedLocal) writtenBytes() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]byte(nil), h.written.Bytes()...)
}

// A client that half-closes after sending its request must still receive the
// gateway's response: TCP EOF on the read side means "no more requests", not
// "stop listening".
//
// The pump must therefore keep draining gateway->local after local EOF, and
// finish only when the gateway signals the end of the exchange.
func TestRunPipe_HalfCloseStillDrainsResponse(t *testing.T) {
	ft := newFakeTransport()
	const sessionID = "sess-halfclose"
	const wantServerBytes = "LATE-RESPONSE"

	local := newHalfClosedLocal(pgSimpleQuery("SELECT 1"))

	go func() {
		_ = waitForSent(t, ft, 1, 2*time.Second)
		ft.push(&pb.Packet{
			Type: pbclient.SessionOpenOK,
			Spec: map[string][]byte{
				pb.SpecGatewaySessionID: []byte(sessionID),
				pb.SpecConnectionType:   []byte(pb.ConnectionTypePostgres.String()),
			},
		})

		// Wait for the half-close signal, so the response below is provably
		// sent *after* the local side stopped writing.
		deadline := time.After(3 * time.Second)
		for {
			var sawClose bool
			for _, p := range ft.sentPackets() {
				if pb.PacketType(p.Type) == pbagent.TCPConnectionClose {
					sawClose = true
				}
			}
			if sawClose {
				break
			}
			select {
			case <-deadline:
				t.Errorf("pipe never signalled the half-close to the gateway")
				return
			case <-time.After(2 * time.Millisecond):
			}
		}

		ft.push(&pb.Packet{
			Type: pbclient.PGConnectionWrite,
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

	if err := runPipe(context.Background(), ft, local, PipeOptions{
		ConnectionName:     "pg-prod",
		SessionOpenTimeout: 2 * time.Second,
	}); err != nil {
		t.Fatalf("runPipe returned error: %v", err)
	}

	if got := string(local.writtenBytes()); got != wantServerBytes {
		t.Fatalf("response arriving after half-close was dropped: want %q, got %q",
			wantServerBytes, got)
	}
}
