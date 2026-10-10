package wasm

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// randReader feeds WASI's random_get. It is crypto/rand because a guest
// may derive something it treats as a secret from it.
var randReader = crand.Reader

// sqlAnalysis is SQLAnalysis in ABI.md's spelling. libhoop's struct carries
// no JSON tags, so this struct declares the wire form once; the Rust and
// Go SDKs decode this shape.
type sqlAnalysis struct {
	Operation inspect.Operation   `json:"operation"`
	Effects   []inspect.Operation `json:"effects,omitempty"`
	Relations []inspect.Relation  `json:"relations,omitempty"`
	Tables    []string            `json:"tables,omitempty"`
	Complete  bool                `json:"complete"`
	Reason    string              `json:"reason,omitempty"`
}

// errMaskOutsideRewrite is the ABI's "a trap elsewhere" for the mask import.
var errMaskOutsideRewrite = errors.New("hoop.mask called outside rewrite or flush")

// instantiateHost builds the `hoop` module in r. Every import reads the
// calling instance from the context run attached, because one host module
// serves every instance the runtime creates.
func instantiateHost(ctx context.Context, r wazero.Runtime, dialect lexer.Dialect) error {
	b := r.NewHostModuleBuilder(hostModule)

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, _ api.Module, stack []uint64) {
			in := fromContext(ctx)
			sql := in.readString(stack[0], stack[1])
			a := inspect.AnalyzeSQLIn(sql, dialect)
			out, err := json.Marshal(sqlAnalysis{
				Operation: a.Operation, Effects: a.Effects, Relations: a.Relations,
				Tables: a.Tables, Complete: a.Complete, Reason: a.Reason,
			})
			if err != nil {
				in.abort(fmt.Errorf("hoop.analyze_sql: %w", err))
			}
			stack[0] = in.returnBytes(ctx, impAnalyzeSQL, out)
		}), []api.ValueType{i32, i32}, []api.ValueType{i64}).
		Export(impAnalyzeSQL)

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, _ api.Module, stack []uint64) {
			in := fromContext(ctx)
			parts := lexer.Split(in.readString(stack[0], stack[1]), dialect)
			if parts == nil {
				parts = []string{}
			}
			out, err := json.Marshal(parts)
			if err != nil {
				in.abort(fmt.Errorf("hoop.split_sql: %w", err))
			}
			stack[0] = in.returnBytes(ctx, impSplitSQL, out)
		}), []api.ValueType{i32, i32}, []api.ValueType{i64}).
		Export(impSplitSQL)

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, _ api.Module, stack []uint64) {
			in := fromContext(ctx)
			if in.mask == nil {
				in.abort(errMaskOutsideRewrite)
			}
			column := in.readString(stack[0], stack[1])
			value := in.readBytes(stack[2], stack[3])
			stack[0] = in.returnBytes(ctx, impMask, in.mask(column, value))
		}), []api.ValueType{i32, i32, i32, i32}, []api.ValueType{i64}).
		Export(impMask)

	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, _ api.Module, stack []uint64) {
			in := fromContext(ctx)
			in.p.logger().Log(ctx, logLevel(uint32(stack[0])), in.readString(stack[1], stack[2]),
				"protocol", in.p.name(), "conn", in.conn)
		}), []api.ValueType{i32, i32, i32}, nil).
		Export(impLog)

	_, err := b.Instantiate(ctx)
	return err
}

func fromContext(ctx context.Context) *instance {
	in, _ := ctx.Value(ctxKey{}).(*instance)
	if in == nil {
		// Unreachable through run; a host import called with a foreign
		// context is a bug in this package.
		panic("sidecar/codec/wasm: host import called outside an instance call")
	}
	return in
}

// readBytes copies a (ptr, len) region the guest handed to an import. A
// region outside memory aborts the call: the guest passed a pointer it
// does not own, and reading nothing in its place would hand a policy an
// empty statement.
func (in *instance) readBytes(ptr, n uint64) []byte {
	view, ok := in.mem.Read(uint32(ptr), uint32(n))
	if !ok {
		in.abort(fmt.Errorf("guest passed (%d, %d), outside its memory", ptr, n))
	}
	out := make([]byte, len(view))
	copy(out, view)
	return out
}

func (in *instance) readString(ptr, n uint64) string {
	view, ok := in.mem.Read(uint32(ptr), uint32(n))
	if !ok {
		in.abort(fmt.Errorf("guest passed (%d, %d), outside its memory", ptr, n))
	}
	return string(view)
}

// returnBytes hands data back to the guest in the ABI's packed form.
func (in *instance) returnBytes(ctx context.Context, name string, data []byte) uint64 {
	v, err := in.writeOut(ctx, data)
	if err != nil {
		in.abort(fmt.Errorf("hoop.%s: %w", name, err))
	}
	return v
}

// abort ends the export call from inside a host import. wazero recovers
// the panic into the error Call returns, and run reads hostErr to report
// this reason instead of the recovered panic's text.
func (in *instance) abort(err error) {
	in.hostErr = err
	panic(err)
}

func logLevel(level uint32) slog.Level {
	switch level {
	case 0:
		return slog.LevelDebug
	case 1:
		return slog.LevelInfo
	case 2:
		return slog.LevelWarn
	}
	return slog.LevelError
}

// wasiLog forwards a guest's stdout (fd 1) or stderr (fd 2) to the log one
// line at a time. The writer holds a partial line until its newline arrives.
func (p *Plugin) wasiLog(fd int) io.Writer {
	level := slog.LevelInfo
	if fd == 2 {
		level = slog.LevelWarn
	}
	return &lineLogger{log: p.logger(), level: level, attrs: []any{"protocol", p.name(), "fd", fd}}
}

type lineLogger struct {
	log   *slog.Logger
	level slog.Level
	attrs []any

	mu  sync.Mutex
	buf []byte
}

func (l *lineLogger) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		l.log.Log(context.Background(), l.level, string(l.buf[:i]), l.attrs...)
		l.buf = l.buf[i+1:]
	}
}
