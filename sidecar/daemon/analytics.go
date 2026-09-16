package daemon

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hoophq/hoop/sidecar/analytics"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// This file is where the daemon turns what it knows into analytics events.
// The client and the event names live in the analytics package; the
// PROPERTIES are assembled here, because only this package sees a resolved
// lane, a license verdict or a reload outcome.
//
// The rule every property follows is the /config endpoint's: counts and
// shape, never content. No lane name, no upstream, no rule name, no prompt.
// A new property that names something an operator wrote does not land.
//
// Adding an event: name it in analytics/events.go, add a track* method here
// that builds its properties, call it from the place in Run (or the
// reloader, or FirstRun) where the fact becomes true.

// usageEvery is the ticker for EventUsage. Fifteen minutes is coarse enough
// that a busy relay costs four events an hour and fine enough that a
// dashboard sees a deploy's traffic on the day it happened.
const usageEvery = 15 * time.Minute

// telemetry is the process-wide analytics state Run holds: the client, the
// data-path counters every gate writes into, and the lifetime totals the
// stopped event reports.
//
// Every method is safe on a nil receiver, so a reloader or heartbeat built
// by a test without one stays quiet instead of branching.
type telemetry struct {
	client   *analytics.Client
	counters *analytics.Counters
	started  time.Time

	reloadsApplied atomic.Int64
	reloadsRestart atomic.Int64
	reloadsRefused atomic.Int64

	// usageMu serializes trackUsage: the ticker goroutine and the final
	// call at shutdown both cut a delta, and two at once would double-count
	// or lose the window between them.
	usageMu sync.Mutex
	// lastUsage is when the previous usage event was cut, so each one
	// carries the real interval rather than the nominal ticker.
	lastUsage time.Time
	// conns is the lifetime connection total the previous usage event
	// saw; the servers only hold running totals.
	conns connSnapshot
}

// newTelemetry builds the client from what Setup learned. A control plane
// token gives a stable id across restarts; a standalone process gets one
// per boot, which is the honest reading of an install with no identity.
func newTelemetry(cfg *Config, log *slog.Logger) *telemetry {
	opts := analytics.Options{
		Version:      Version,
		Entrypoint:   cfg.entrypoint,
		ControlPlane: cfg.cp != nil,
	}
	if opts.Entrypoint == "" {
		opts.Entrypoint = analytics.EntrypointEmbedded
	}
	if cfg.cp != nil {
		opts.SidecarID = analytics.IDFromToken(cfg.cp.token)
	}
	now := time.Now()
	t := &telemetry{
		client:    analytics.New(opts),
		counters:  analytics.NewCounters(),
		started:   now,
		lastUsage: now,
	}
	if t.client.Enabled() {
		log.Info("usage analytics enabled", "disable", analytics.EnvVar+"=off")
	}
	return t
}

// laneMetrics returns the counter a lane's gates report into, keyed by
// protocol so two lanes speaking postgres share one. The return type is the
// interface, so a nil telemetry yields a nil interface and not a typed nil
// pointer the gate would call through.
func (t *telemetry) laneMetrics(protocol string) gate.Metrics {
	if t == nil {
		return nil
	}
	return t.counters.Lane(protocol)
}

// heartbeatFailed counts one failed control plane handshake.
func (t *telemetry) heartbeatFailed() {
	if t != nil {
		t.counters.HeartbeatFailed()
	}
}

// bootProperties are the facts fixed at startup: how the process was
// invoked and where its config came from. control_plane_disk is a plane
// that delegated the document to the local file: connected, but the file
// is the authority.
func (t *telemetry) bootProperties(cfg *Config) analytics.Properties {
	source := "file"
	switch {
	case cfg.cp != nil && cfg.cp.diskMode:
		source = "control_plane_disk"
	case cfg.cp != nil:
		source = "control_plane"
	}
	p := analytics.Properties{
		"deprecated-alias":       cfg.deprecatedAlias,
		"config-source":          source,
		"config-format":          cfg.configFormat,
		"license-state":          cfg.lic.State().String(),
		"license-required":       cfg.dependsOnLicense(),
		"deprecations-count":     len(cfg.Deprecations),
		"control-plane-imported": false,
		"file-listeners-ignored": false,
	}
	if cfg.cp != nil {
		p["control-plane-imported"] = cfg.cp.imported
		p["file-listeners-ignored"] = cfg.cp.fileListeners > 0
	}
	return p
}

// shapeProperties describe the resolved enforcement stack as counts. It
// reads the same lane facts /config renders and reports none of the names.
func shapeProperties(cfg *Config, lanes []lane, det Plugin) analytics.Properties {
	protocols := make(map[string]int, 4)
	var enforcing, observing, withRules, withOPA, masking, withAnalyzer,
		captureBody, identityHeader, upstreamTLS, downstreamTLS int
	for _, ln := range lanes {
		protocols[ln.cfg.Protocol]++
		if ln.observing {
			observing++
		} else if ln.policy != nil {
			enforcing++
		}
		if ln.policy != nil {
			withRules++
		}
		if ln.opaURL != "" {
			withOPA++
		}
		if ln.masker != nil {
			masking++
		}
		if ln.cfg.Analyzer != nil || len(ln.analyzed) > 0 {
			withAnalyzer++
		}
		if ln.captureBody {
			captureBody++
		}
		if ln.cfg.IdentityHeader != "" {
			identityHeader++
		}
		if ln.cfg.UpstreamTLS != nil {
			upstreamTLS++
		}
		if ln.cfg.DownstreamTLS != nil {
			downstreamTLS++
		}
	}
	_, guardrailTotal := cfg.guardrailSites()
	_, maskTotal, _ := cfg.maskSites()

	sinks := make([]string, 0, 3)
	if cfg.Audit.File != "" {
		sinks = append(sinks, "jsonl")
	}
	if cfg.Audit.MemoryBuffer > 0 {
		sinks = append(sinks, "memory")
	}
	if cfg.Audit.QuerySessions > 0 {
		sinks = append(sinks, "query")
	}

	p := analytics.Properties{
		"lane-count":            len(lanes),
		"protocols":             protocols,
		"lanes-enforcing":       enforcing,
		"lanes-observing":       observing,
		"lanes-with-rules":      withRules,
		"guardrail-rules-total": guardrailTotal,
		"lanes-with-opa":        withOPA,
		"lanes-masking":         masking,
		"mask-rules-total":      maskTotal,
		"pii-configured":        len(cfg.PII) > 0,
		"detector-attached":     det != nil,
		"lanes-with-analyzer":   withAnalyzer,
		"lanes-capture-body":    captureBody,
		"lanes-identity-header": identityHeader,
		"lanes-upstream-tls":    upstreamTLS,
		"lanes-downstream-tls":  downstreamTLS,
		"audit-sinks":           sinks,
		"audit-async":           cfg.Audit.AsyncQueueSize > 0,
		"fail-on-audit-error":   cfg.Audit.failOnAuditError(),
		"admin-enabled":         cfg.Admin.Listen != "",
		"log-level":             cfg.LogLevel,
	}
	if a := cfg.Analyzer; a != nil {
		p["analyzer-provider"] = a.Provider
		p["analyzer-model"] = a.Model
		p["analyzer-send"] = string(sendModeOrDefault(a.Send))
		p["analyzer-fail-open"] = a.failOpen()
		p["analyzer-custom-prompt"] = a.Prompt != ""
	}
	return p
}

// trackStarted emits EventStarted: boot facts plus the config shape.
func (t *telemetry) trackStarted(cfg *Config, lanes []lane, det Plugin) {
	if t == nil {
		return
	}
	p := t.bootProperties(cfg)
	for k, v := range shapeProperties(cfg, lanes, det) {
		p[k] = v
	}
	t.client.Track(analytics.EventStarted, p)
}

// trackReload emits EventConfigApplied for every heartbeat outcome that
// changed or refused something. Unchanged and retry outcomes are silence:
// the first is the steady state and the second re-runs next tick. cfg and
// lanes are the applied generation; nil for the outcomes that applied none.
func (t *telemetry) trackReload(out reloadOutcome, gen, swapped, kept int, cfg *Config, lanes []lane, det Plugin) {
	if t == nil {
		return
	}
	var outcome string
	switch out {
	case reloadApplied:
		t.reloadsApplied.Add(1)
		outcome = "applied"
	case reloadRestart:
		t.reloadsRestart.Add(1)
		outcome = "restart-required"
	case reloadRefused:
		t.reloadsRefused.Add(1)
		outcome = "refused"
	default:
		return
	}
	p := analytics.Properties{
		"config-generation": gen,
		"outcome":           outcome,
		"lanes-swapped":     swapped,
		"lanes-kept":        kept,
	}
	if out == reloadApplied && cfg != nil {
		for k, v := range shapeProperties(cfg, lanes, det) {
			p[k] = v
		}
	}
	t.client.Track(analytics.EventConfigApplied, p)
}

// trackUsage emits EventUsage with the deltas since the previous one.
// Connection counters come from the servers, which hold lifetime totals;
// statement counters come from the gates through Counters.
func (t *telemetry) trackUsage(servers []*proxy.Server, grpcServers []GRPCServer) {
	if t == nil {
		return
	}
	t.usageMu.Lock()
	defer t.usageMu.Unlock()
	now := time.Now()
	u := t.counters.Snapshot()
	cur := snapshotConns(servers, grpcServers)
	p := analytics.Properties{
		"interval-seconds":   int64(now.Sub(t.lastUsage).Seconds()),
		"uptime-seconds":     int64(now.Sub(t.started).Seconds()),
		"connections-total":  cur.total - t.conns.total,
		"connections-active": cur.active,
		"connections-denied": cur.denied - t.conns.denied,
		"statements-total":   u.Statements,
		"statements-denied":  u.Denied,
		"statements-masked":  u.Masked,
		"heartbeat-failures": u.HeartbeatFailures,
		"by-protocol":        u.ByProtocol,
	}
	t.conns = cur
	t.lastUsage = now
	t.client.Track(analytics.EventUsage, p)
}

// connSnapshot is the lifetime connection totals across every server at
// one instant; two of them make a delta.
type connSnapshot struct {
	active, total, denied int64
}

func snapshotConns(servers []*proxy.Server, grpcServers []GRPCServer) connSnapshot {
	var s connSnapshot
	for _, srv := range servers {
		a, tot, d := srv.Stats()
		s.active += a
		s.total += tot
		s.denied += d
	}
	for _, srv := range grpcServers {
		a, tot, d := srv.Stats()
		s.active += a
		s.total += tot
		s.denied += d
	}
	return s
}

// Stop reasons for EventStopped.
const (
	stopSignal         = "signal"
	stopListenerFailed = "listener-failed"
	stopLicenseExpired = "license-expired"
)

// trackStopped emits EventStopped. Run cuts the final EventUsage first, so
// the usage series is complete on its own.
func (t *telemetry) trackStopped(reason string) {
	if t == nil {
		return
	}
	t.client.Track(analytics.EventStopped, analytics.Properties{
		"reason":                   reason,
		"uptime-seconds":           int64(time.Since(t.started).Seconds()),
		"reloads-applied":          t.reloadsApplied.Load(),
		"reloads-restart-required": t.reloadsRestart.Load(),
		"reloads-refused":          t.reloadsRefused.Load(),
	})
}

// trackLicenseExpired emits EventLicenseExpired with what exceeded the
// free tier.
func (t *telemetry) trackLicenseExpired(cfg *Config) {
	if t == nil {
		return
	}
	_, guardrailTotal := cfg.guardrailSites()
	_, maskTotal, _ := cfg.maskSites()
	t.client.Track(analytics.EventLicenseExpired, analytics.Properties{
		"guardrail-rules-total": guardrailTotal,
		"mask-rules-total":      maskTotal,
		"uptime-seconds":        int64(time.Since(t.started).Seconds()),
	})
}

// close flushes and stops the client. Bounded by the client's own
// deadline, so shutdown never waits on Segment.
func (t *telemetry) close() {
	if t != nil {
		t.client.Close()
	}
}
