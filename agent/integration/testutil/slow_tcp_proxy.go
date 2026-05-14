//go:build integration

package testutil

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// SlowTCPProxy is a TCP forwarder that can be paused on demand. It sits
// between the agent (as the SSH client) and the real sshd container,
// transparently bidirectional in the normal state.
//
// When Pause() is called, the proxy stops draining bytes from the
// agent-side connection to the upstream-side connection. Bytes pile up
// in the agent → libhoop → libhoop's writerQueueCh path; once that
// 100-message buffer fills, the agent's serverWriter.Write blocks. With
// synchronous packet dispatch (feature flag off), that blocks the
// entire recv loop and any other session on the agent stalls — which is
// exactly the customer's reported symptom.
//
// Resume() releases the buffered bytes back to the upstream.
//
// SlowTCPProxy is goroutine-safe. Stop() closes the listener and any
// active connections; the test's t.Cleanup hook calls it automatically.
type SlowTCPProxy struct {
	listener   net.Listener
	upstream   string // host:port
	paused     atomic.Bool
	resumeCh   chan struct{} // closed and replaced under mu on each Pause()
	mu         sync.Mutex
	activeConn []net.Conn
	wg         sync.WaitGroup
	stopped    atomic.Bool
}

// StartSlowTCPProxy starts a TCP forwarder on an ephemeral port and
// returns its host:port plus the proxy handle. Connections accepted on
// the proxy are forwarded bidirectionally to upstreamAddr. Use Pause/
// Resume to control whether agent→upstream bytes drain.
func StartSlowTCPProxy(t T, upstreamAddr string) *SlowTCPProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("slow-tcp-proxy: failed to listen: %v", err)
	}

	p := &SlowTCPProxy{
		listener: ln,
		upstream: upstreamAddr,
		resumeCh: make(chan struct{}),
	}

	p.wg.Add(1)
	go p.acceptLoop()

	t.Cleanup(func() {
		p.Stop()
	})
	return p
}

// Addr returns the listener address as host, port strings ready to be
// passed to BuildSSHEnvVars.
func (p *SlowTCPProxy) Addr() (host, port string) {
	addr := p.listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), fmt.Sprintf("%d", addr.Port)
}

// Pause stops draining agent→upstream bytes. Already-buffered bytes in
// the kernel's TCP receive buffer stay there; bytes the agent writes
// after Pause arrive at the proxy but are held until Resume.
//
// Idempotent; calling Pause while already paused is a no-op.
func (p *SlowTCPProxy) Pause() {
	p.paused.Store(true)
}

// Resume releases the proxy from a paused state. Any goroutines that
// were waiting on the resume channel wake up and drain the bytes that
// piled up during the pause.
//
// Idempotent.
func (p *SlowTCPProxy) Resume() {
	p.mu.Lock()
	if !p.paused.Swap(false) {
		p.mu.Unlock()
		return
	}
	old := p.resumeCh
	p.resumeCh = make(chan struct{})
	p.mu.Unlock()
	close(old)
}

// Stop terminates the proxy: closes the listener and every active
// connection it forwarded. Safe to call multiple times.
func (p *SlowTCPProxy) Stop() {
	if !p.stopped.CompareAndSwap(false, true) {
		return
	}
	_ = p.listener.Close()
	p.mu.Lock()
	conns := append([]net.Conn(nil), p.activeConn...)
	p.activeConn = nil
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	// Unblock any goroutines currently parked on resumeCh.
	p.Resume()
	p.wg.Wait()
}

func (p *SlowTCPProxy) acceptLoop() {
	defer p.wg.Done()
	for {
		downstream, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.trackConn(downstream)
		p.wg.Add(1)
		go p.handleConn(downstream)
	}
}

func (p *SlowTCPProxy) trackConn(c net.Conn) {
	p.mu.Lock()
	p.activeConn = append(p.activeConn, c)
	p.mu.Unlock()
}

func (p *SlowTCPProxy) handleConn(downstream net.Conn) {
	defer p.wg.Done()
	defer downstream.Close()

	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d := net.Dialer{}
	upstream, err := d.DialContext(dialCtx, "tcp", p.upstream)
	if err != nil {
		return
	}
	p.trackConn(upstream)
	defer upstream.Close()

	// Two unidirectional pumps. Upstream → downstream never pauses so
	// SSH replies can reach the agent during a paused state (the SSH
	// handshake itself must complete before a Pause() makes sense).
	// Downstream → upstream respects the pause and is where buffers
	// accumulate.
	//
	// Use a WaitGroup local to this connection so handleConn doesn't
	// return (and trigger the deferred Close) until both pumps finish.
	// Without this, the two goroutines outlive their connections and
	// the deferred Close fires immediately, resetting the SSH
	// handshake before any bytes flow.
	var pumpWg sync.WaitGroup
	pumpWg.Add(2)
	p.wg.Add(2)
	go func() {
		defer pumpWg.Done()
		p.copyOneWay(upstream, downstream, false /* respectPause */)
	}()
	go func() {
		defer pumpWg.Done()
		p.copyOneWay(downstream, upstream, true /* respectPause */)
	}()
	pumpWg.Wait()
}

// copyOneWay forwards bytes from src to dst. If respectPause is true,
// the loop waits on the proxy's resumeCh whenever paused is set before
// performing the dst.Write. This is the direction (agent → upstream)
// where we want backpressure to accumulate.
func (p *SlowTCPProxy) copyOneWay(src, dst net.Conn, respectPause bool) {
	defer p.wg.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if respectPause {
				for p.paused.Load() && !p.stopped.Load() {
					p.mu.Lock()
					ch := p.resumeCh
					p.mu.Unlock()
					<-ch
				}
				if p.stopped.Load() {
					return
				}
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			// Half-close the write side so the peer sees EOF instead of
			// the connection abruptly resetting. SSH treats RST as a
			// protocol error during teardown.
			if tc, ok := dst.(*net.TCPConn); ok {
				_ = tc.CloseWrite()
			}
			return
		}
	}
}


