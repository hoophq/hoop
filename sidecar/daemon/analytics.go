package daemon

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hoophq/hoop/sidecar/analytics"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/policy"
)

// This file is where the daemon turns what it knows into analytics events.
// The client and the event names live in the analytics package; the
// PROPERTIES are assembled here, because only this package sees a resolved
// lane, a license verdict or a reload outcome.
//
// The rule every property follows is the /config endpoint's: counts and
// shape, never content. No lane name, no upstream, no rule name, no prompt.
// A new property that names something an operator wrote does not land;
// TestAnalyticsPropertiesCarryNoOperatorContent is the check.
//
// Adding an event: name it in analytics/events.go, add a track* method here
// that builds its properties, call it from the place in Run (or the
// reloader, or FirstRun) where the fact becomes true.

// usageEvery is the ticker for EventUsage. Fifteen minutes is coarse enough
// that a busy relay costs four events an hour and fine enough that a
// dashboard sees a deploy's traffic on the day it happened.
const usageEvery = 15 * time.Minute

// statSource is one running server's connection counters, tagged with the
// protocol its lane speaks. Both server kinds satisfy it.
type statSource struct {
	protocol string
	stats    interface {
		Stats() (active, total, denied int64)
	}
}

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
	// conns is the lifetime connection total per protocol the previous
	// usage event saw; the servers only hold running totals.
	conns map[string]connSnapshot
	// analyzers is the Stats each evaluator reported at the previous usage
	// event, keyed by instance. A reload that swaps a lane's evaluators
	// starts them from zero, and keying by pointer keeps that from reading
	// as a negative delta.
	analyzers map[*analyzer.Evaluator]analyzer.Stats
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
		conns:     make(map[string]connSnapshot),
		analyzers: make(map[*analyzer.Evaluator]analyzer.Stats),
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

// licenseType is the fixed oss|enterprise vocabulary the verifier enforces:
// the free tier unless a document verified.
func licenseType(lic license.Status) string {
	if lic.State() == license.StateMissing || lic.License == nil {
		return license.OSSType
	}
	return lic.License.Payload.Type
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
		"license-type":           licenseType(cfg.lic),
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
		if len(ln.analyzers) > 0 {
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
	if det != nil {
		// How many entity classes the detector looks for: the whole
		// catalogue when the pii section is absent, fewer when it narrows.
		p["pii-entities"] = len(det.Entities())
	}
	// Provider is one of the registered names (openai, anthropic, vertex);
	// the model is free text the operator typed, so it stays out. Same
	// rule as the prompt: whether one is set, never what it says.
	if a := cfg.Analyzer; a != nil {
		p["analyzer-provider"] = a.Provider
		p["analyzer-send"] = string(sendModeOrDefault(a.Send))
		p["analyzer-fail-open"] = a.failOpen()
		p["analyzer-custom-prompt"] = a.Prompt != ""
	}
	return p
}

// collectAnalyzers walks a lane's built evaluator and returns the analyzer
// instances inside it, so usage can read their Stats. buildPolicy composes
// Chain and Observe and nothing else, so those are the two shapes to open.
func collectAnalyzers(ev policy.Evaluator) []*analyzer.Evaluator {
	switch e := ev.(type) {
	case *analyzer.Evaluator:
		return []*analyzer.Evaluator{e}
	case policy.Chain:
		var out []*analyzer.Evaluator
		for _, inner := range e {
			out = append(out, collectAnalyzers(inner)...)
		}
		return out
	case policy.Observe:
		return collectAnalyzers(e.Evaluator)
	}
	return nil
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

// reloadReport is what the reloader hands trackReload: the outcome and,
// when a document applied, what changed and the generation it produced.
type reloadReport struct {
	outcome reloadOutcome
	gen     int
	swapped int
	kept    int
	// changed names the config sections that differed from the running
	// generation: guardrails, opa, mask, analyzer, pii. Fixed vocabulary.
	changed []string
	cfg     *Config
	lanes   []lane
	det     Plugin
}

// trackReload emits EventConfigApplied for every heartbeat outcome that
// changed or refused something. Unchanged and retry outcomes are silence:
// the first is the steady state and the second re-runs next tick.
func (t *telemetry) trackReload(r reloadReport) {
	if t == nil {
		return
	}
	var outcome string
	switch r.outcome {
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
		"config-generation": r.gen,
		"outcome":           outcome,
		"lanes-swapped":     r.swapped,
		"lanes-kept":        r.kept,
	}
	if r.changed != nil {
		p["changed"] = r.changed
	}
	if r.outcome == reloadApplied && r.cfg != nil {
		for k, v := range shapeProperties(r.cfg, r.lanes, r.det) {
			p[k] = v
		}
	}
	t.client.Track(analytics.EventConfigApplied, p)
}

// protoUsage is one protocol's row in by-protocol: statement counters from
// the gates plus connection counters from the servers.
type protoUsage struct {
	Statements        int64 `json:"statements"`
	Denied            int64 `json:"denied"`
	Masked            int64 `json:"masked"`
	Connections       int64 `json:"connections"`
	ConnectionsDenied int64 `json:"connections-denied"`
}

// trackUsage emits EventUsage with the deltas since the previous one.
// sources are the running servers with their protocols; lanes are the
// generation currently serving, read for their analyzer instances.
func (t *telemetry) trackUsage(sources []statSource, lanes []lane) {
	if t == nil {
		return
	}
	t.usageMu.Lock()
	defer t.usageMu.Unlock()
	now := time.Now()
	u := t.counters.Snapshot()

	// Connections: lifetime totals per protocol, turned into deltas
	// against what the previous event saw.
	cur := make(map[string]connSnapshot, len(sources))
	var active, connTotal, connDenied int64
	for _, s := range sources {
		a, tot, d := s.stats.Stats()
		c := cur[s.protocol]
		c.active += a
		c.total += tot
		c.denied += d
		cur[s.protocol] = c
	}
	byProto := make(map[string]protoUsage, len(cur))
	for proto, c := range cur {
		prev := t.conns[proto]
		row := protoUsage{
			Connections:       c.total - prev.total,
			ConnectionsDenied: c.denied - prev.denied,
		}
		if lu, ok := u.ByProtocol[proto]; ok {
			row.Statements, row.Denied, row.Masked = lu.Statements, lu.Denied, lu.Masked
		}
		active += c.active
		connTotal += row.Connections
		connDenied += row.ConnectionsDenied
		if row != (protoUsage{}) {
			byProto[proto] = row
		}
	}
	// A protocol with statements but no server row (a test lane) still
	// reports its statements.
	for proto, lu := range u.ByProtocol {
		if _, ok := byProto[proto]; !ok {
			byProto[proto] = protoUsage{Statements: lu.Statements, Denied: lu.Denied, Masked: lu.Masked}
		}
	}
	t.conns = cur

	// Analyzer: deltas per evaluator instance, summed. Instances that left
	// with a reload take their tail with them; the next generation starts
	// from its own zero.
	var calls, failures, failOpen, denied, cacheHits int64
	seen := make(map[*analyzer.Evaluator]analyzer.Stats)
	for _, ln := range lanes {
		for _, ev := range ln.analyzers {
			if _, dup := seen[ev]; dup {
				continue
			}
			s := ev.Stats()
			prev := t.analyzers[ev]
			seen[ev] = s
			calls += s.Calls - prev.Calls
			denied += s.Denied - prev.Denied
			cacheHits += int64(s.CacheHits - prev.CacheHits)
			errs := s.Errors - prev.Errors
			failures += errs
			if ev.FailOpen() {
				failOpen += errs
			}
		}
	}
	t.analyzers = seen

	p := analytics.Properties{
		"interval-seconds":        int64(now.Sub(t.lastUsage).Seconds()),
		"uptime-seconds":          int64(now.Sub(t.started).Seconds()),
		"connections-total":       connTotal,
		"connections-active":      active,
		"connections-denied":      connDenied,
		"statements-total":        u.Statements,
		"statements-denied":       u.Denied,
		"statements-masked":       u.Masked,
		"denies-by-kind":          u.DeniedBy,
		"analyzer-calls":          calls,
		"analyzer-failures":       failures,
		"analyzer-fail-open-hits": failOpen,
		"analyzer-denied":         denied,
		"analyzer-cache-hits":     cacheHits,
		"audit-write-failures":    u.AuditErrors,
		"heartbeat-failures":      u.HeartbeatFailures,
		"by-protocol":             byProto,
	}
	t.lastUsage = now
	t.client.Track(analytics.EventUsage, p)
}

// connSnapshot is one protocol's lifetime connection totals at an instant;
// two of them make a delta.
type connSnapshot struct {
	active, total, denied int64
}

// Stop reasons for EventStopped.
const (
	stopSignal         = "signal"
	stopListenerFailed = "listener-failed"
	stopLicenseExpired = "license-expired"
)

// listenerErrorKind classifies a listener failure into the fixed
// bind|tls|other vocabulary. Never the message: it names an address.
func listenerErrorKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EADDRNOTAVAIL) {
		return "bind"
	}
	var certErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	if errors.As(err, &certErr) || errors.As(err, &recErr) {
		return "tls"
	}
	if msg := err.Error(); strings.Contains(msg, "tls") || strings.Contains(msg, "x509") {
		return "tls"
	}
	return "other"
}

// trackStopped emits EventStopped. Run cuts the final EventUsage first, so
// the usage series is complete on its own. listenerErr is the failure that
// stopped the process, nil for every other reason.
func (t *telemetry) trackStopped(reason string, listenerErr error) {
	if t == nil {
		return
	}
	p := analytics.Properties{
		"reason":                   reason,
		"uptime-seconds":           int64(time.Since(t.started).Seconds()),
		"reloads-applied":          t.reloadsApplied.Load(),
		"reloads-restart-required": t.reloadsRestart.Load(),
		"reloads-refused":          t.reloadsRefused.Load(),
	}
	if kind := listenerErrorKind(listenerErr); kind != "" {
		p["listener-error-kind"] = kind
	}
	t.client.Track(analytics.EventStopped, p)
}

// trackLicenseExpired emits EventLicenseExpired with what exceeded the
// free tier and how the term ran. over-cap is the sibling stop: the term
// is fine, the entitlement no longer covers the rules.
func (t *telemetry) trackLicenseExpired(cfg *Config, st *licenseState) {
	if t == nil {
		return
	}
	_, guardrailTotal := cfg.guardrailSites()
	_, maskTotal, _ := cfg.maskSites()
	lic := st.get()
	p := analytics.Properties{
		"guardrail-rules-total": guardrailTotal,
		"mask-rules-total":      maskTotal,
		"uptime-seconds":        int64(time.Since(t.started).Seconds()),
		"license-state":         lic.State().String(),
		"license-type":          licenseType(lic),
		"over-cap":              st.overCap.Load(),
		"expiry-notices-sent":   st.notices.Load(),
	}
	if l := lic.License; l != nil && l.Payload.ExpireAt > l.Payload.IssuedAt {
		p["license-term-days"] = (l.Payload.ExpireAt - l.Payload.IssuedAt) / 86400
	}
	t.client.Track(analytics.EventLicenseExpired, p)
}

// sortedKeys renders a set as a stable list for a property.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// close flushes and stops the client. Bounded by the client's own
// deadline, so shutdown never waits on Segment.
func (t *telemetry) close() {
	if t != nil {
		t.client.Close()
	}
}
