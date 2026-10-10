package wasm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"
)

// instance is one instantiated module: one connection under
// per_connection, every connection of the lane under per_lane.
//
// Every export call goes through runLocked under mu. ABI.md promises the
// guest that calls are serialized; the mutex is what keeps that promise
// when the proxy pumps both directions of one connection from two
// goroutines, and when per_lane codecs for several connections share this
// instance.
type instance struct {
	p   *Plugin
	mod api.Module
	mem api.Memory
	fns map[string]api.Function

	mu   sync.Mutex
	dead error

	// Per-call state the host imports read through the call's context.
	// conn tags log lines; mask is non-nil only inside rewrite and flush,
	// which is how a mask import anywhere else becomes a trap; hostErr
	// carries the reason a host import aborted the call, so the error the
	// codec reports names the rule rather than wazero's panic text.
	conn    uint32
	mask    func(column string, value []byte) []byte
	hostErr error
}

// ctxKey carries the *instance to its host imports. The host module is
// built once per runtime and serves every instance of the plug-in, so the
// calling instance has to travel with the call.
type ctxKey struct{}

// errDead is the base of every error a dead instance reports.
var errDead = errors.New("codec plug-in instance is closed")

// instantiate builds one instance from the plug-in's compiled module and
// runs _initialize when the module exports it. The module is closed again
// on any error, so a failed instance holds no memory.
func (p *Plugin) instantiate(ctx context.Context) (*instance, error) {
	cfg := wazero.NewModuleConfig().
		WithName(""). // anonymous: per_connection instantiates the same module many times
		WithStartFunctions()
	if p.manifest.WASI {
		// No FS, no env, no args: the defaults. Clocks and randomness are
		// the one thing a sandboxed codec may legitimately want (a
		// per-connection nonce, a timestamp in a log line).
		cfg = cfg.WithSysWalltime().WithSysNanotime().WithRandSource(randReader).
			WithStdout(p.wasiLog(1)).WithStderr(p.wasiLog(2))
	}
	mod, err := p.runtime.InstantiateModule(ctx, p.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: instantiate: %w", p.name(), err)
	}
	in := &instance{p: p, mod: mod, mem: mod.ExportedMemory(expMemory), fns: map[string]api.Function{}}
	for name := range p.shape.exports {
		in.fns[name] = mod.ExportedFunction(name)
	}
	if in.fns[expInitialize] != nil {
		if _, err := in.call(0, expInitialize); err != nil {
			in.close()
			return nil, err
		}
	}
	return in, nil
}

// The four call shapes of the ABI. call takes scalar arguments only;
// callIn appends an input region as the trailing (ptr, len) pair; callOut
// reads and frees the packed (ptr << 32 | len) result; callInOut does
// both. Each holds the instance lock for the whole call.
func (in *instance) call(conn uint32, name string, args ...uint64) ([]uint64, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	res, _, err := in.runLocked(conn, name, nil, false, false, args...)
	return res, err
}

func (in *instance) callIn(conn uint32, name string, input []byte, args ...uint64) ([]uint64, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	res, _, err := in.runLocked(conn, name, input, true, false, args...)
	return res, err
}

func (in *instance) callOut(conn uint32, name string, args ...uint64) ([]byte, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	_, out, err := in.runLocked(conn, name, nil, false, true, args...)
	return out, err
}

func (in *instance) callInOut(conn uint32, name string, input []byte, args ...uint64) ([]byte, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	_, out, err := in.runLocked(conn, name, input, true, true, args...)
	return out, err
}

// runLocked calls export name under one deadline. With hasInput the input
// is placed in guest memory and passed as the trailing (ptr, len) pair,
// (0, 0) when empty so the guest never allocates for nothing; packed says
// the export returns a (ptr << 32 | len) u64 the host must read and free.
//
// Any error from wazero — a trap, the deadline, a module closed by
// proc_exit — kills the instance: the guest's memory is in a state nobody
// can reason about, and ABI.md says the connection drops. Every later call
// returns the same error so a caller that ignores one failure cannot keep
// feeding a corpse.
func (in *instance) runLocked(conn uint32, name string, input []byte, hasInput, packed bool, args ...uint64) (results []uint64, out []byte, err error) {
	if in.dead != nil {
		return nil, nil, in.dead
	}
	fn := in.fns[name]
	if fn == nil {
		// Only reachable through a host bug: every caller checks the
		// capability before dispatching. Loud rather than nil.
		return nil, nil, in.die(fmt.Errorf("%s: export %q is not present", in.p.name(), name))
	}

	ctx, cancel := context.WithTimeout(in.p.ctx, in.p.timeout)
	defer cancel()
	ctx = context.WithValue(ctx, ctxKey{}, in)
	in.conn, in.hostErr = conn, nil

	if hasInput {
		var ptr uint32
		if len(input) > 0 {
			if ptr, err = in.alloc(ctx, uint32(len(input))); err != nil {
				return nil, nil, in.die(fmt.Errorf("%s: alloc for %s: %w", in.p.name(), name, err))
			}
			if !in.mem.Write(ptr, input) {
				return nil, nil, in.die(fmt.Errorf("%s: alloc returned %d for %d bytes, outside guest memory", in.p.name(), ptr, len(input)))
			}
			defer func() {
				// Free after the call, as the ABI promises; skipped once
				// the instance is dead, because its module is closed.
				if in.dead != nil {
					return
				}
				if _, ferr := in.fns[expFree].Call(ctx, uint64(ptr), uint64(len(input))); ferr != nil && err == nil {
					results, out = nil, nil
					err = in.die(fmt.Errorf("%s: free after %s: %w", in.p.name(), name, ferr))
				}
			}()
		}
		args = append(args, uint64(ptr), uint64(len(input)))
	}

	results, err = fn.Call(ctx, args...)
	if err != nil {
		return nil, nil, in.die(in.callError(name, err))
	}
	if !packed {
		return results, nil, nil
	}
	out, err = in.readPacked(ctx, name, results[0])
	if err != nil {
		return nil, nil, in.die(err)
	}
	return results, out, nil
}

// callError turns wazero's error into one that names the ABI rule broken.
// A trap's stack trace goes to the log at debug and not into the error:
// the error becomes the deny message the client reads and one line of a
// warning, and neither has room for a wasm backtrace.
func (in *instance) callError(name string, err error) error {
	if in.hostErr != nil {
		return fmt.Errorf("%s: %s: %w", in.p.name(), name, in.hostErr)
	}
	var exit *sys.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case sys.ExitCodeDeadlineExceeded:
			return fmt.Errorf("%s: %s exceeded call_timeout_ms (%s)", in.p.name(), name, in.p.timeout)
		case sys.ExitCodeContextCanceled:
			return fmt.Errorf("%s: %s: plug-in closed during the call", in.p.name(), name)
		}
		return fmt.Errorf("%s: %s: guest exited with code %d", in.p.name(), name, exit.ExitCode())
	}
	msg := err.Error()
	if first, rest, cut := strings.Cut(msg, "\n"); cut {
		in.p.logger().Debug("codec plug-in trap", "protocol", in.p.name(), "conn", in.conn, "export", name, "detail", rest)
		msg = first
	}
	return fmt.Errorf("%s: %s: %s", in.p.name(), name, msg)
}

// die records the first fatal error and closes the module. The call that
// died gets the cause; every later call gets the cause behind errDead,
// so a log reads "closed: <why>" rather than the same trap again.
func (in *instance) die(err error) error {
	if in.dead != nil {
		return in.dead
	}
	in.dead = fmt.Errorf("%w: %w", errDead, err)
	in.p.logger().Error("codec plug-in instance closed", "protocol", in.p.name(), "conn", in.conn, "error", err)
	_ = in.mod.Close(context.WithoutCancel(in.p.ctx))
	return err
}

// close releases the instance. A call in flight on another goroutine is
// terminated by the module closing (WithCloseOnContextDone), and every
// later call reports the instance closed.
func (in *instance) close() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.dead == nil {
		in.dead = errDead
		_ = in.mod.Close(context.WithoutCancel(in.p.ctx))
	}
}

// isDead reports whether a call would fail. Used by per_lane codecs to
// tell the plug-in it must rebuild the shared instance.
func (in *instance) isDead() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.dead != nil
}

// alloc asks the guest for n bytes. Called for export input and, from the
// host imports, for import output.
func (in *instance) alloc(ctx context.Context, n uint32) (uint32, error) {
	res, err := in.fns[expAlloc].Call(ctx, uint64(n))
	if err != nil {
		return 0, err
	}
	return uint32(res[0]), nil
}

// readPacked copies the (ptr << 32 | len) region out of guest memory and
// frees it. 0 means no output. A region outside memory is fatal: the guest
// returned a pointer it does not own.
func (in *instance) readPacked(ctx context.Context, name string, v uint64) ([]byte, error) {
	if v == 0 {
		return nil, nil
	}
	ptr, n := uint32(v>>32), uint32(v)
	view, ok := in.mem.Read(ptr, n)
	if !ok {
		return nil, fmt.Errorf("%s: %s returned (%d, %d), outside guest memory", in.p.name(), name, ptr, n)
	}
	// Copy before free: the view aliases guest memory the guest is about to
	// reuse, and memory.grow may move it.
	out := make([]byte, n)
	copy(out, view)
	if _, err := in.fns[expFree].Call(ctx, uint64(ptr), uint64(n)); err != nil {
		return nil, fmt.Errorf("%s: free after %s: %w", in.p.name(), name, err)
	}
	return out, nil
}

// writeOut places data in guest memory for an import's return value and
// packs it. The guest frees it. Used only by host imports, on the stack of
// the export that called them, so ctx carries the call's deadline.
func (in *instance) writeOut(ctx context.Context, data []byte) (uint64, error) {
	if len(data) == 0 {
		return 0, nil
	}
	ptr, err := in.alloc(ctx, uint32(len(data)))
	if err != nil {
		return 0, err
	}
	if !in.mem.Write(ptr, data) {
		return 0, fmt.Errorf("alloc returned %d for %d bytes, outside guest memory", ptr, len(data))
	}
	return uint64(ptr)<<32 | uint64(len(data)), nil
}
