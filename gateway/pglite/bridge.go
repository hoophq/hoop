package pglite

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/hoophq/hoop/common/log"
)

// serveLoop accepts client connections and proxies them to the embedded
// backend, one session at a time. PGlite runs a single-user Postgres
// backend, so wire-protocol sessions cannot interleave: a new connection is
// only accepted after the previous one ends. Consumers must therefore keep
// at most one open connection (the gateway caps its pool accordingly);
// otherwise the extra connection simply waits in the accept queue.
func (i *Instance) serveLoop() {
	for {
		conn, err := i.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Warnf("pglite bridge accept failed, err=%v", err)
			return
		}
		if err := i.serveConn(context.Background(), conn); err != nil {
			log.Warnf("pglite bridge session ended with error, err=%v", err)
		}
	}
}

// serveConn pumps one client session: split the inbound byte stream into
// complete wire-protocol messages, hand them to the backend, write the
// response back. The Postgres protocol is strictly client-initiated in
// single-user mode, so a read-exchange-write loop is sufficient.
//
// Terminate ('X') messages are filtered out: for a single-user backend,
// Terminate means "shut the database down", but bridge sessions are
// transient client connections — the database must outlive them.
func (i *Instance) serveConn(ctx context.Context, conn net.Conn) error {
	defer conn.Close()

	// A previous session may have taken the backend down (trap or guest
	// exit); restore it before serving.
	if err := i.ensureModule(ctx); err != nil {
		return fmt.Errorf("backend unavailable: %w", err)
	}
	// Isolate sessions: drop output a previous session left behind.
	i.discardStaleOutput()

	// The backend session outlives bridge sessions, so it must be idle when
	// a client disconnects. txStatus tracks the transaction-status byte of
	// the backend's ReadyForQuery messages: 'I' idle, 'T' in transaction,
	// 'E' failed transaction.
	txStatus := byte('I')
	failedTxExchanges := 0
	defer func() {
		if txStatus != 'I' {
			// The session ended inside a transaction (possibly one the
			// guest cannot roll back, see below). Discard it by rebooting
			// the backend — for an uncommitted transaction this is
			// semantically a rollback.
			log.Warnf("pglite bridge session ended in transaction state %q, scheduling backend reboot", txStatus)
			i.rebootBackend(ctx)
		}
	}()

	var splitter frameSplitter
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		frames := splitter.push(buf[:n])
		if len(frames) == 0 {
			continue
		}
		reply, err := i.exchange(ctx, frames)
		// Deliver whatever the backend produced even when the exchange
		// failed: SQL errors surface as an ErrorResponse the client must
		// receive (GORM error translation depends on the SQLSTATE).
		if len(reply) > 0 {
			if _, werr := conn.Write(reply); werr != nil && err == nil {
				return fmt.Errorf("write: %w", werr)
			}
		}
		if err != nil {
			return err
		}
		if st := lastReadyStatus(reply); st != 0 {
			txStatus = st
			if st == 'E' {
				failedTxExchanges++
			} else {
				failedTxExchanges = 0
			}
		}
		// A failed transaction can only be rolled back, but this build's
		// backend cannot always do it (statement errors can leave an
		// active portal behind, making ROLLBACK fail and the session
		// answer 25P02 forever). A client always rolls back upon 'E', so
		// still being in 'E' after a further exchange means the backend is
		// stuck: end the session; the deferred check reboots the backend,
		// which discards the failed transaction.
		if failedTxExchanges >= 2 {
			return fmt.Errorf("backend is stuck in a failed transaction, resetting the session")
		}
	}
}

// rebootBackend closes the wasm module so the next session boots a fresh
// backend resuming the cluster.
func (i *Instance) rebootBackend(ctx context.Context) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.mod != nil && !i.mod.IsClosed() {
		_ = i.mod.Close(ctx)
	}
}

// lastReadyStatus walks the server message stream and returns the
// transaction-status byte of the last ReadyForQuery ('Z') message, or zero
// when the reply contains none.
func lastReadyStatus(reply []byte) byte {
	var status byte
	for len(reply) >= 5 {
		n := int(binary.BigEndian.Uint32(reply[1:5])) + 1
		if n < 5 || n > len(reply) {
			break
		}
		if reply[0] == 'Z' && n == 6 {
			status = reply[5]
		}
		reply = reply[n:]
	}
	return status
}

// discardStaleOutput drops any backend output left over from a previous
// session so it cannot bleed into the next one.
func (i *Instance) discardStaleOutput() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if stale, _ := i.drainOutput(); len(stale) > 0 {
		log.Debugf("pglite bridge discarded %d stale output bytes", len(stale))
	}
}

// ensureModule reboots the wasm module if a previous session left it
// closed. Booting on an existing data directory resumes the cluster.
func (i *Instance) ensureModule(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return errors.New("instance is closed")
	}
	if i.mod != nil && !i.mod.IsClosed() {
		return nil
	}
	log.Warn("pglite backend is down, rebooting wasm module")
	return i.bootModuleLocked(ctx)
}

// exchange forwards wire-protocol bytes to the backend through the
// socket-file transport and returns the raw response bytes.
//
// Transport contract (mirrors the upstream reference hosts): write the
// payload to .lock.in and atomically rename it to .in so the guest sees a
// complete frame, tick interactive_one() to process it, then drain .out
// (multiple frames may be emitted for a single tick).
func (i *Instance) exchange(ctx context.Context, payload []byte) ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return nil, errors.New("instance is closed")
	}

	slock, sin := i.ioBase+".lock.in", i.ioBase+".in"
	if err := os.WriteFile(slock, payload, 0o600); err != nil {
		return nil, fmt.Errorf("write input frame: %w", err)
	}
	if err := os.Rename(slock, sin); err != nil {
		return nil, fmt.Errorf("publish input frame: %w", err)
	}

	var tickErr error
	if _, err := i.interactiveOne.Call(ctx); err != nil {
		_ = os.Remove(sin)
		tickErr = err
	}

	reply, drainErr := i.drainOutput()

	if tickErr != nil {
		if i.mod.IsClosed() {
			// Guest exited; the module is rebooted before the next session.
			return reply, fmt.Errorf("interactive_one: %w", tickErr)
		}
		// A statement-level ereport(ERROR) aborts the tick via a wasm trap
		// and the guest only flushes the ErrorResponse on a later tick:
		// clear the error state, tick once with no input to let it flush,
		// and hand the response to the caller.
		if i.clearError != nil {
			_, _ = i.clearError.Call(ctx)
		}
		if _, err := i.interactiveOne.Call(ctx); err != nil && i.mod.IsClosed() {
			return reply, fmt.Errorf("interactive_one: %w", tickErr)
		}
		flushed, _ := i.drainOutput()
		reply = append(reply, flushed...)
		// After an error the backend skips input until it sees Sync, and
		// the rest of the client's pipeline was dropped with the failed
		// frame. Synthesize the Sync ourselves and hand its ReadyForQuery
		// to the client: the exchange completes exactly like a regular
		// statement error and the session stays usable.
		reply = append(reply, i.sendFrameLocked(ctx, []byte{'S', 0, 0, 0, 4})...)
		return reply, nil
	}
	if drainErr != nil {
		return reply, drainErr
	}

	if i.pglClosed != nil && !i.mod.IsClosed() {
		if ret, err := i.pglClosed.Call(ctx); err == nil && len(ret) == 1 && ret[0] == 0 {
			// The backend reported a closed session state; the module will
			// be rebooted before the next session.
			_ = i.mod.Close(ctx)
		}
	}
	return reply, nil
}

// sendFrameLocked pushes one bridge-originated wire-protocol frame to the
// backend and returns its response, best-effort. Callers must hold i.mu.
func (i *Instance) sendFrameLocked(ctx context.Context, frame []byte) []byte {
	slock, sin := i.ioBase+".lock.in", i.ioBase+".in"
	if err := os.WriteFile(slock, frame, 0o600); err != nil {
		return nil
	}
	if err := os.Rename(slock, sin); err != nil {
		return nil
	}
	if _, err := i.interactiveOne.Call(ctx); err != nil {
		if i.clearError != nil && !i.mod.IsClosed() {
			_, _ = i.clearError.Call(ctx)
		}
		_ = os.Remove(sin)
	}
	reply, _ := i.drainOutput()
	return reply
}

// simpleQueryFrame encodes sql as a wire-protocol simple Query message.
func simpleQueryFrame(sql string) []byte {
	payload := append([]byte(sql), 0)
	frame := make([]byte, 0, 5+len(payload))
	frame = append(frame, 'Q')
	frame = binary.BigEndian.AppendUint32(frame, uint32(4+len(payload)))
	return append(frame, payload...)
}

func (i *Instance) drainOutput() ([]byte, error) {
	cout, clock := i.ioBase+".out", i.ioBase+".lock.out"
	var result []byte
	for {
		data, err := os.ReadFile(cout)
		if os.IsNotExist(err) {
			return result, nil
		}
		if err != nil {
			return result, fmt.Errorf("read output frame: %w", err)
		}
		result = append(result, data...)
		_ = os.Remove(cout)
		_ = os.Remove(clock)
	}
}

// frameSplitter splits the client byte stream into complete Postgres wire
// messages, dropping Terminate ('X') messages. The first message of a
// session (startup message) carries no type byte; every subsequent message
// is [type byte][int32 length][payload].
type frameSplitter struct {
	buf     []byte
	started bool
}

// push appends p to the internal buffer and returns the complete messages
// accumulated so far, excluding Terminate. Incomplete trailing data is kept
// for the next call.
func (f *frameSplitter) push(p []byte) []byte {
	f.buf = append(f.buf, p...)
	var out []byte
	for {
		if !f.started {
			if len(f.buf) < 4 {
				return out
			}
			n := int(binary.BigEndian.Uint32(f.buf[:4]))
			if n < 4 {
				// Malformed length; forward as-is and let the backend
				// produce the protocol error.
				out = append(out, f.buf...)
				f.buf = nil
				return out
			}
			if len(f.buf) < n {
				return out
			}
			out = append(out, f.buf[:n]...)
			f.buf = f.buf[n:]
			f.started = true
			continue
		}
		if len(f.buf) < 5 {
			return out
		}
		n := int(binary.BigEndian.Uint32(f.buf[1:5])) + 1
		if n < 5 {
			out = append(out, f.buf...)
			f.buf = nil
			return out
		}
		if len(f.buf) < n {
			return out
		}
		if f.buf[0] != 'X' { // filter Terminate
			out = append(out, f.buf[:n]...)
		}
		f.buf = f.buf[n:]
	}
}
