package daemon

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"sync/atomic"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// This file is the ADR-0014 hot reload: when the control plane's config
// drifts in rule content only, the running relay lanes swap evaluators and
// maskers atomically instead of asking for a restart. Connections already
// open keep the Gate they captured at accept time; connections accepted
// after the swap run the new rules.
//
// The boundary is the non-rule document: listener topology, audit, admin,
// log_level and the analyzer section are bound at startup (sockets, sinks,
// loggers, provider credentials), so any drift there keeps the restart log.
//
// Everything here runs on the heartbeat goroutine alone. Run hands the
// reloader over before starting it and never touches it again, which is why
// no field needs a lock; the one cross-goroutine surface is laneState,
// published through an atomic pointer the admin endpoints load.

// reloadOutcome is what handle concluded, returned so a test asserts the
// decision rather than parsing log lines.
type reloadOutcome int

const (
	// reloadApplied swapped the rules into the running lanes.
	reloadApplied reloadOutcome = iota
	// reloadRestart names drift a live process cannot absorb.
	reloadRestart
	// reloadRefused kept the running rules because the new document failed
	// the same checks startup runs.
	reloadRefused
	// reloadUnchanged is a document this reloader already handled; nothing
	// is re-run and nothing is re-logged.
	reloadUnchanged
	// reloadRetry kept the running rules over a failure that may clear
	// without another edit, so the same document is retried next tick.
	reloadRetry
)

// laneState is one config generation as the admin endpoints see it: the
// lanes serving traffic and the generation they came from. Published
// atomically so /config renders what the data path runs, never the startup
// snapshot of a process that reloaded since (ADR-0014's admin consequence).
type laneState struct {
	lanes []lane
	gen   int
}

// reloader holds what a running process needs to rebuild and swap lane
// rules. Run assembles one when a control plane is connected; the heartbeat
// owns it afterwards.
type reloader struct {
	// baseline is the running config's non-rule document. A fetched config
	// whose non-rule document differs needs a restart.
	baseline []byte
	// piiRaw is the pii section the running detector was built from. The
	// detector is rebuilt only when this drifts, never per reload: a
	// rebuilt detector forces every masker and pii rule to swap, and most
	// reloads touch neither.
	piiRaw []byte
	// laneDocs maps every lane to the resolved rule document it is
	// serving. A lane whose document is unchanged is skipped, keeping its
	// evaluator instances alive: analyzer call budgets, verdict caches and
	// counters survive every reload that does not edit that lane's rules.
	laneDocs map[string][]byte
	// prevLanes keeps the lane structs the skipped lanes still serve, so a
	// published generation renders the truth for swapped and kept lanes
	// alike.
	prevLanes map[string]lane
	// servers maps relay lane names to their running servers, the swap
	// targets.
	servers map[string]*proxy.Server
	// view is where an applied generation is published for the admin
	// endpoints.
	view *atomic.Pointer[laneState]

	lic license.Status
	det Plugin
	// ac is the startup analyzer state, retained whole: the provider and
	// its credential are restart-guarded, so a reload never rebuilds them.
	// Only the redactor is replaced, and only when the detector changed.
	ac    *analyzerDeps
	build PluginBuilder

	// lastHandled is the last document that reached a terminal outcome.
	// Tracked apart from the fetch dedupe in the heartbeat on purpose: a
	// retryable failure leaves it alone, so the same bytes run again next
	// tick instead of waiting for another edit.
	lastHandled []byte
	gen         int
}

// newReloader captures the startup state handle compares against.
func newReloader(cfg *Config, lanes []lane, servers map[string]*proxy.Server,
	det Plugin, ac *analyzerDeps, view *atomic.Pointer[laneState]) (*reloader, error) {
	baseline, err := nonRuleDoc(cfg)
	if err != nil {
		return nil, err
	}
	laneDocs := make(map[string][]byte, len(lanes))
	prevLanes := make(map[string]lane, len(lanes))
	for _, ln := range lanes {
		doc, derr := laneRuleDoc(cfg, ln.cfg)
		if derr != nil {
			return nil, derr
		}
		laneDocs[ln.name] = doc
		prevLanes[ln.name] = ln
	}
	return &reloader{
		baseline:    baseline,
		piiRaw:      cfg.PII,
		laneDocs:    laneDocs,
		prevLanes:   prevLanes,
		servers:     servers,
		view:        view,
		lic:         cfg.lic,
		det:         det,
		ac:          ac,
		build:       cfg.cp.build,
		lastHandled: cfg.cp.lastRaw,
	}, nil
}

// handle is the heartbeat's entry: it drops documents already handled and
// remembers terminal outcomes, so one edit logs once while a retryable
// failure runs again next tick.
func (r *reloader) handle(log *slog.Logger, raw []byte) reloadOutcome {
	if bytes.Equal(raw, r.lastHandled) {
		return reloadUnchanged
	}
	out := r.apply(log, raw)
	if out != reloadRetry {
		r.lastHandled = raw
	}
	return out
}

// apply decides what a drifted document means for the running process and
// swaps it in when it can. Every refusal runs the same checks startup runs
// (LoadConfigBytes, the caps in buildLanes), so a config startup would
// refuse is refused here with the same message; the heartbeat must never be
// a side door past validation.
func (r *reloader) apply(log *slog.Logger, raw []byte) reloadOutcome {
	newCfg, err := LoadConfigBytes(raw)
	if err != nil {
		log.Warn("the control plane sent a config this build refuses; keeping the running rules",
			"error", err)
		return reloadRefused
	}

	doc, err := nonRuleDoc(newCfg)
	if err != nil {
		// A marshal failure here is internal, not a fact about the
		// document; retrying costs one compare and may clear.
		log.Warn("config compare failed; keeping the running rules", "error", err)
		return reloadRetry
	}
	if !bytes.Equal(doc, r.baseline) {
		log.Warn("the control plane configuration changed beyond the rules; restart to apply it")
		return reloadRestart
	}

	det, detChanged := r.det, false
	if !bytes.Equal(bytes.TrimSpace(r.piiRaw), bytes.TrimSpace(newCfg.PII)) {
		if r.build == nil {
			log.Warn("the pii section changed and this build retained no detector builder; restart to apply it")
			return reloadRestart
		}
		det, err = r.build(newCfg.PII)
		if err != nil {
			log.Warn("detector rebuild failed; keeping the running rules", "error", err)
			return reloadRefused
		}
		detChanged = true
		if r.ac != nil {
			// The provider and its credential stay: the analyzer section
			// is inside the baseline. Only the redactors hold the
			// detector, and each evaluator builds its own from ac.det
			// at swap time, so handing the rebuilt detector over is
			// all a pii edit needs.
			r.ac.det = det
		}
	}

	newCfg.lic = r.lic
	lanes, err := buildLanes(newCfg, det, r.ac)
	if err != nil {
		log.Warn("the control plane sent a config the rules or the caps refuse; keeping the running rules",
			"error", err)
		return reloadRefused
	}

	swapped, kept := 0, 0
	viewLanes := make([]lane, 0, len(lanes))
	for _, ln := range lanes {
		doc, derr := laneRuleDoc(newCfg, ln.cfg)
		if derr != nil {
			log.Warn("lane compare failed; keeping the running rules",
				"listener", ln.name, "error", derr)
			return reloadRetry
		}
		if isGRPCTransport(ln.cfg) {
			// gRPC-transport lanes (grpc, spanner) stay on the restart path
			// (ADR-0014); drift is reported, never swapped, and the view
			// keeps the serving lane.
			if !bytes.Equal(doc, r.laneDocs[ln.name]) {
				log.Warn("grpc lane rules changed on the control plane; restart to apply them",
					"listener", ln.name)
			}
			viewLanes = append(viewLanes, r.prevLanes[ln.name])
			continue
		}
		if !detChanged && bytes.Equal(doc, r.laneDocs[ln.name]) {
			// This lane's rules did not move, so its running evaluators
			// keep serving: analyzer call budgets, verdict caches and
			// per-rule counters survive the reload. Only an edit to THIS
			// lane's rules re-arms them.
			viewLanes = append(viewLanes, r.prevLanes[ln.name])
			kept++
			continue
		}
		srv, ok := r.servers[ln.name]
		if !ok {
			// The baseline compare guarantees the same listener set, so a
			// miss is a bug worth a line, not a silent skip.
			log.Warn("no running server for a reloaded lane", "listener", ln.name)
			continue
		}
		srv.SwapRules(ln.policy, ln.masker)
		r.laneDocs[ln.name] = doc
		r.prevLanes[ln.name] = ln
		viewLanes = append(viewLanes, ln)
		swapped++
	}

	r.det = det
	r.piiRaw = newCfg.PII
	r.gen++
	r.view.Store(&laneState{lanes: viewLanes, gen: r.gen})
	log.Info("control plane configuration applied",
		"generation", r.gen, "swapped", swapped, "kept", kept)
	return reloadApplied
}

// nonRuleDoc renders everything a hot swap cannot change: the config with
// every rule-bearing section removed. Two configs with equal documents
// differ in rules alone.
//
// A JSON render rather than a field-by-field compare, so a new Config field
// is restart-guarded by default; forgetting it here fails safe.
func nonRuleDoc(c *Config) ([]byte, error) {
	cp := *c
	cp.Guardrails, cp.OPA, cp.Mask, cp.Policy = nil, nil, nil, nil
	cp.PII = nil
	// The running config carries the resolved URL and the file's license
	// reference; the fetched one carries neither. Neither is a lane fact.
	cp.License = ""
	cp.ControlPlaneURL = ""
	listeners := make([]ListenerConfig, len(c.Listeners))
	for i, lc := range c.Listeners {
		lc.Guardrails, lc.OPA, lc.Mask, lc.Policy = nil, nil, nil, nil
		// The lane's analyzer block swaps with the rules: it builds
		// evaluators, not sockets, and its provider lives in the
		// top-level analyzer section, which stays in the baseline.
		lc.Analyzer = nil
		listeners[i] = lc
	}
	cp.Listeners = listeners
	return json.Marshal(&cp)
}

// laneRuleDoc renders one listener's RESOLVED rule stack: the skip decision
// per lane, and the drift report on grpc lanes.
func laneRuleDoc(c *Config, lc ListenerConfig) ([]byte, error) {
	gc, opa, mc := c.resolve(lc)
	return json.Marshal(struct {
		G GuardrailsConfig    `json:"g"`
		O *OPAConfig          `json:"o"`
		M MaskConfig          `json:"m"`
		A *LaneAnalyzerConfig `json:"a"`
	}{gc, opa, mc, lc.Analyzer})
}
