package daemon

import (
	"bytes"
	"encoding/json"
	"log/slog"

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

// reloadOutcome is what apply concluded, returned so a test asserts the
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
)

// reloader holds what a running process needs to rebuild and swap lane
// rules. Run assembles one when a control plane is connected; the heartbeat
// owns it afterwards, so nothing here needs a lock.
type reloader struct {
	// baseline is the running config's non-rule document. A fetched config
	// whose non-rule document differs needs a restart.
	baseline []byte
	// piiRaw is the pii section the running detector was built from. Only
	// consulted when build is nil: with a builder the section is rebuilt
	// into a new detector instead of compared.
	piiRaw []byte
	// grpcRules maps each grpc lane to the rule document it started with.
	// gRPC lanes stay on the restart path (ADR-0014), so drift there is
	// reported, never swapped.
	grpcRules map[string][]byte
	// servers maps relay lane names to their running servers, the swap
	// targets.
	servers map[string]*proxy.Server

	lic   license.Status
	det   Plugin
	build PluginBuilder

	// gen counts applied reloads, so log lines can say which config
	// generation the lanes accepted from.
	gen int
}

// newReloader captures the startup state apply compares against. lanes are
// the built startup lanes; servers pairs each relay lane name with its
// server, in lane order.
func newReloader(cfg *Config, lanes []lane, servers map[string]*proxy.Server, det Plugin) (*reloader, error) {
	baseline, err := nonRuleDoc(cfg)
	if err != nil {
		return nil, err
	}
	grpcRules := map[string][]byte{}
	for _, ln := range lanes {
		if !isGRPC(ln.cfg) {
			continue
		}
		doc, err := laneRuleDoc(cfg, ln.cfg)
		if err != nil {
			return nil, err
		}
		grpcRules[ln.name] = doc
	}
	return &reloader{
		baseline:  baseline,
		piiRaw:    cfg.PII,
		grpcRules: grpcRules,
		servers:   servers,
		lic:       cfg.lic,
		det:       det,
		build:     cfg.cp.build,
	}, nil
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
		log.Warn("config compare failed; keeping the running rules", "error", err)
		return reloadRefused
	}
	if !bytes.Equal(doc, r.baseline) {
		log.Warn("the control plane configuration changed beyond the rules; restart to apply it")
		return reloadRestart
	}

	det := r.det
	if r.build != nil {
		det, err = r.build(newCfg.PII)
		if err != nil {
			log.Warn("detector rebuild failed; keeping the running rules", "error", err)
			return reloadRefused
		}
	} else if !bytes.Equal(bytes.TrimSpace(r.piiRaw), bytes.TrimSpace(newCfg.PII)) {
		log.Warn("the pii section changed and this build retained no detector builder; restart to apply it")
		return reloadRestart
	}

	// The analyzer section is inside the baseline, so it is identical here;
	// the deps still rebuild when a builder ran, because the redactor holds
	// the detector. The startup credential probe is not repeated: the
	// provider config cannot have changed.
	ac, err := setupAnalyzer(newCfg, det)
	if err != nil {
		log.Warn("analyzer rebuild failed; keeping the running rules", "error", err)
		return reloadRefused
	}

	newCfg.lic = r.lic
	lanes, err := buildLanes(newCfg, det, ac)
	if err != nil {
		log.Warn("the control plane sent a config the rules or the caps refuse; keeping the running rules",
			"error", err)
		return reloadRefused
	}

	swapped := 0
	for _, ln := range lanes {
		if isGRPC(ln.cfg) {
			doc, derr := laneRuleDoc(newCfg, ln.cfg)
			if derr == nil && !bytes.Equal(doc, r.grpcRules[ln.name]) {
				log.Warn("grpc lane rules changed on the control plane; restart to apply them",
					"listener", ln.name)
			}
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
		swapped++
	}

	r.det = det
	r.piiRaw = newCfg.PII
	r.gen++
	log.Info("control plane configuration applied",
		"generation", r.gen, "lanes", swapped)
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
		listeners[i] = lc
	}
	cp.Listeners = listeners
	return json.Marshal(&cp)
}

// laneRuleDoc renders one listener's RESOLVED rule stack, for the per-lane
// drift report on grpc lanes.
func laneRuleDoc(c *Config, lc ListenerConfig) ([]byte, error) {
	gc, opa, mc := c.resolve(lc)
	return json.Marshal(struct {
		G GuardrailsConfig `json:"g"`
		O *OPAConfig       `json:"o"`
		M MaskConfig       `json:"m"`
	}{gc, opa, mc})
}
