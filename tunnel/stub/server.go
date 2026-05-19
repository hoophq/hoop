// Package stub implements a minimal tunnel gateway suitable only for the
// RD-176 spike. It accepts a WebSocket upgrade, hands the connection to a
// tunnel client.Session, and forwards every opened stream to a hardcoded
// target address looked up by connection name.
//
// This is NOT a real gateway. There is no auth, no DB, no plugin pipeline,
// no audit. Use it for local end-to-end demos only.
package stub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/tunnel/client"
)

// Target is the dial address for a single tunneled connection name.
type Target struct {
	// Address is "host:port" — anything net.Dial("tcp", ...) accepts.
	Address string
	// Network defaults to "tcp" when empty.
	Network string
}

// Server is an http.Handler that upgrades incoming requests to the tunnel
// WebSocket protocol and forwards streams to its configured targets.
type Server struct {
	mu       sync.RWMutex
	targets  map[string]Target
	upgrader websocket.Upgrader
	logger   *log.Logger
}

// NewServer returns a Server with the given target map. The map is copied
// so the caller may mutate the original.
func NewServer(targets map[string]Target, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	t := make(map[string]Target, len(targets))
	for k, v := range targets {
		if v.Network == "" {
			v.Network = "tcp"
		}
		t[k] = v
	}
	return &Server{
		targets: t,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  16 * 1024,
			WriteBufferSize: 16 * 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
		logger: logger,
	}
}

// SetTarget adds or replaces a target. Safe for concurrent use.
func (s *Server) SetTarget(name string, t Target) {
	if t.Network == "" {
		t.Network = "tcp"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets[name] = t
}

// Names returns a snapshot of every configured target name. Useful for the
// /connections endpoint stub.
func (s *Server) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.targets))
	for n := range s.targets {
		out = append(out, n)
	}
	return out
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/tunnel/connections":
		s.serveConnections(w, r)
		return
	case "/api/tunnel":
		s.serveTunnel(w, r)
		return
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveConnections(w http.ResponseWriter, r *http.Request) {
	names := s.Names()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "[")
	for i, n := range names {
		if i > 0 {
			_, _ = io.WriteString(w, ",")
		}
		// Hand-rolled JSON to avoid pulling in encoding/json's reflection
		// path; keeps the stub allocation profile flat. Names are
		// validated by the allocator before they're set on the server.
		_, _ = fmt.Fprintf(w, "{\"name\":%q}", n)
	}
	_, _ = io.WriteString(w, "]")
}

func (s *Server) serveTunnel(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote a response.
		return
	}
	sess := client.NewServerSession(conn)
	sess.SetStreamHandler(s.handleStream)
	<-sess.Context().Done()
}

func (s *Server) handleStream(st *client.Stream) {
	defer st.Close()
	s.mu.RLock()
	t, ok := s.targets[st.Name()]
	s.mu.RUnlock()
	if !ok {
		s.logger.Printf("stub: unknown target %q", st.Name())
		return
	}
	upstream, err := dialWithTimeout(t, 10*time.Second)
	if err != nil {
		s.logger.Printf("stub: dial %s %s: %v", t.Network, t.Address, err)
		return
	}
	defer upstream.Close()

	bridge(context.Background(), st, upstream)
}

func dialWithTimeout(t Target, d time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: d}
	return dialer.Dial(t.Network, t.Address)
}

// bridge shuttles bytes both ways between the tunnel stream and the upstream
// TCP socket. It returns when either side closes.
func bridge(ctx context.Context, st *client.Stream, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	// upstream -> stream
	go func() {
		defer wg.Done()
		_, err := io.Copy(st, upstream)
		_ = st.CloseWrite()
		if err != nil && !isClosed(err) {
			// Swallow — half-closes look like errors at io.Copy.
		}
	}()
	// stream -> upstream
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstream, st)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	wg.Wait()
}

func isClosed(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	return false
}
