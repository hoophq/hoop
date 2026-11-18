package proxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// An upstream still busy checkDrainTimeout after a check's deadline is closed.
const checkDrainTimeout = 2 * time.Second

var errCheckNotDrained = errors.New("the upstream did not finish the resource check; the connection was closed")

// The client pump writes a check while blocked in the gate; the server pump
// feeds the answer to the check instead of the gate.
type sidePath struct {
	s        *Server
	upstream net.Conn
	connCtx  context.Context
	end      context.CancelCauseFunc
	log      *slog.Logger

	// reading is set while the server pump waits in Read, so a check never
	// waits behind a slow client. wake tells a waiting check it was set.
	reading atomic.Bool
	wake    chan struct{}

	mu     sync.Mutex
	active *diversion
}

// readWait lets the server pump finish writing the previous answer.
const readWait = 100 * time.Millisecond

func newSidePath(s *Server, upstream net.Conn, connCtx context.Context, end context.CancelCauseFunc, log *slog.Logger) *sidePath {
	return &sidePath{s: s, upstream: upstream, connCtx: connCtx, end: end, log: log, wake: make(chan struct{}, 1)}
}

func (p *sidePath) enterRead() {
	p.reading.Store(true)
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *sidePath) leaveRead() { p.reading.Store(false) }

func (p *sidePath) waitReading(ctx context.Context) bool {
	if p.reading.Load() {
		return true
	}
	t := time.NewTimer(readWait)
	defer t.Stop()
	for {
		select {
		case <-p.wake:
			if p.reading.Load() {
				return true
			}
		case <-t.C:
			return p.reading.Load()
		case <-ctx.Done():
			return false
		case <-p.connCtx.Done():
			return false
		}
	}
}

type diversion struct {
	resp inspect.CheckResponse
	done chan error
}

func (p *sidePath) Exchange(ctx context.Context, req []byte, resp inspect.CheckResponse, cancel []byte) error {
	if !p.waitReading(ctx) {
		return policy.ErrCheckNotReady
	}
	p.mu.Lock()
	if p.active != nil || !p.reading.Load() {
		p.mu.Unlock()
		return policy.ErrCheckNotReady
	}
	d := &diversion{resp: resp, done: make(chan error, 1)}
	p.active = d
	p.mu.Unlock()

	if _, err := p.upstream.Write(req); err != nil {
		p.complete(d, err)
		p.end(errors.New("the upstream write of a resource check failed"))
		return err
	}
	select {
	case err := <-d.done:
		return err
	case <-p.connCtx.Done():
		return context.Cause(p.connCtx)
	case <-ctx.Done():
	}

	deadline := time.Now().Add(checkDrainTimeout)
	if len(cancel) > 0 {
		if err := p.cancel(cancel, deadline); err != nil {
			p.log.Debug("resource check cancel failed", "error", err)
		}
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case err := <-d.done:
		if err == nil {
			err = policy.ErrCheckCancelled
		}
		return err
	case <-p.connCtx.Done():
		return context.Cause(p.connCtx)
	case <-timer.C:
	}
	p.end(errCheckNotDrained)
	_ = p.upstream.Close()
	return errCheckNotDrained
}

// cancel waits for the server to close, so the cancel cannot reach a later
// statement.
func (p *sidePath) cancel(req []byte, deadline time.Time) error {
	ctx, stop := context.WithDeadline(p.connCtx, deadline)
	defer stop()
	conn, err := p.s.dialUpstream(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, conn)
	return err
}

// forward goes to the client; rest follows the check and goes through the gate.
func (p *sidePath) divert(chunk []byte) (forward, rest []byte, err error) {
	p.mu.Lock()
	d := p.active
	p.mu.Unlock()
	if d == nil {
		return nil, chunk, nil
	}
	fwd, used, done, err := d.resp.Feed(chunk)
	switch {
	case err != nil:
		p.complete(d, err)
		return fwd, nil, err
	case done:
		p.complete(d, nil)
		return fwd, chunk[used:], nil
	}
	return fwd, nil, nil
}

func (p *sidePath) abort(err error) {
	p.mu.Lock()
	d := p.active
	p.mu.Unlock()
	if d != nil {
		p.complete(d, err)
	}
}

func (p *sidePath) complete(d *diversion, err error) {
	p.mu.Lock()
	if p.active == d {
		p.active = nil
	}
	p.mu.Unlock()
	select {
	case d.done <- err:
	default:
	}
}
