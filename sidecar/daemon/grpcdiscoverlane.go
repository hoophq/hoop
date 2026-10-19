package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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

	// attempted is set when the first discovery attempt ends. Until then
	// the lane is not ready, whatever it would answer.
	attempted atomic.Bool
	failure   atomic.Pointer[string]
}

// Serve binds the stand-in, discovers the schema, and serves until ctx ends,
// Close is called, or the listener fails.
//
// The stand-in binds before the first attempt, so a stalled upstream leaves
// the address answering (refusing, or forwarding with fail_open) instead of
// closed for the attempt's timeout. Attempts run beside the stand-in, so a
// bind failure ends Serve at once.
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

	standIn, err := d.build(nil, "")
	if err != nil {
		return fmt.Errorf("%s: %w", d.name, err)
	}
	if !d.install(standIn) {
		return nil
	}
	served := make(chan error, 1)
	go func() {
		err := standIn.Serve(ctx)
		// Closed before Serve began (a fast first attempt, or Close): libhoop
		// answers ErrServerClosed, which is a clean stop like any other.
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		served <- err
	}()

	type attempt struct {
		schema *codecgrpc.Schema
		sum    string
		err    error
	}
	results := make(chan attempt, 1)
	try := func() {
		go func() {
			schema, sum, err := d.discover(ctx)
			results <- attempt{schema, sum, err}
		}()
	}
	try()

	wait := d.retry
	if wait <= 0 {
		wait = grpcDiscoverRetryMin
	}
	var retry <-chan time.Time
	for {
		select {
		case err := <-served:
			// The stand-in stopped: a bind failure, Close, or ctx.
			return err
		case <-retry:
			retry = nil
			try()
		case a := <-results:
			if a.err != nil {
				if ctx.Err() != nil {
					return <-served
				}
				d.recordFailure(a.err)
				d.attempted.Store(true)
				retry = time.After(wait)
				wait = min(wait*2, grpcDiscoverRetryMax)
				continue
			}
			// Empty when the stand-in had not bound yet: the schema-backed
			// server then binds the configured address.
			var listen string
			if addr := standIn.Addr(); addr != nil {
				listen = addr.String()
			}
			if err := d.retire(standIn, served); err != nil {
				return err
			}
			return d.serveSchema(ctx, a.schema, a.sum, listen)
		}
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
	d.attempted.Store(true)
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

// retire closes the stand-in, waits for its Serve to return, then banks its
// counters, so Stats stays monotonic across the swap and counts every RPC
// the stand-in accepted. Stats reads the stand-in until then.
func (d *discoveringGRPCServer) retire(srv *codecgrpc.Server, served <-chan error) error {
	_ = srv.Close()
	err := <-served
	d.mu.Lock()
	_, total, denied := srv.Stats()
	d.retiredTotal += total
	d.retiredDenied += denied
	d.current = nil
	d.mu.Unlock()
	return err
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

// Ready is true once the lane is bound and its first discovery attempt has
// ended, in both modes. It stays true while discovery keeps failing: /readyz
// answers for the whole process, and one upstream's lasting failure (a pin
// mismatch, reflection turned off) must not take every other listener out
// of rotation. That failure shows in the log and as rpc_refused records.
func (d *discoveringGRPCServer) Ready() bool {
	if !d.attempted.Load() {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.current != nil && d.current.Addr() != nil
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
