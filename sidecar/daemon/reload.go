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
	// reloadApplied swapped the drift into the running process: rules
	// into the lanes, or a disk-mode license into the shared holder.
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

	// lic is the shared holder Run publishes the running license through.
	// A disk-mode reload stores a drifted license here; rule reloads read
	// it so the caps follow the running license.
	lic *atomic.Pointer[license.Status]
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
	// diskMode reports the plane delegated the config to the local file:
	// a license drift hot-applies through lic, and an ownership flip runs
	// the incoming owner's document through applyOwned, flipping this
	// only when it applied.
	diskMode bool
	// configPath and load re-read the config file when the plane hands
	// ownership to it mid-run. An empty path means no file was given.
	configPath string
	load       Loader
}

// newReloader captures the startup state handle compares against.
func newReloader(cfg *Config, lanes []lane, servers map[string]*proxy.Server,
	det Plugin, ac *analyzerDeps, view *atomic.Pointer[laneState],
	lic *atomic.Pointer[license.Status]) (*reloader, error) {
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
		lic:         lic,
		det:         det,
		ac:          ac,
		build:       cfg.cp.build,
		lastHandled: cfg.cp.lastRaw,
		diskMode:    cfg.cp.diskMode,
		configPath:  cfg.cp.configPath,
		load:        cfg.cp.load,
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
//
// The load_from_disk flag flipping is handled here too, hot when the
// documents allow it: the incoming owner's document (the plane's full
// answer on a takeover, the re-read config file on a handover) runs through
// the same swap-or-restart decision as any owned drift, and the mode flips
// only on a document that applied.
func (r *reloader) apply(log *slog.Logger, raw []byte) reloadOutcome {
	var planeDoc Config
	on := json.Unmarshal(raw, &planeDoc) == nil &&
		planeDoc.LoadFromDisk != nil && *planeDoc.LoadFromDisk
	switch {
	case on && r.diskMode:
		// Same flag, different bytes: only the license moved.
		return r.applyLicense(log, planeDoc.License)
	case on && !r.diskMode:
		return r.adoptFile(log, planeDoc.License)
	case !on && r.diskMode:
		out := r.applyOwned(log, raw, "control plane")
		if out == reloadApplied {
			r.diskMode = false
			log.Info("the control plane took ownership of the configuration; " +
				"the config file's document no longer serves")
		}
		return out
	}
	return r.applyOwned(log, raw, "control plane")
}

// adoptFile is the handover flip: the plane's answer turned into the tiny
// disk-mode document, so the config file is the source of truth from here.
// The file is re-read — load_from_disk means the FILE, not a startup
// snapshot of it — and its document runs through the same swap-or-restart
// decision as any owned drift. A file this process cannot hot-adopt stays
// on the restart path, where startup either serves it or refuses it by
// name.
func (r *reloader) adoptFile(log *slog.Logger, planeLicense string) reloadOutcome {
	if r.configPath == "" {
		log.Warn("the control plane handed the configuration to the local file, " +
			"but this process was started without a config file; restart with one to apply it")
		return reloadRestart
	}
	local, err := r.load(r.configPath)
	if err != nil {
		log.Warn("the control plane handed the configuration to the local file, "+
			"but the file does not load; fix it and restart to apply it",
			"path", r.configPath, "error", err)
		return reloadRestart
	}
	if local.LoadFromDisk != nil {
		log.Warn(`the control plane handed the configuration to the local file, `+
			`but the file writes "load_from_disk", which only the control plane says; `+
			`remove the key and restart to apply it`, "path", r.configPath)
		return reloadRestart
	}
	if len(local.Listeners) == 0 {
		log.Warn("the control plane handed the configuration to the local file, "+
			"but the file declares no listeners; restart to apply it", "path", r.configPath)
		return reloadRestart
	}
	raw, err := json.Marshal(local)
	if err != nil {
		log.Warn("config compare failed; keeping the running rules", "error", err)
		return reloadRetry
	}
	out := r.applyOwned(log, raw, "config file")
	if out != reloadApplied {
		return out
	}
	r.diskMode = true
	log.Info("the control plane handed the configuration to the local file; "+
		"the file's document now serves", "path", r.configPath)
	if planeLicense != "" {
		// The tiny document's license, through the same seam as startup;
		// a refused one keeps the running license without undoing the
		// adopted lanes.
		if s := license.Load(license.Ref{Value: planeLicense,
			Source: controlPlaneLicenseSource}); s.State() == license.StateInvalid {
			log.Warn("the control plane sent a license this build refuses; keeping the running one",
				"error", s.Err)
		} else {
			r.lic.Store(&s)
		}
	}
	return reloadApplied
}

// applyOwned runs one owner's full document against the running process:
// rule-only drift swaps, anything beyond the rules is restart-bound. from
// names the document's owner in the logs: "control plane" or "config file".
func (r *reloader) applyOwned(log *slog.Logger, raw []byte, from string) reloadOutcome {
	newCfg, err := LoadConfigBytes(raw)
	if err != nil {
		log.Warn("the "+from+" sent a config this build refuses; keeping the running rules",
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
		log.Warn("the " + from + " changed the configuration beyond the rules; restart to apply it")
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
	}

	// The candidate detector is STAGED, never written into r.ac before the
	// document is accepted: a refusal below must leave the running
	// dependencies exactly as they were, or a later reload would combine
	// this uncommitted detector with the running maskers and pii rules.
	// The copy shares the budgets map on purpose — budget continuity is
	// per name, not per generation.
	ac := r.ac
	if detChanged && r.ac != nil {
		staged := *r.ac
		staged.det = det
		ac = &staged
	}

	newCfg.lic = *r.lic.Load()
	lanes, err := buildLanes(newCfg, det, ac)
	if err != nil {
		log.Warn("the "+from+" sent a config the rules or the caps refuse; keeping the running rules",
			"error", err)
		return reloadRefused
	}

	// Pre-pass: render every lane's rule document BEFORE anything swaps.
	// Two reasons. A gRPC-transport lane cannot swap (ADR-0013 binds its
	// callbacks at server construction), so drift there makes the whole
	// document restart-bound: swapping the relay lanes and reporting the
	// generation applied would leave a lane silently serving old rules
	// under a log line that says otherwise. And a render failure must
	// surface before any lane swapped, so a retry re-runs the whole
	// document rather than half of it.
	docs := make(map[string][]byte, len(lanes))
	for _, ln := range lanes {
		doc, derr := laneRuleDoc(newCfg, ln.cfg)
		if derr != nil {
			log.Warn("lane compare failed; keeping the running rules",
				"listener", ln.name, "error", derr)
			return reloadRetry
		}
		docs[ln.name] = doc
		if isGRPCTransport(ln.cfg) && !bytes.Equal(doc, r.laneDocs[ln.name]) {
			log.Warn("grpc lane rules changed on the "+from+"; restart to apply them",
				"listener", ln.name)
			return reloadRestart
		}
	}

	swapped, kept := 0, 0
	viewLanes := make([]lane, 0, len(lanes))
	for _, ln := range lanes {
		doc := docs[ln.name]
		if isGRPCTransport(ln.cfg) {
			// Unchanged by the pre-pass check above; the view keeps the
			// serving lane.
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

	// Commit, all together: the detector, the pii section it came from and
	// the analyzer dependencies move as one, only on a document that
	// applied.
	if detChanged && r.ac != nil {
		r.ac.det = det
	}
	r.det = det
	r.piiRaw = newCfg.PII
	r.gen++
	r.view.Store(&laneState{lanes: viewLanes, gen: r.gen})
	log.Info(from+" configuration applied",
		"generation", r.gen, "swapped", swapped, "kept", kept)
	return reloadApplied
}

// applyLicense is the disk-mode drift: the plane's answer carries only the
// flag and the license, so different bytes mean the license moved. A
// verified document swaps into the shared holder — a renewal must not cost
// a restart — and the expiry watcher picks the new term up on its next
// tick. Same seam and same precedence as startup's UseLicense: the control
// plane goes above every local source.
func (r *reloader) applyLicense(log *slog.Logger, doc string) reloadOutcome {
	if doc == "" {
		// Removal narrows the caps, and a running lane may exceed them.
		// Same posture as expiry (watchLicense holds the rationale): a
		// controlled restart, where startup re-resolves the local sources
		// or refuses the config by name.
		log.Warn("the control plane removed the license; restart to apply it")
		return reloadRestart
	}
	s := license.Load(license.Ref{Value: doc, Source: controlPlaneLicenseSource})
	if s.State() == license.StateInvalid {
		log.Warn("the control plane sent a license this build refuses; keeping the running one",
			"error", s.Err)
		return reloadRefused
	}
	r.lic.Store(&s)
	log.Info("control plane license applied: " + s.Line())
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
