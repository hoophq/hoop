// Command hoop-inspect is an inspecting TCP relay.
//
// It sits between a client and a database or API, decodes the wire protocol,
// evaluates each statement against policy, records an audit trail naming the
// human who ran it, and masks sensitive values on the way back.
//
// # Where it belongs in a deployment
//
// Behind something that already owns the network path and identity — an Envoy
// sidecar, typically. Envoy terminates TLS, authenticates the user and
// forwards to hoop-inspect over loopback or a unix socket; hoop-inspect owns
// the payload. It is deliberately not a router or a load balancer: one
// listener, one upstream, one protocol per endpoint.
//
// # Why a separate binary rather than a library-only distribution
//
// The library is the product, and it is embeddable (libhoop's ReverseProxy
// calls the same gate). This binary exists so a deployment that just wants
// the capability gets a container image instead of an integration project.
//
// Usage:
//
//	hoop-inspect -config /etc/hoop-inspect/config.json
//	hoop-inspect -validate -config config.json   # check and exit
//	hoop-inspect -version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	_ "github.com/hoophq/hoopinspect/codec/all"
	"github.com/hoophq/hoopinspect/gate"
	"github.com/hoophq/hoopinspect/mask"
	"github.com/hoophq/hoopinspect/policy"
	"github.com/hoophq/hoopinspect/proxy"
	"github.com/hoophq/hoopinspect/session"
	"github.com/hoophq/hoopinspect/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		configPath = flag.String("config", "", "path to the JSON config file")
		validate   = flag.Bool("validate", false, "validate the config and exit")
		showVer    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("hoop-inspect", version)
		return
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "hoop-inspect: -config is required")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hoop-inspect:", err)
		os.Exit(1)
	}
	if *validate {
		fmt.Println("config OK:", len(cfg.Listeners), "listener(s)")
		return
	}

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "hoop-inspect:", err)
		os.Exit(1)
	}
}

func run(cfg *Config) error {
	log := newLogger(cfg.LogLevel)

	ac, err := buildAudit(cfg.Audit)
	if err != nil {
		return err
	}
	auditSink := ac.sink
	defer func() {
		// Close flushes buffered events. Losing the tail of an audit trail on
		// shutdown is the failure mode this defer exists to prevent.
		if cerr := auditSink.Close(); cerr != nil {
			log.Error("audit sink close failed", "error", cerr)
		}
	}()

	pol, err := cfg.BuildPolicy()
	if err != nil {
		return err
	}
	if pol == nil {
		log.Warn("running in observe-only mode; no statement will be denied",
			"hint", "set policy.enforce=true to enforce")
	}

	var masker gate.Masker
	if cfg.Mask.Enabled {
		m, merr := mask.New(cfg.Mask.Rules)
		if merr != nil {
			return merr
		}
		masker = maskAdapter{m}
		log.Info("response masking enabled", "rules", len(cfg.Mask.Rules))
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	servers := make([]*proxy.Server, 0, len(cfg.Listeners))
	for _, lc := range cfg.Listeners {
		srv, serr := buildServer(lc, cfg, pol, auditSink, masker, log)
		if serr != nil {
			return serr
		}
		servers = append(servers, srv)
	}

	if cfg.Admin.Listen != "" {
		go serveAdmin(ctx, cfg.Admin.Listen, servers, ac, log)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(servers))
	for i, srv := range servers {
		wg.Add(1)
		go func(s *proxy.Server, name string) {
			defer wg.Done()
			if serr := s.Serve(ctx); serr != nil {
				log.Error("listener failed", "listener", name, "error", serr)
				errCh <- serr
			}
		}(srv, cfg.Listeners[i].displayName(i))
	}

	<-ctx.Done()
	log.Info("shutting down")
	for _, srv := range servers {
		_ = srv.Close()
	}
	wg.Wait()
	close(errCh)

	// Report the first listener failure; a bind error on one endpoint must
	// not be swallowed just because the process was also asked to stop.
	for e := range errCh {
		if e != nil {
			return e
		}
	}
	return nil
}

func (l ListenerConfig) displayName(i int) string {
	switch {
	case l.Name != "":
		return l.Name
	case l.Connection != "":
		return l.Connection
	}
	return fmt.Sprintf("listener[%d]", i)
}

// buildServer turns one listener config into a running-capable Server.
func buildServer(
	lc ListenerConfig,
	cfg *Config,
	pol policy.Evaluator,
	sink audit.Sink,
	masker gate.Masker,
	log *slog.Logger,
) (*proxy.Server, error) {
	upstreamTLS, err := lc.UpstreamTLS.BuildTLS()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", lc.Connection, err)
	}
	if upstreamTLS != nil && upstreamTLS.InsecureSkipVerify {
		// Warn loudly. A proxy whose whole purpose is inspecting sensitive
		// traffic should not quietly accept any upstream certificate.
		log.Warn("upstream certificate verification is DISABLED",
			"listener", lc.Connection, "upstream", lc.Upstream)
	}

	var identityFn func(net.Conn) session.Identity
	if lc.IdentityHeader != "" {
		identityFn = headerIdentity(lc.IdentityHeader)
	}

	return proxy.NewServer(proxy.Config{
		Listen:           lc.Listen,
		Network:          lc.Network,
		Upstream:         lc.Upstream,
		UpstreamTLS:      upstreamTLS,
		Protocol:         hoopinspect.Protocol(lc.Protocol),
		Connection:       lc.Connection,
		Policy:           pol,
		Audit:            sink,
		Masker:           masker,
		FailOnAuditError: cfg.Audit.FailClosed,
		DenyWriter:       proxy.ProtocolDenyWriter{},
		IdentityFn:       identityFn,
		IdleTimeout:      time.Duration(lc.IdleTimeoutSec) * time.Second,
		MaxConns:         lc.MaxConns,
		Logger:           log.With("listener", lc.displayName(0)),
	})
}

// maskAdapter bridges *mask.Masker to the narrow gate.Masker interface. The
// gate declares its own interface so a shop with an existing DLP service can
// substitute one without forking the gate.
type maskAdapter struct{ m *mask.Masker }

func (a maskAdapter) Mask(data []byte) ([]byte, []string, int) {
	out, res := a.m.Mask(data)
	return out, res.Entities, res.Count
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	// JSON to stderr: audit events go to their own sink, so stderr is purely
	// operational and a container platform can treat the two differently.
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lv})
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// auditChain is what buildAudit assembles: the sink the gate writes to, plus
// the read-side handles the admin endpoint needs.
type auditChain struct {
	sink  audit.Sink
	mem   *audit.MemorySink
	query *store.MemoryStore
}

// buildAudit assembles the sink chain.
//
// Order matters: the durable JSONL sink is first so it records even if a
// later sink errors, and MultiSink attempts every sink regardless.
func buildAudit(cfg AuditConfig) (auditChain, error) {
	opts := audit.SinkOptions{
		RedactStatements:  cfg.RedactStatements,
		MaxStatementBytes: cfg.MaxStatementBytes,
	}

	var out auditChain
	var sinks []audit.Sink

	switch cfg.File {
	case "", "-":
		// No file configured: still record to stdout rather than silently
		// discarding. A deployment that genuinely wants no audit trail has
		// to say so by pointing the file at /dev/null.
		sinks = append(sinks, audit.NewJSONLSink(os.Stdout, opts))
	default:
		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if err != nil {
			return out, fmt.Errorf("open audit file: %w", err)
		}
		sinks = append(sinks, audit.NewJSONLSink(f, opts))
	}

	if cfg.MemoryBuffer > 0 {
		out.mem = audit.NewMemorySink(cfg.MemoryBuffer)
		sinks = append(sinks, out.mem)
	}

	// The query store is both a sink and the read side, so it indexes on the
	// same write that records the event. Two pipelines would drift.
	if cfg.QuerySessions > 0 {
		out.query = store.NewMemoryStore(cfg.QuerySessions)
		sinks = append(sinks, out.query)
	}

	out.sink = audit.NewMultiSink(sinks...)
	if cfg.AsyncQueueSize > 0 {
		out.sink = audit.NewAsyncSink(out.sink, cfg.AsyncQueueSize)
	}
	return out, nil
}

// serveAdmin exposes health and stats.
//
// Bound separately from the data path so a platform can scrape it without
// being able to reach the proxy, and vice versa.
func serveAdmin(
	ctx context.Context,
	addr string,
	servers []*proxy.Server,
	ac auditChain,
	log *slog.Logger,
) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		type stat struct {
			Addr   string `json:"addr"`
			Active int64  `json:"active"`
			Total  int64  `json:"total"`
			Denied int64  `json:"denied"`
		}
		out := make([]stat, 0, len(servers))
		for _, s := range servers {
			active, total, denied := s.Stats()
			a := ""
			if s.Addr() != nil {
				a = s.Addr().String()
			}
			out = append(out, stat{Addr: a, Active: active, Total: total, Denied: denied})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":   version,
			"listeners": out,
		})
	})

	if ac.mem != nil {
		// The recent-events endpoint is a debugging aid, not an audit
		// interface: the ring drops old events, so anything reading this for
		// compliance is reading an incomplete record.
		mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"events":  ac.mem.Events(),
				"dropped": ac.mem.Dropped(),
				"warning": "in-memory ring buffer; not a complete audit trail",
			})
		})
	}

	// The query API: the endpoints a UI renders. Mounted under /api so the
	// operational endpoints above (/healthz, /stats) stay stable and
	// separately scrapeable.
	//
	// Deliberately on the ADMIN listener, not the data path. This is a read
	// interface to every statement every user ran; it belongs behind
	// whatever already decides who may see audit data, and the data-path
	// ports must never serve it.
	if ac.query != nil {
		api := store.NewAPI(ac.query)
		api.BasePath = "/api"
		mux.Handle("/api/", api)
		log.Info("query API mounted",
			"paths", "/api/sessions, /api/sessions/{id}, /api/sessions/{id}/events, /api/events, /api/stats")
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info("admin endpoint listening", "listen", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("admin endpoint failed", "error", err)
	}
}

// headerIdentity extracts the authenticated subject from an HTTP header on
// the first bytes of a connection.
//
// This peeks at the connection without consuming it, which a plain net.Conn
// cannot do — so it is only wired up for listeners that opt in via
// IdentityHeader, and it is documented as safe only when nothing but the
// authenticating proxy can reach the listener.
func headerIdentity(header string) func(net.Conn) session.Identity {
	return func(c net.Conn) session.Identity {
		// The proxy reads the stream itself; extracting the header here would
		// require buffering ahead of the relay. Rather than duplicate that
		// machinery, identity for the http protocol is resolved from the
		// inspected request inside the gate, and this function contributes
		// only the peer address.
		//
		// Kept as a named function so the config surface is honest about
		// where identity comes from today.
		return session.Identity{PeerAddr: c.RemoteAddr().String()}
	}
}
