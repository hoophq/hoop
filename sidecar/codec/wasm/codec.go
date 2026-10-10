package wasm

import (
	"errors"
	"fmt"
	"sync"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// codec is one connection's view of the plug-in. It satisfies
// inspect.Codec and the optional capabilities the gate discovers by type
// assertion, with one exception: the rewrite pair lives on rewriteCodec,
// because gate.MaskSupportedBy must answer no for a plug-in that cannot
// rebuild its frames.
//
// Both directions of a connection share this one value (gate.Duplex): a
// guest keys its state by connection, not by direction, and the ABI
// promises it serialized calls, which the instance's lock provides.
type codec struct {
	p     *Plugin
	in    *instance
	conn  uint32
	owned bool // per_connection: Close releases the instance

	// err is set when the codec could not be attached (instantiate failed,
	// open refused). Every method reports it, so the gate fails the
	// connection with the reason rather than a nil-interface panic.
	err error

	mu     sync.Mutex
	closed bool
}

// The capability wrappers. deny, filter and credential degrade in place
// (a nil frame, the identity filter, ok=false), because the gate copes
// with each of those answers. rewrite and content cannot: the gate reads
// gate.Reframer as "this codec can mask" and refuses a masker otherwise,
// and the daemon reads analyzer.ContentRenderer as "this module renders
// its own analyzer input" and would otherwise fall back to the generic
// builder. So those two methods live on wrapper types the manifest
// selects, one per combination.
type rewriteCodec struct{ *codec }
type contentCodec struct{ *codec }
type rewriteContentCodec struct{ *rewriteCodec }

func (c *contentCodec) Content(stmt inspect.Statement, maxBytes int) (string, string, bool) {
	return c.content(stmt, maxBytes)
}

func (c *rewriteContentCodec) Content(stmt inspect.Statement, maxBytes int) (string, string, bool) {
	return c.content(stmt, maxBytes)
}

func dirCode(dir inspect.Direction) uint64 {
	if dir == inspect.FromServer {
		return 1
	}
	return 0
}
func (c *codec) Protocol() inspect.Protocol { return c.p.Protocol() }
func (c *codec) Label() string              { return c.p.Label() }
func (c *codec) Duplex()                    {}
func (c *codec) MaxReassemblyBytes() int    { return c.p.manifest.maxReassembly() }

// streamError is every error Decode returns.
//
// The gate's default for a decode error is to forward the bytes: for a
// shipped codec the upstream's parser is the authority on its own
// protocol, and a chunk the relay misread is still one the server can
// judge. ABI.md promises the opposite for a plug-in — a decode error, a
// trap or a timeout DROPS the connection — because the plug-in IS the
// only parser the relay has for that protocol, and bytes it could not
// read are bytes policy never saw. inspect.ErrStreamUnsafe is the one
// error the gate denies regardless of policy, so every decode failure
// matches it; the proxy then writes the deny frame and closes.
type streamError struct{ err error }

func (e *streamError) Error() string        { return e.err.Error() }
func (e *streamError) Unwrap() error        { return e.err }
func (e *streamError) Is(target error) bool { return target == inspect.ErrStreamUnsafe }
func (c *codec) fail(err error) error       { return &streamError{err} }

// Decode implements inspect.Codec over the guest's `decode` export.
func (c *codec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	if c.err != nil {
		return nil, 0, c.fail(c.err)
	}
	out, err := c.in.callInOut(c.conn, expDecode, data, uint64(c.conn), dirCode(dir))
	if err != nil {
		return nil, 0, c.fail(err)
	}
	if len(out) == 0 {
		// 0 is "no output" in the ABI, and decode always has something to
		// say, if only {"consumed": 0}. Treating silence as "nothing
		// consumed" would buffer a stream the guest cannot read until
		// max_reassembly, which is a slower way to drop the connection.
		return nil, 0, c.fail(fmt.Errorf("%s: decode returned no output", c.p.name()))
	}
	var res decodeResult
	if err := decodeStrict(out, &res); err != nil {
		return nil, 0, c.fail(fmt.Errorf("%s: decode result: %w", c.p.name(), err))
	}
	if res.Error != "" {
		// The guest's wording, verbatim: it becomes the deny message the
		// client reads, and the guest knows its protocol's vocabulary.
		return nil, 0, c.fail(errors.New(res.Error))
	}
	if res.Consumed < 0 || res.Consumed > len(data) {
		return nil, 0, c.fail(fmt.Errorf("%s: decode consumed %d of %d bytes", c.p.name(), res.Consumed, len(data)))
	}
	var stmts []inspect.Statement
	if len(res.Statements) > 0 {
		stmts = make([]inspect.Statement, 0, len(res.Statements))
		for i := range res.Statements {
			s, err := adoptStatement(&res.Statements[i], c.p.Protocol(), dir)
			if err != nil {
				return nil, 0, c.fail(fmt.Errorf("%s: decode result: %w", c.p.name(), err))
			}
			stmts = append(stmts, s)
		}
	}
	return stmts, res.Consumed, nil
}

// Filter implements gate.StreamFilter; identity when the manifest names no
// filter capability.
func (c *codec) Filter(dir inspect.Direction, data []byte) ([]byte, error) {
	if !c.p.manifest.hasCapability(CapFilter) {
		return data, nil
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.in.callInOut(c.conn, expFilter, data, uint64(c.conn), dirCode(dir))
}

// DenyFrame implements gate.DenyFramer. Nil when the manifest names no
// deny capability, or when the guest cannot answer: the proxy then falls
// back to its own writer, and for a plug-in protocol that is a bare close,
// which is the outcome the capability exists to improve on.
func (c *codec) DenyFrame(dir inspect.Direction, message string) []byte {
	if !c.p.manifest.hasCapability(CapDeny) || c.err != nil {
		return nil
	}
	out, err := c.in.callInOut(c.conn, expDeny, []byte(message), uint64(c.conn), dirCode(dir))
	if err != nil {
		c.p.logger().Error("codec plug-in: deny frame failed", "protocol", c.p.name(), "conn", c.conn, "error", err)
		return nil
	}
	return out
}

// TakeCredential implements gate.CredentialSource; ok is false when the
// manifest names no credential capability.
//
// A guest failure here has no error to return through. The statement is
// then reduced to an unknown operation carrying only the failure, so no
// trace of a credential the guest did not remove reaches policy, audit or
// the analyzer, and a rule naming `unknown` refuses it. The instance is
// dead at that point, so the connection drops on its next byte.
func (c *codec) TakeCredential(stmt *inspect.Statement) (string, bool) {
	if !c.p.manifest.hasCapability(CapCredential) || c.err != nil {
		return "", false
	}
	fail := func(err error) (string, bool) {
		c.p.logger().Error("codec plug-in: take_credential failed", "protocol", c.p.name(), "conn", c.conn, "error", err)
		*stmt = inspect.Statement{
			Protocol:  stmt.Protocol,
			Direction: stmt.Direction,
			Operation: inspect.OpUnknown,
			Text:      "take_credential failed: " + err.Error(),
		}
		return "", false
	}
	payload, err := encodeStatement(*stmt)
	if err != nil {
		return fail(err)
	}
	out, err := c.in.callInOut(c.conn, expCredential, payload, uint64(c.conn))
	if err != nil {
		return fail(err)
	}
	if len(out) == 0 {
		return "", false
	}
	var res credentialResult
	if err := decodeStrict(out, &res); err != nil {
		return fail(fmt.Errorf("credential result: %w", err))
	}
	if !res.OK {
		return "", false
	}
	if res.Statement == nil {
		return fail(fmt.Errorf("credential result says ok but carries no statement"))
	}
	replaced, err := adoptStatement(res.Statement, stmt.Protocol, stmt.Direction)
	if err != nil {
		return fail(fmt.Errorf("credential result: %w", err))
	}
	*stmt = replaced
	return res.Credential, true
}

// content renders a statement for the AI analyzer through the guest's
// `content` export, behind the Content method of the wrappers above; ok
// is false when the guest says there is nothing to classify, or when the
// guest failed (nothing to classify is the safe reading of a dead
// instance: the connection is about to drop). The daemon adapts it into
// an analyzer.Builder.
func (c *codec) content(stmt inspect.Statement, maxBytes int) (text, cacheKey string, ok bool) {
	if c.err != nil {
		return "", "", false
	}
	payload, err := encodeStatement(stmt)
	if err != nil {
		return "", "", false
	}
	out, err := c.in.callInOut(c.conn, expContent, payload, uint64(c.conn))
	if err != nil || len(out) == 0 {
		return "", "", false
	}
	var res contentResult
	if err := decodeStrict(out, &res); err != nil {
		c.p.logger().Error("codec plug-in: content result refused", "protocol", c.p.name(), "conn", c.conn, "error", err)
		return "", "", false
	}
	if !res.OK {
		return "", "", false
	}
	return analyzer.Truncate(res.Text, maxBytes), res.CacheKey, true
}

// Close ends the connection on the guest side and, under per_connection,
// releases the instance. Idempotent: the gate closes a Duplex codec once,
// but MaskSupportedBy and tests close what they build.
func (c *codec) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if c.err != nil || c.in == nil {
		return nil
	}
	var err error
	if c.p.shape.exports[expClose] {
		_, err = c.in.call(c.conn, expClose, uint64(c.conn))
	}
	if c.owned {
		c.in.close()
	}
	return err
}

// EnableRewrite tells the guest the lane has a masker, before any server
// bytes arrive. A failure is logged; the instance is dead and Decode
// reports it on the next chunk.
func (c *rewriteCodec) EnableRewrite() {
	if c.err != nil {
		return
	}
	if _, err := c.in.call(c.conn, expEnableRewrite, uint64(c.conn)); err != nil {
		c.p.logger().Error("codec plug-in: enable_rewrite failed", "protocol", c.p.name(), "conn", c.conn, "error", err)
	}
}

// Rewrite implements gate.Reframer. The mask callback is installed on the
// instance for the duration of the call, which is the only window in
// which the guest's `mask` import works.
func (c *rewriteCodec) Rewrite(data []byte, mask func(column string, value []byte) []byte) ([]byte, inspect.ReframeResult, error) {
	if c.err != nil {
		return nil, inspect.ReframeResult{}, c.fail(c.err)
	}
	out, err := c.withMask(mask, func() ([]byte, error) {
		_, out, err := c.in.runLocked(c.conn, expRewrite, data, true, true, uint64(c.conn))
		return out, err
	})
	if err != nil {
		return nil, inspect.ReframeResult{}, c.fail(err)
	}
	res, err := c.rewriteResult(expRewrite, out)
	if err != nil {
		// The gate forwards what a failing reframer produced unless the
		// error is ErrStreamUnsafe; ABI.md says a rewrite error fails
		// the stream closed, same as decode.
		return nil, inspect.ReframeResult{}, c.fail(err)
	}
	return res.Bytes, inspect.ReframeResult{Cells: res.Cells, Rows: res.Rows}, nil
}

// Flush implements gate.Reframer. It has no error to return: a failure is
// logged and nothing is released, which is the fail-closed direction for
// bytes that were held because they could not be masked yet.
func (c *rewriteCodec) Flush(mask func(column string, value []byte) []byte) []byte {
	if c.err != nil {
		return nil
	}
	out, err := c.withMask(mask, func() ([]byte, error) {
		_, out, err := c.in.runLocked(c.conn, expFlush, nil, false, true, uint64(c.conn))
		return out, err
	})
	if err == nil {
		var res rewriteResult
		if res, err = c.rewriteResult(expFlush, out); err == nil {
			return res.Bytes
		}
	}
	c.p.logger().Error("codec plug-in: flush failed", "protocol", c.p.name(), "conn", c.conn, "error", err)
	return nil
}

func (c *rewriteCodec) withMask(mask func(string, []byte) []byte, call func() ([]byte, error)) ([]byte, error) {
	c.in.mu.Lock()
	defer c.in.mu.Unlock()
	c.in.mask = mask
	defer func() { c.in.mask = nil }()
	return call()
}

// rewriteResult parses what rewrite or flush returned. No output means
// nothing to forward yet: rows held, nothing counted.
func (c *rewriteCodec) rewriteResult(name string, out []byte) (rewriteResult, error) {
	var res rewriteResult
	if len(out) == 0 {
		return res, nil
	}
	if err := decodeStrict(out, &res); err != nil {
		return res, fmt.Errorf("%s: %s result: %w", c.p.name(), name, err)
	}
	if res.Error != "" {
		return res, fmt.Errorf("%s: %s: %s", c.p.name(), name, res.Error)
	}
	return res, nil
}
