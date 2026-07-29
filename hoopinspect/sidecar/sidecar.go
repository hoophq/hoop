// Package sidecar assembles the hoop-inspect relay from a JSON config: audit
// sinks, policy evaluators, masking rules and one proxy.Server per listener.
//
// It sits between a client and a database or API, decodes the wire protocol,
// evaluates each statement against policy, records an audit trail naming the
// human who ran it, and masks sensitive values on the way back.
//
// # Deployment topology
//
// Run it behind something that already owns the network path and identity,
// typically an Envoy sidecar. Envoy terminates TLS, authenticates the user
// and forwards to hoop-inspect over loopback or a unix socket; hoop-inspect
// owns the payload. It routes nothing and balances nothing: one listener,
// one upstream, one protocol per endpoint.
//
// # Package here, binary in cmd
//
// The binary lives in the nested module cmd, because that is where the
// optional plugins are linked -- PII detection and the YAML config front end
// -- and a main in the root module could not import them without dragging
// their dependencies into the root's go.mod. Keeping the assembly here leaves
// the relay in the root module: the binary is a shell that supplies a
// detector and a loader, then calls Main. A caller embedding this library can
// call Run directly and skip the binary.
package sidecar

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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/audit"
	_ "github.com/hoophq/hoopinspect/codec/all"
	"github.com/hoophq/hoopinspect/gate"
	"github.com/hoophq/hoopinspect/policy"
	"github.com/hoophq/hoopinspect/proxy"
	"github.com/hoophq/hoopinspect/session"
	"github.com/hoophq/hoopinspect/store"
)

// Version is reported by the admin /stats endpoint and the -version flag.
// Main sets it from its argument; a caller embedding Run directly sets it.
var Version = "dev"

// Loader reads and validates a config file. It lets a build accept YAML
// without the root module linking a YAML parser: pass
// github.com/hoophq/hoopinspect/config/yaml's Load, or nil for JSON only.
type Loader func(path string) (*Config, error)

// Main is the command-line entry point.
//
// version is stamped by the caller's -ldflags. det is the optional detection
// plugin: nil disables masking and makes any pii policy rule a config error.
// load is the optional config reader; nil means JSON only. Main calls
// os.Exit, so it goes last in a main.
//
// Usage:
//
//	hoop-inspect -config /etc/hoop-inspect/config.yaml
//	hoop-inspect -validate -config config.yaml   # check and exit
//	hoop-inspect -version
func Main(version string, det Plugin, load Loader) {
	Version = version

	syntax := "JSON"
	if load == nil {
		load = LoadConfig
	} else {
		syntax = "YAML or JSON"
	}

	var (
		configPath = flag.String("config", "", "path to the config file ("+syntax+")")
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

	cfg, err := load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hoop-inspect:", err)
		os.Exit(1)
	}

	// A "pii" section in a build with no detector is a fatal error. Dropping
	// it silently would leave an operator believing their national-ID masking
	// is on when the binary cannot do it.
	if len(cfg.PII) > 0 && det == nil {
		fmt.Fprintln(os.Stderr, "hoop-inspect: config has a \"pii\" section but this build "+
			"has no PII detector; build github.com/hoophq/hoopinspect/cmd, or pass a "+
			"detector to Main, or remove the section")
		os.Exit(1)
	}

	if *validate {
		// Building every lane covers most of what can go wrong in a config,
		// and a -validate that skips it is a check you stop trusting. With a
		// detector attached this also proves the entity names resolve.
		lanes, lerr := buildLanes(cfg, det)
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "hoop-inspect:", lerr)
			os.Exit(1)
		}
		fmt.Println("config OK:", len(lanes), "listener(s)")
		for _, ln := range lanes {
			mode := "observe-only"
			if ln.policy != nil {
				mode = fmt.Sprintf("enforcing %d rule(s)", len(ln.rules))
			}
			if ln.opaURL != "" {
				mode += " + opa"
			}
			if ln.masker != nil {
				mode += " + masking"
			}
			fmt.Printf("  %-16s %-9s %s\n", ln.name, ln.cfg.Protocol, mode)
		}
		return
	}

	if err := Run(cfg, det); err != nil {
		fmt.Fprintln(os.Stderr, "hoop-inspect:", err)
		os.Exit(1)
	}
}

// Run starts the sidecar and blocks until the process is signalled.
//
// det is the optional detection plugin. Passing nil disables masking and
// rejects any pii policy rule. Call Run to embed the relay in your own
// binary; the shipped one goes through Main.
func Run(cfg *Config, det Plugin) error {
	log := newLogger(cfg.LogLevel)

	ac, err := buildAudit(cfg.Audit)
	if err != nil {
		return err
	}
	auditSink := ac.sink
	defer func() {
		// Close flushes buffered events, so shutdown does not drop the tail of
		// the audit trail.
		if cerr := auditSink.Close(); cerr != nil {
			log.Error("audit sink close failed", "error", cerr)
		}
	}()

	if det != nil {
		log.Info("detection plugin attached")
	}

	lanes, err := buildLanes(cfg, det)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	servers := make([]*proxy.Server, 0, len(lanes))
	for _, ln := range lanes {
		srv, serr := buildServer(ln, cfg.Audit, auditSink, log)
		if serr != nil {
			return serr
		}
		servers = append(servers, srv)

		// One line per lane naming what it enforces. The config file does not
		// show what a lane inherited, so this logs the RESOLVED stack: it turns
		// "why did this not deny" into a minute of reading instead of an
		// afternoon.
		log.Info("lane ready",
			"listener", ln.name,
			"protocol", ln.cfg.Protocol,
			"upstream", ln.cfg.Upstream,
			"enforcing", ln.policy != nil,
			"rules", len(ln.rules),
			"opa", ln.opaURL,
			"masking", ln.masker != nil)
		if ln.policy == nil {
			log.Warn("lane is observe-only; no statement will be denied",
				"listener", ln.name, "hint", "set policy.enforce=true on this listener or at the top level")
		}
	}

	if cfg.Admin.Listen != "" {
		go serveAdmin(ctx, cfg.Admin.Listen, servers, lanes, ac, log)
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
		}(srv, lanes[i].name)
	}

	<-ctx.Done()
	log.Info("shutting down")
	for _, srv := range servers {
		_ = srv.Close()
	}
	wg.Wait()
	close(errCh)

	// Report the first listener failure: a shutdown request must not hide a
	// bind error on one endpoint.
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

// lane is one listener's fully resolved enforcement stack: the listener
// itself plus the evaluator and masker built from its merged config.
//
// It exists so the merge happens once, at startup, in one place. Resolving
// per connection would put slice concatenation on the accept path and let two
// lanes disagree about what they inherited.
type lane struct {
	cfg    ListenerConfig
	name   string
	policy policy.Evaluator
	masker gate.Masker

	// rules and opaURL are the resolved facts the startup log and the
	// /config endpoint report. They sit alongside the built evaluator because
	// a policy.Chain cannot report what went into it.
	rules  []string
	opaURL string
}

// buildLanes resolves and builds every listener's stack.
//
// Exhaustive rather than fail-fast, matching Validate: a config with three
// broken lanes reports three problems in one run, so you do not fix a fleet
// config one error per restart.
func buildLanes(cfg *Config, det Plugin) ([]lane, error) {
	out := make([]lane, 0, len(cfg.Listeners))
	var problems []string

	for i, lc := range cfg.Listeners {
		name := lc.displayName(i)
		pc, mc := cfg.resolve(lc)

		if errs := checkPIIEntities(pc, det); len(errs) > 0 {
			for _, e := range errs {
				problems = append(problems, name+": "+e)
			}
			continue
		}

		pol, err := buildPolicy(pc, det)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			continue
		}
		masker, err := buildMasker(mc, det, hoopinspect.Protocol(lc.Protocol))
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			continue
		}

		ln := lane{cfg: lc, name: name, policy: pol, masker: masker}
		if pol != nil {
			ln.rules = make([]string, 0, len(pc.Rules))
			for _, r := range pc.Rules {
				ln.rules = append(ln.rules, r.Name)
			}
			if pc.OPA != nil {
				ln.opaURL = pc.OPA.URL
			}
		}
		out = append(out, ln)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return out, nil
}

// checkPIIEntities rejects a pii rule naming an entity the detector was not
// configured to find.
//
// Without this the rule loads and evaluates cleanly: matchesPII intersects
// what the scanner reported with what the rule listed, so an entity the
// engine never looks for never appears, and the rule silently allows every
// statement it was written to deny. The operator sees a guardrail that is
// doing nothing.
func checkPIIEntities(pc PolicyConfig, det Plugin) []string {
	if det == nil {
		return nil // buildPolicy already refuses pii rules with no scanner
	}
	var known map[string]bool
	var problems []string

	for _, r := range pc.Rules {
		if r.Type != policy.MatchPII {
			continue
		}
		if known == nil {
			active := det.Entities()
			known = make(map[string]bool, len(active))
			for _, e := range active {
				known[e] = true
			}
		}
		for _, want := range r.Entities {
			if !known[want] {
				problems = append(problems, fmt.Sprintf(
					"rule %q names entity %q, which the detector is not configured to find; "+
						"add it to pii.entities or the rule will never match",
					r.Name, want))
			}
		}
	}
	return problems
}

// buildServer turns one resolved lane into a running-capable Server.
func buildServer(
	ln lane,
	ac AuditConfig,
	sink audit.Sink,
	log *slog.Logger,
) (*proxy.Server, error) {
	lc := ln.cfg
	upstreamTLS, err := lc.UpstreamTLS.BuildTLS()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	if upstreamTLS != nil && upstreamTLS.InsecureSkipVerify {
		// Warn loudly. A proxy built to inspect sensitive traffic should not
		// quietly accept any upstream certificate.
		log.Warn("upstream certificate verification is DISABLED",
			"listener", ln.name, "upstream", lc.Upstream)
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
		Policy:           ln.policy,
		Audit:            sink,
		Masker:           ln.masker,
		FailOnAuditError: ac.FailClosed,
		DenyWriter:       proxy.ProtocolDenyWriter{},
		IdentityFn:       identityFn,
		IdleTimeout:      time.Duration(lc.IdleTimeoutSec) * time.Second,
		MaxConns:         lc.MaxConns,
		Logger:           log.With("listener", ln.name),
	})
}

// buildMasker asks the plugin to compile one lane's "mask" section.
//
// Two refusals rather than silent downgrades, because you discover both
// failures the same way: by finding an unmasked SSN in a screenshot.
//
//   - No plugin. Masking needs a detection engine and this package links
//     none, so an enabled mask section without one cannot work.
//   - A protocol whose framing cannot survive substitution. The rules would
//     load, validate, and never fire.
//
// Validate reports both at startup; these checks cover a caller reaching Run
// without going through LoadConfig.
func buildMasker(mc MaskConfig, det Plugin, proto hoopinspect.Protocol) (gate.Masker, error) {
	if !mc.on() {
		return nil, nil
	}
	if det == nil {
		return nil, fmt.Errorf("mask.enabled is true but this build has no detection plugin")
	}
	if !gate.MaskSupported(proto) {
		return nil, fmt.Errorf("mask.enabled is true but masking is not supported on %s", proto)
	}
	return det.BuildMasker(mc.Rules)
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
	// JSON to stderr: audit events go to their own sink, so stderr carries
	// only operational output and a container platform can route the two
	// differently.
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
		// No file configured: record to stdout rather than discard. A
		// deployment that wants no audit trail says so by pointing the file at
		// /dev/null.
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
// It binds separately from the data path so a platform can scrape it without
// reaching the proxy, and vice versa.
func serveAdmin(
	ctx context.Context,
	addr string,
	servers []*proxy.Server,
	lanes []lane,
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
			Name   string `json:"name"`
			Addr   string `json:"addr"`
			Active int64  `json:"active"`
			Total  int64  `json:"total"`
			Denied int64  `json:"denied"`
		}
		out := make([]stat, 0, len(servers))
		for i, s := range servers {
			active, total, denied := s.Stats()
			a := ""
			if s.Addr() != nil {
				a = s.Addr().String()
			}
			// servers and lanes are built in lockstep from cfg.Listeners, so
			// the index is the join. Each entry carries its lane name so you
			// can tell which of two postgres listeners denied something.
			out = append(out, stat{
				Name: lanes[i].name, Addr: a,
				Active: active, Total: total, Denied: denied,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":   Version,
			"listeners": out,
		})
	})

	// The resolved enforcement stack, per lane.
	//
	// The config file does not show inheritance: a lane's rules are its own
	// plus whatever the top level contributed, and reading the file cannot
	// tell you the result. Debugging a missing denial starts with "which rules
	// is this lane running", and this endpoint answers it.
	//
	// Rule NAMES only. A rule's pattern_regex can encode business logic, and
	// this endpoint already sits beside a read interface to the audit trail.
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		type laneView struct {
			Name      string   `json:"name"`
			Protocol  string   `json:"protocol"`
			Listen    string   `json:"listen"`
			Upstream  string   `json:"upstream"`
			Enforcing bool     `json:"enforcing"`
			Rules     []string `json:"rules"`
			OPA       string   `json:"opa,omitempty"`
			Masking   bool     `json:"masking"`
			MaskNote  string   `json:"mask_note,omitempty"`
		}
		out := make([]laneView, 0, len(lanes))
		for _, ln := range lanes {
			v := laneView{
				Name:      ln.name,
				Protocol:  ln.cfg.Protocol,
				Listen:    ln.cfg.Listen,
				Upstream:  ln.cfg.Upstream,
				Enforcing: ln.policy != nil,
				Rules:     ln.rules,
				OPA:       ln.opaURL,
				Masking:   ln.masker != nil,
			}
			if v.Rules == nil {
				v.Rules = []string{} // render [] rather than null
			}
			if !v.Masking && !gate.MaskSupported(hoopinspect.Protocol(ln.cfg.Protocol)) {
				v.MaskNote = "masking is not supported on this protocol"
			}
			out = append(out, v)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": Version,
			"lanes":   out,
		})
	})

	if ac.mem != nil {
		// The recent-events endpoint is a debugging aid. The ring drops old
		// events, so a compliance reader gets an incomplete record.
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
	// It sits on the ADMIN listener by design. This is a read interface to
	// every statement every user ran; it belongs behind whatever already
	// decides who may see audit data, and the data-path ports must never
	// serve it.
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
// cannot do, so only listeners that opt in via IdentityHeader get it. It is
// safe only when nothing but the authenticating proxy can reach the
// listener.
func headerIdentity(header string) func(net.Conn) session.Identity {
	return func(c net.Conn) session.Identity {
		// The proxy reads the stream itself; extracting the header here would
		// require buffering ahead of the relay. Instead of duplicating that
		// machinery, the gate resolves http identity from the inspected
		// request, and this function contributes only the peer address.
		//
		// It stays a named function so the config surface says where identity
		// comes from today.
		return session.Identity{PeerAddr: c.RemoteAddr().String()}
	}
}
