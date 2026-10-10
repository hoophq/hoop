// Package wasm loads a codec plug-in: one WebAssembly module that decodes
// a wire protocol the relay was not built with.
//
// # The case for a wasm module
//
// A customer with an in-house protocol, or a vendor whose database the
// shipped codecs do not cover, needs the relay to understand its bytes
// without a release of this repository. A Go plugin would tie them to our
// toolchain and our exact dependency graph; a subprocess would put a
// second binary and an IPC contract in every deployment. A wasm module is
// one file, built from any language with a wasm32 target, that the relay
// runs in-process with no filesystem, no network and no way to reach
// another connection's bytes. The control plane can serve it like it
// serves the config, pinned by sha256.
//
// abi/ABI.md is the contract the module implements; this package is the
// host side of it. The SDKs under sdk/ are conveniences on top.
//
// # The host's checks
//
// Load refuses a module before it runs any of its code for real: it
// checks the imports and exports against the ABI tables, then runs
// `describe` on a throwaway instance and holds the manifest to the module
// (every capability has its exports and vice versa, a module importing the
// SQL lexer names a dialect, a module importing WASI says so). Load then
// builds the runtime that serves connections under the manifest's own
// memory limit, and every export call runs under its call_timeout_ms.
//
// Every failure fails closed. A trap, a timeout or an exit kills the
// instance and every later call on it reports the same error, so the
// connection drops and nothing half-decoded reaches a policy. Under
// per_lane instancing the next connection rebuilds the instance.
//
// # The injection seam
//
// The classifier and the lexer stay in sidecar/, as everywhere else: the
// guest reaches them through the `hoop` imports, with the dialect the
// manifest names. A plug-in cannot classify SQL any other way, which is
// what keeps one auditable copy of the most safety-critical code.
package wasm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Option configures Load.
type Option func(*loadOptions)

type loadOptions struct {
	log *slog.Logger
}

// WithLogger routes the guest's `log` import and the host's own messages
// about the plug-in to l. Without it the plug-in logs through
// slog.Default, read at each call, so the plug-in honours a logger
// installed after Load (the daemon sets its own). Pass a logger already
// tagged with the lane so a guest log line names where it ran.
func WithLogger(l *slog.Logger) Option {
	return func(o *loadOptions) {
		if l != nil {
			o.log = l
		}
	}
}

// Plugin is one loaded module: a compiled program plus the runtime that
// instantiates it per connection (or once per lane). It is safe for
// concurrent use; the instances it creates serialize their own calls.
type Plugin struct {
	manifest Manifest
	raw      []byte
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	shape    moduleShape
	dialect  lexer.Dialect
	timeout  time.Duration
	log      *slog.Logger

	// ctx is the parent of every call's deadline. Close cancels it, which
	// makes WithCloseOnContextDone end a call in flight on any instance.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	lane     *instance // the shared instance under per_lane; nil until the first codec
	nextConn uint32
	closed   bool
}

// errPluginClosed is what a codec built after Close reports.
var errPluginClosed = errors.New("codec plug-in is closed")

// Load compiles module, validates it against the ABI, runs `describe` on a
// throwaway instance and returns the plug-in ready to serve connections.
// It does not check the module's sha256: the daemon pins and verifies the
// bytes it fetched before handing them here.
func Load(ctx context.Context, module []byte, opts ...Option) (*Plugin, error) {
	var o loadOptions
	for _, opt := range opts {
		opt(&o)
	}

	// The manifest decides the memory limit and the deadline, and the
	// manifest comes from running the module: so a bootstrap runtime
	// under the defaults runs describe, and Load builds the serving
	// runtime afterwards under the manifest's own limits. Two compiles
	// per load; loads are rare.
	boot := &Plugin{log: o.log, timeout: callTimeout(DefaultCallTimeoutMS)}
	boot.ctx, boot.cancel = context.WithCancel(context.WithoutCancel(ctx))
	defer boot.Close()
	boot.runtime = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(DefaultMemoryLimitPages))
	compiled, err := boot.runtime.CompileModule(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("codec plug-in: compile: %w", err)
	}
	boot.compiled = compiled
	if boot.shape, err = inspectModule(compiled); err != nil {
		return nil, fmt.Errorf("codec plug-in: module %w", err)
	}
	// describe has no SQL to classify; the dialect is unknown until it
	// returns, so the bootstrap host serves the default one.
	if err := instantiateHost(ctx, boot.runtime, lexer.Postgres); err != nil {
		return nil, fmt.Errorf("codec plug-in: host module: %w", err)
	}
	boot.manifest.WASI = boot.shape.importsWASI
	if boot.manifest.WASI {
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, boot.runtime); err != nil {
			return nil, fmt.Errorf("codec plug-in: wasi: %w", err)
		}
	}
	in, err := boot.instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("codec plug-in: %w", err)
	}
	raw, err := in.callOut(0, expDescribe)
	in.close()
	if err != nil {
		return nil, fmt.Errorf("codec plug-in: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("codec plug-in: describe returned no output")
	}
	manifest, err := parseManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("codec plug-in: %w", err)
	}
	if err := checkCapabilities(manifest, boot.shape); err != nil {
		return nil, fmt.Errorf("codec plug-in %s: %w", manifest.Protocol, err)
	}

	p := &Plugin{
		manifest: manifest,
		raw:      raw,
		shape:    boot.shape,
		timeout:  callTimeout(manifest.callTimeoutMS()),
		log:      o.log,
	}
	if manifest.SQLDialect != "" {
		p.dialect, _ = dialectOf(manifest.SQLDialect)
	}
	p.ctx, p.cancel = context.WithCancel(context.WithoutCancel(ctx))
	p.runtime = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(manifest.memoryLimitPages()))
	if p.compiled, err = p.runtime.CompileModule(ctx, module); err != nil {
		p.Close()
		return nil, fmt.Errorf("codec plug-in %s: compile: %w", manifest.Protocol, err)
	}
	if err := instantiateHost(ctx, p.runtime, p.dialect); err != nil {
		p.Close()
		return nil, fmt.Errorf("codec plug-in %s: host module: %w", manifest.Protocol, err)
	}
	if manifest.WASI {
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, p.runtime); err != nil {
			p.Close()
			return nil, fmt.Errorf("codec plug-in %s: wasi: %w", manifest.Protocol, err)
		}
	}
	return p, nil
}

// Protocol is the manifest's protocol name.
func (p *Plugin) Protocol() inspect.Protocol { return inspect.Protocol(p.manifest.Protocol) }

// Label is the manifest's human name.
func (p *Plugin) Label() string { return p.manifest.Label }

// Version is the plug-in's own version, empty when the manifest gave none.
func (p *Plugin) Version() string { return p.manifest.Version }

// Capabilities lists the optional exports the manifest declares.
func (p *Plugin) Capabilities() []string { return slices.Clone(p.manifest.Capabilities) }

// Manifest returns the describe JSON as the module produced it, after
// validation. The daemon renders the listener form from it.
func (p *Plugin) Manifest() []byte { return slices.Clone(p.raw) }

// ManifestValues returns the parsed manifest.
func (p *Plugin) ManifestValues() Manifest { return p.manifest }

func (p *Plugin) logger() *slog.Logger {
	if p.log != nil {
		return p.log
	}
	return slog.Default()
}

// name prefixes errors and log lines. The bootstrap plug-in has no
// protocol yet.
func (p *Plugin) name() string {
	if p.manifest.Protocol == "" {
		return "codec plug-in"
	}
	return p.manifest.Protocol
}

// ValidateOptions holds a listener's option values to the manifest: every
// key must be an option the manifest declares and every value must parse
// as its type. The daemon calls it at config validation, so it refuses a
// config with a typo before any connection reaches open.
func (p *Plugin) ValidateOptions(options map[string]string) error {
	for name, value := range options {
		i := slices.IndexFunc(p.manifest.Options, func(o ManifestOption) bool { return o.Name == name })
		if i < 0 {
			return fmt.Errorf("%s: unknown option %q", p.manifest.Protocol, name)
		}
		o := p.manifest.Options[i]
		switch o.Type {
		case "int":
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				return fmt.Errorf("%s: option %q: %q is not an integer", p.manifest.Protocol, name, value)
			}
		case "bool":
			if value != "true" && value != "false" {
				return fmt.Errorf("%s: option %q: %q is not true or false", p.manifest.Protocol, name, value)
			}
		case "enum":
			if !slices.Contains(o.Values, value) {
				return fmt.Errorf("%s: option %q: %q is not one of %v", p.manifest.Protocol, name, value, o.Values)
			}
		}
	}
	return nil
}

// openOptions is what `open` receives: the manifest's defaults with the
// listener's values over them, so a guest reads every option it declared
// and never has to carry its own default table.
func (p *Plugin) openOptions(options map[string]string) ([]byte, error) {
	merged := make(map[string]string, len(p.manifest.Options)+len(options))
	for _, o := range p.manifest.Options {
		if o.Default != "" {
			merged[o.Name] = o.Default
		}
	}
	for k, v := range options {
		merged[k] = v
	}
	return json.Marshal(merged)
}

// NewCodec builds the codec for one connection. Under per_connection it
// is a fresh instance; under per_lane it is a new connection id on the
// shared instance, rebuilt first when the previous one died.
//
// It never returns nil: a codec that could not be built reports its
// error from every call, so the gate drops the connection with the reason
// in the log.
//
// The returned value implements gate.Reframer and EnableRewrite only when
// the manifest names the rewrite capability, and analyzer.ContentRenderer
// only when it names content, through distinct types, so a type
// assertion answers for this plug-in alone.
func (p *Plugin) NewCodec(options map[string]string) inspect.Codec {
	c := &codec{p: p}
	c.err = p.attach(c, options)
	if c.err != nil {
		p.logger().Error("codec plug-in: connection refused", "protocol", p.manifest.Protocol, "error", c.err)
	}
	rewrite, content := p.manifest.hasCapability(CapRewrite), p.manifest.hasCapability(CapContent)
	switch {
	case rewrite && content:
		return &rewriteContentCodec{&rewriteCodec{c}}
	case rewrite:
		return &rewriteCodec{c}
	case content:
		return &contentCodec{c}
	}
	return c
}

// attach gives c its instance and connection id and runs `open`.
func (p *Plugin) attach(c *codec, options map[string]string) error {
	if err := p.ValidateOptions(options); err != nil {
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errPluginClosed
	}
	var err error
	switch p.manifest.Instances {
	case InstancesPerLane:
		if p.lane != nil && p.lane.isDead() {
			// ABI.md: the lane refuses new connections until the host
			// rebuilds the instance. This is the rebuild; codecs still
			// holding the dead one keep failing.
			p.lane.close()
			p.lane = nil
		}
		if p.lane == nil {
			p.lane, err = p.instantiate(p.ctx)
		}
		c.in, c.conn = p.lane, p.nextConn
		p.nextConn++
	default:
		c.in, err = p.instantiate(p.ctx)
		c.owned = true
	}
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if !p.shape.exports[expOpen] {
		return nil
	}
	opts, err := p.openOptions(options)
	if err != nil {
		return err
	}
	res, err := c.in.callIn(c.conn, expOpen, opts, uint64(c.conn))
	if err != nil {
		return err
	}
	if code := uint32(res[0]); code != 0 {
		return fmt.Errorf("%s: open refused the connection (code %d)", p.manifest.Protocol, code)
	}
	return nil
}

// Close releases the runtime and every instance. It terminates a call in
// flight on any instance; codecs still open report the plug-in closed.
func (p *Plugin) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	lane := p.lane
	p.lane = nil
	p.mu.Unlock()
	if lane != nil {
		lane.close()
	}
	p.cancel()
	if p.runtime == nil {
		return nil
	}
	return p.runtime.Close(context.WithoutCancel(p.ctx))
}

func callTimeout(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
