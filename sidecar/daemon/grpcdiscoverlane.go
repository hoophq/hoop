package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	codecgrpc "github.com/hoophq/libhoop/v2/codec/grpc"
)

// The wait between discovery attempts while a lane has no schema: doubled
// after each failure, up to the cap.
const (
	grpcDiscoverRetryMin = time.Second
	grpcDiscoverRetryMax = 30 * time.Second
)

// discoveringGRPCServer is a grpc lane whose schema comes from its upstream
// over gRPC server reflection (grpc.discover).
//
// libhoop binds a schema into its server when it builds one, and its Close
// drops every connection. So until discovery succeeds a stand-in server with
// no schema holds the address: it refuses every RPC, or with fail_open
// forwards them with method-level inspection. When a schema loads, the
// stand-in closes and the schema-backed server binds the address it held.
// In-flight RPCs on the stand-in end with the swap; clients retry.
type discoveringGRPCServer struct {
	name     string
	upstream string
	cfg      GRPCDiscoverConfig
	log      *slog.Logger

	// fetch returns the upstream's serialized FileDescriptorSet.
	fetch func(ctx context.Context) ([]byte, error)
	// build makes a libhoop server. A nil schema makes the stand-in; a
	// non-empty listen overrides the configured address, so the
	// schema-backed server binds where the stand-in did, port 0 included.
	build func(schema *codecgrpc.Schema, listen string) (*codecgrpc.Server, error)
	// retry is the first wait between attempts. Zero is grpcDiscoverRetryMin.
	retry time.Duration

	mu            sync.Mutex
	current       *codecgrpc.Server
	cancel        context.CancelFunc
	closed        bool
	retiredTotal  int64
	retiredDenied int64
	methods       []string

	ready   atomic.Bool
	failure atomic.Pointer[string]
}

// Serve discovers the schema and serves until ctx ends, Close is called, or
// the listener fails.
func (d *discoveringGRPCServer) Serve(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.cancel = cancel
	d.mu.Unlock()

	schema, sum, err := d.discover(ctx)
	if err == nil {
		return d.serveSchema(ctx, schema, sum, "")
	}
	if ctx.Err() != nil {
		return nil
	}
	d.recordFailure(err)

	standIn, err := d.build(nil, "")
	if err != nil {
		return fmt.Errorf("%s: %w", d.name, err)
	}
	if !d.install(standIn) {
		return nil
	}
	served := make(chan error, 1)
	go func() { served <- standIn.Serve(ctx) }()

	wait := d.retry
	if wait <= 0 {
		wait = grpcDiscoverRetryMin
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case err := <-served:
			// The stand-in stopped: a bind failure, Close, or ctx.
			return err
		case <-timer.C:
		}
		schema, sum, err := d.discover(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return <-served
			}
			d.recordFailure(err)
			wait = min(wait*2, grpcDiscoverRetryMax)
			timer.Reset(wait)
			continue
		}
		var listen string
		if addr := standIn.Addr(); addr != nil {
			listen = addr.String()
		}
		d.retire(standIn)
		if err := <-served; err != nil {
			return err
		}
		return d.serveSchema(ctx, schema, sum, listen)
	}
}

// discover runs one attempt: fetch, check the pin, load.
func (d *discoveringGRPCServer) discover(ctx context.Context) (*codecgrpc.Schema, string, error) {
	actx, cancel := context.WithTimeout(ctx, grpcDiscoverTimeout)
	defer cancel()
	raw, err := d.fetch(actx)
	if err != nil {
		return nil, "", err
	}
	sum := grpcSchemaDigest(raw)
	if d.cfg.SHA256 != "" && !strings.EqualFold(sum, d.cfg.SHA256) {
		return nil, sum, fmt.Errorf("the upstream's schema has sha256 %s, not the pinned "+
			"grpc.discover.sha256 %s; review it with -grpc-discover, then update the pin",
			sum, strings.ToLower(d.cfg.SHA256))
	}
	schema, err := codecgrpc.LoadSchemaBytes("discovered from "+d.upstream, raw)
	if err != nil {
		return nil, sum, err
	}
	return schema, sum, nil
}

// grpcSchemaDigest is the pin a discovered set is checked against. Stable
// across runs against an unchanged server: Discover sorts the files by name,
// and a FileDescriptorProto has no map field to marshal in random order.
func grpcSchemaDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (d *discoveringGRPCServer) serveSchema(ctx context.Context, schema *codecgrpc.Schema, sum, listen string) error {
	srv, err := d.build(schema, listen)
	if err != nil {
		return fmt.Errorf("%s: %w", d.name, err)
	}
	methods := schema.MethodPaths()
	d.mu.Lock()
	d.methods = methods
	d.mu.Unlock()
	if !d.install(srv) {
		return nil
	}
	d.failure.Store(nil)
	d.ready.Store(true)
	d.log.Info("grpc schema discovered",
		"upstream", d.upstream, "sha256", sum, "methods", len(methods), "pinned", d.cfg.SHA256 != "")
	return srv.Serve(ctx)
}

// recordFailure keeps the latest failure for the stand-in's refusals and
// logs it when it changes, so a retry loop does not repeat one line.
func (d *discoveringGRPCServer) recordFailure(err error) {
	msg := err.Error()
	if prev := d.failure.Swap(&msg); prev != nil && *prev == msg {
		return
	}
	action := "refusing every RPC"
	if d.cfg.FailOpen {
		action = "forwarding RPCs with method-level inspection only (fail_open)"
	}
	d.log.Warn("grpc schema discovery failed; "+action+" and retrying",
		"upstream", d.upstream, "error", msg)
}

// lastFailure is the reason the stand-in gives for an RPC.
func (d *discoveringGRPCServer) lastFailure() string {
	if p := d.failure.Load(); p != nil {
		return *p
	}
	return "no schema loaded yet"
}

// install makes srv the server Close, Addr and Stats read. False means the
// lane was closed meanwhile, and srv is closed with it.
func (d *discoveringGRPCServer) install(srv *codecgrpc.Server) bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		_ = srv.Close()
		return false
	}
	d.current = srv
	d.mu.Unlock()
	return true
}

// retire closes the stand-in and banks its counters, so Stats stays
// monotonic across the swap.
func (d *discoveringGRPCServer) retire(srv *codecgrpc.Server) {
	d.mu.Lock()
	_, total, denied := srv.Stats()
	d.retiredTotal += total
	d.retiredDenied += denied
	d.current = nil
	d.mu.Unlock()
	_ = srv.Close()
}

// Close stops discovery and the current server. Safe before Serve and more
// than once.
func (d *discoveringGRPCServer) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	cur, cancel := d.current, d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cur != nil {
		return cur.Close()
	}
	return nil
}

func (d *discoveringGRPCServer) Addr() net.Addr {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current == nil {
		return nil
	}
	return d.current.Addr()
}

func (d *discoveringGRPCServer) Stats() (active, total, denied int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current != nil {
		active, total, denied = d.current.Stats()
	}
	return active, total + d.retiredTotal, denied + d.retiredDenied
}

// Ready is false while a fail-closed lane has no schema, so /readyz keeps
// traffic away from a lane that refuses it. A fail-open lane serves, so it
// is ready.
func (d *discoveringGRPCServer) Ready() bool {
	return d.cfg.FailOpen || d.ready.Load()
}

// Notes reports the discovery settings, then the discovered methods once a
// schema loaded. -validate reads them before any discovery runs.
func (d *discoveringGRPCServer) Notes() []string {
	notes := []string{fmt.Sprintf(
		"schema: discovered from %s over gRPC server reflection when the lane starts", d.upstream)}
	if d.cfg.SHA256 != "" {
		notes = append(notes, "schema pinned to sha256 "+strings.ToLower(d.cfg.SHA256))
	} else {
		notes = append(notes, "schema not pinned: the lane accepts any schema the upstream "+
			"reports; set grpc.discover.sha256 to refuse a changed one")
	}
	if d.cfg.FailOpen {
		notes = append(notes, "while discovery fails: forwards RPCs with method-level inspection only")
	} else {
		notes = append(notes, "while discovery fails: refuses every RPC with UNAVAILABLE")
	}
	d.mu.Lock()
	notes = append(notes, d.methods...)
	d.mu.Unlock()
	return notes
}
