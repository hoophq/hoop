package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// This file is the ADR-0014 hot reload: when the running config drifts in
// rule content only, the running relay lanes swap evaluators and maskers
// atomically instead of asking for a restart. Connections already open keep
// the Gate they captured at accept time; connections accepted after the swap
// run the new rules.
//
// Two sources feed it, and a process has exactly one: the control plane's
// heartbeat when a plane owns the config, the config file otherwise. A
// standalone process polls the file's size and mtime and re-reads it on a
// change or on SIGHUP; both paths end in the same applyOwned the heartbeat
// uses, so a file edit gets the same swap, the same refusal and the same
// "restart to apply it" a plane edit gets.
//
// The boundary is the non-rule document: listener topology, audit, admin,
// log_level and the analyzer section are bound at startup (sockets, sinks,
// loggers, provider credentials), so any drift there keeps the restart log.
// A grpc lane's descriptor sets sit on that side too: the schema is bound
// into the endpoint when it is built, so a changed `descriptors` list is
// restart-bound drift, and a new version of a remote object behind an
// unchanged URL is never refetched by a reload — a restart reads it, the
// same way a restart reads a replaced file.
//
// Everything here runs on one goroutine: the heartbeat under a plane,
// watchFile otherwise. Run hands the reloader over before starting that
// goroutine and never touches it again, which is why no field needs a lock;
// the one cross-goroutine surface is laneState, published through an atomic
// pointer the admin endpoints load.

// fileWatchEvery is how often a standalone process stats its config file.
// Ten seconds keeps a Kubernetes ConfigMap edit (which the kubelet itself
// delivers on a sync period of about a minute) from adding a noticeable
// wait, and a stat every ten seconds costs nothing. Polling rather than
// inotify because the root module carries no fsnotify, and because a
// ConfigMap volume update is an atomic symlink swap that inotify on the
// file misses while a stat that follows the link sees.
const fileWatchEvery = 10 * time.Second

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

// String is the name this outcome travels under. The control plane reads it
// to tell a sidecar that took a document from one that refused it, so these
// are a wire vocabulary: rename one and a fleet view starts reading "unknown"
// for every sidecar that has not been upgraded.
func (o reloadOutcome) String() string {
	switch o {
	case reloadApplied:
		return "applied"
	case reloadRestart:
		return "restart"
	case reloadRefused:
		return "refused"
	case reloadUnchanged:
		return "unchanged"
	case reloadRetry:
		return "retry"
	}
	return "unknown"
}

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
	// sections is laneDocs split per section (guardrails, opa, mask,
	// analyzer), kept only so the applied event can name what moved.
	sections map[string]map[string][]byte
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

	// lic is the live license state, shared with the expiry watchdog and
	// the admin report. The reloader is the only writer: a plane that
	// rotates the organization's license publishes it here, and every
	// reader sees the same one.
	lic *licenseState
	// licRaw is the license document the owner last supplied -- the plane's
	// under a plane, the file's `license` key otherwise -- the compare that
	// tells a rotation from a rule edit. Empty when the owner names none.
	licRaw string
	// licSource labels a license the owner rotates in: PlaneLicenseSource
	// or fileLicenseSource. It is also the precedence rule for a file
	// process: the `license` key is the lowest of the local sources, so a
	// file edit rotates nothing while the flag or HOOP_LICENSE holds the
	// document in force -- exactly what a restart would conclude.
	licSource string
	det       Plugin
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
	// planeOwned reports a control plane owns the running config. The
	// heartbeat is then the goroutine that owns this struct, and watchFile
	// touches nothing but this flag.
	planeOwned bool
	// diskMode reports the plane delegated the config to the local file:
	// a drifted answer re-adopts the file (adoptFile), and an ownership
	// flip runs the incoming owner's document through applyOwned,
	// flipping this only when it applied.
	diskMode bool
	// configPath and load re-read the config file: a standalone process
	// on every change, a plane-owned one when the plane hands ownership to
	// the file mid-run. An empty path means no file was given.
	configPath string
	load       Loader

	// fileEvery overrides fileWatchEvery; zero means the default. A test
	// sets it so a case about what an edit does is not a case about
	// waiting ten seconds.
	fileEvery time.Duration
	// fileSize and fileMod are the stat of the file at its last read; a
	// tick that finds them unchanged reads nothing. filePending forces
	// the next tick to read anyway: the last read ended in reloadRetry or
	// in a file that did not load, and either can clear without the file
	// changing again (an editor finishing its write, a marshal that failed
	// once).
	fileSize    int64
	fileMod     time.Time
	filePending bool
	// fileBroken remembers that the file's load failure was logged, so a
	// file left broken warns once rather than every ten seconds until
	// someone fixes it. Cleared by a load that succeeds.
	fileBroken bool

	// tel receives every terminal outcome. Nil in a test that built no
	// telemetry; its methods accept that.
	tel *telemetry
}

// newReloader captures the startup state handle compares against.
func newReloader(cfg *Config, lanes []lane, servers map[string]*proxy.Server,
	det Plugin, ac *analyzerDeps, view *atomic.Pointer[laneState],
	lic *licenseState) (*reloader, error) {
	baseline, err := nonRuleDoc(cfg)
	if err != nil {
		return nil, err
	}
	laneDocs := make(map[string][]byte, len(lanes))
	laneSections := make(map[string]map[string][]byte, len(lanes))
	prevLanes := make(map[string]lane, len(lanes))
	for _, ln := range lanes {
		doc, derr := laneRuleDoc(cfg, ln.cfg)
		if derr != nil {
			return nil, derr
		}
		sections, serr := laneSectionDocs(cfg, ln.cfg)
		if serr != nil {
			return nil, serr
		}
		laneDocs[ln.name] = doc
		laneSections[ln.name] = sections
		prevLanes[ln.name] = ln
	}
	r := &reloader{
		baseline:   baseline,
		piiRaw:     cfg.PII,
		laneDocs:   laneDocs,
		sections:   laneSections,
		prevLanes:  prevLanes,
		servers:    servers,
		view:       view,
		lic:        lic,
		det:        det,
		ac:         ac,
		build:      cfg.build,
		configPath: cfg.configPath,
		load:       cfg.load,
	}
	if cfg.cp != nil {
		r.planeOwned = true
		r.licRaw = cfg.cp.license
		r.licSource = PlaneLicenseSource
		r.lastHandled = cfg.cp.lastRaw
		r.diskMode = cfg.cp.diskMode
		return r, nil
	}
	r.licRaw = cfg.License
	r.licSource = fileLicenseSource
	if r.configPath != "" {
		// Seed the compare from the file itself, through the same read the
		// watcher runs, so the first tick that finds the file untouched is
		// reloadUnchanged rather than a generation that swapped nothing. A
		// seed that fails leaves the zero values: the first tick then
		// applies a document identical to the running one, which keeps
		// every lane and costs one log line.
		if local, lerr := r.load(r.configPath); lerr == nil {
			if raw, merr := json.Marshal(local); merr == nil {
				r.lastHandled = raw
			}
		}
		if st, serr := os.Stat(r.configPath); serr == nil {
			r.fileSize, r.fileMod = st.Size(), st.ModTime()
		}
	}
	return r, nil
}

// handle is the heartbeat's entry: it drops documents already handled and
// remembers terminal outcomes, so one edit logs once while a retryable
// failure runs again next tick.
func (r *reloader) handle(log *slog.Logger, raw []byte) reloadOutcome {
	return r.once(log, raw, r.apply)
}

// once is the dedupe both sources share: a document already handled is
// dropped, a terminal outcome is remembered, a retryable one is not.
func (r *reloader) once(log *slog.Logger, raw []byte,
	apply func(*slog.Logger, []byte) reloadOutcome) reloadOutcome {
	if bytes.Equal(raw, r.lastHandled) {
		return reloadUnchanged
	}
	out := apply(log, raw)
	if out != reloadRetry {
		r.lastHandled = raw
	}
	if out != reloadApplied {
		// applyOwned reports the applied case itself, where it still holds
		// the generation's lanes for the shape properties.
		r.tel.trackReload(reloadReport{outcome: out, gen: r.gen})
	}
	return out
}

// watchFile is the standalone source: it re-reads the config file when its
// size or mtime moves, and on SIGHUP, and hands the document to the same
// pipeline the heartbeat feeds. It is started in every mode so SIGHUP has a
// reader -- unhandled, the signal's default action kills the process, and
// "SIGHUP reloads the config" must not be true of one deployment shape and
// fatal in the other. Under a plane it owns nothing: the heartbeat does, and
// this goroutine only says so.
func (r *reloader) watchFile(ctx context.Context, log *slog.Logger) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	// A nil channel never fires, which is how a plane-owned process or one
	// with no file keeps the loop and drops the ticker.
	var tick <-chan time.Time
	if !r.planeOwned && r.configPath != "" {
		every := r.fileEvery
		if every == 0 {
			every = fileWatchEvery
		}
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			r.pollFile(log)
		case <-hup:
			switch {
			case r.planeOwned:
				log.Info("SIGHUP ignored: the control plane owns the configuration",
					"hint", "edit the configuration in the control plane; the heartbeat applies it")
			case r.configPath == "":
				log.Info("SIGHUP ignored: this process was started without a config file")
			default:
				log.Info("SIGHUP received; re-reading the config file", "path", r.configPath)
				r.filePending = r.reloadFile(log) == reloadRetry
			}
		}
	}
}

// pollFile is one tick: stat the file, read it when the stat moved or the
// last read asked to be retried.
func (r *reloader) pollFile(log *slog.Logger) {
	st, err := os.Stat(r.configPath)
	if err != nil {
		// A ConfigMap swap is atomic and an editor's rename is too, so a
		// missing file is an operator's `rm`, worth one line and no more:
		// the running rules keep serving until the file is back.
		if !r.fileBroken {
			log.Warn("the config file cannot be read; keeping the running rules",
				"path", r.configPath, "error", err)
			r.fileBroken = true
		}
		return
	}
	if !r.filePending && st.Size() == r.fileSize && st.ModTime().Equal(r.fileMod) {
		return
	}
	r.fileSize, r.fileMod = st.Size(), st.ModTime()
	r.filePending = r.reloadFile(log) == reloadRetry
}

// reloadFile reads the config file and runs it through the reload. A file
// that does not load is a retry, not a refusal: the read may have caught an
// editor mid-write, and the next tick re-reads without waiting for the stat
// to move again. It is logged once until a read succeeds.
func (r *reloader) reloadFile(log *slog.Logger) reloadOutcome {
	local, err := r.load(r.configPath)
	if err != nil {
		if !r.fileBroken {
			log.Warn("the config file does not load; keeping the running rules",
				"path", r.configPath, "error", err)
			r.fileBroken = true
		}
		return reloadRetry
	}
	r.fileBroken = false
	if local.LoadFromDisk != nil {
		// The same refusal resolveConfigSource gives the key at startup,
		// phrased for a process that is already running.
		log.Warn(`the config file writes "load_from_disk", which is not a config file key; keeping the running rules`,
			"path", r.configPath,
			"hint", "it is set on the control plane's sidecar configuration; remove it from the file")
		return reloadRefused
	}
	raw, err := json.Marshal(local)
	if err != nil {
		log.Warn("config compare failed; keeping the running rules", "error", err)
		return reloadRetry
	}
	return r.once(log, raw, r.applyFile)
}

// applyFile is the standalone counterpart of apply: the file is the owner,
// so there is no load_from_disk flag and no ownership flip to consider.
func (r *reloader) applyFile(log *slog.Logger, raw []byte) reloadOutcome {
	return r.applyOwned(log, raw, "config file")
}

// ownerLicenseApplies reports whether a license the owner supplies is the
// one this process runs under. A plane's always is. A file's `license` key
// is the lowest local source: while the flag or HOOP_LICENSE holds the
// document in force, editing the key changes nothing, on a reload exactly
// as on a restart. An empty Source is a process running with no license at
// all, which the key may license.
func (r *reloader) ownerLicenseApplies() bool {
	if r.planeOwned {
		return true
	}
	src := r.lic.get().Source
	return src == "" || src == fileLicenseSource
}

// apply decides what a drifted document means for the running process and
// swaps it in when it can. Every refusal runs the same checks startup runs
// (LoadConfigBytes, the caps in buildLanes), so a config startup would
// refuse is refused here with the same message; the heartbeat must never be
// a side door past validation.
//
// The load_from_disk flag is handled here too, hot when the documents
// allow it: a disk-mode answer re-adopts the config file (which also
// rotates the license the tiny document carries), a takeover runs the
// plane's full document through the same swap-or-restart decision as any
// owned drift, and the mode flips only on a document that applied.
func (r *reloader) apply(log *slog.Logger, raw []byte) reloadOutcome {
	var planeDoc Config
	on := json.Unmarshal(raw, &planeDoc) == nil &&
		planeDoc.LoadFromDisk != nil && *planeDoc.LoadFromDisk
	switch {
	case on:
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

// adoptFile serves the disk-mode answer: the config file is the source of
// truth, re-read on every drifted answer -- load_from_disk means the FILE,
// not a startup snapshot of it. The plane's license is stamped into the
// document so applyOwned's rotation verifies and publishes it, and the
// document runs through the same swap-or-restart decision as any owned
// drift. On a handover (the process was plane-owned) the mode flips only
// when the document applied; a file this process cannot hot-adopt stays on
// the restart path, where startup either serves it or refuses it by name.
func (r *reloader) adoptFile(log *slog.Logger, planeLicense string) reloadOutcome {
	if r.configPath == "" {
		log.Warn("the control plane says the configuration loads from disk, " +
			"but this process was started without a config file; restart with one to apply it")
		return reloadRestart
	}
	local, err := r.load(r.configPath)
	if err != nil {
		log.Warn("the control plane says the configuration loads from disk, "+
			"but the file does not load; fix it and restart to apply it",
			"path", r.configPath, "error", err)
		return reloadRestart
	}
	if local.LoadFromDisk != nil {
		log.Warn(`the control plane says the configuration loads from disk, `+
			`but the file writes "load_from_disk", which only the control plane says; `+
			`remove the key and restart to apply it`, "path", r.configPath)
		return reloadRestart
	}
	if len(local.Listeners) == 0 {
		log.Warn("the control plane says the configuration loads from disk, "+
			"but the file declares no listeners; restart to apply it", "path", r.configPath)
		return reloadRestart
	}
	// The plane's license rides beside the tiny document; stamped into the
	// file's license slot, applyOwned's rotation verifies and publishes it
	// exactly like a plane-owned rotation, and a downgrade the file's
	// rules no longer fit stops the relay the same way. The file's own key
	// is not a source under a managing plane.
	local.License = planeLicense
	raw, err := json.Marshal(local)
	if err != nil {
		log.Warn("config compare failed; keeping the running rules", "error", err)
		return reloadRetry
	}
	flipped := !r.diskMode
	out := r.applyOwned(log, raw, "config file")
	if out == reloadApplied && flipped {
		r.diskMode = true
		log.Info("the control plane handed the configuration to the local file; "+
			"the file's document now serves", "path", r.configPath)
	}
	return out
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

	// The license the lanes are built against: the one in use, unless the
	// owner supplied a different document. UseLicense verifies the
	// signature here the same way startup does, so a plane cannot hand
	// this process a verdict, only a document.
	newCfg.lic = r.lic.get()
	licRotated := newCfg.License != r.licRaw
	if licRotated && !r.ownerLicenseApplies() {
		// Said once: the compare moves on so a later rule edit does not
		// repeat it, and nothing else about the running license changes.
		r.licRaw = newCfg.License
		licRotated = false
		log.Warn("the "+from+"'s license key changed, but "+newCfg.lic.Source+
			" outranks it; keeping the license in use",
			"hint", "a restart would read the same precedence")
	}
	if licRotated {
		if err := newCfg.UseLicense(license.Ref{Value: newCfg.License, Source: r.licSource}); err != nil {
			// Kept, not refused: a license nobody can read must not
			// also freeze the rules. The caps stay where they were.
			log.Warn("the "+from+" sent a license this build refuses; keeping the license in use",
				"error", err)
			licRotated = false
		}
	}
	// ac, not r.ac: the staged detector this reload built. A refusal below
	// must leave the running dependencies untouched.
	lanes, err := buildLanes(newCfg, det, ac)
	if err != nil {
		if licRotated {
			// The plane narrowed or removed the license and the running
			// rules no longer fit under it. Keeping the old one would let
			// a downgraded organization serve paid rules until the term
			// ended or somebody restarted the process -- and `handle`
			// records this document as handled, so no later heartbeat
			// would revisit it.
			//
			// So the new license is published and the relay is stopped,
			// the same controlled drain the expiry path runs: Run exits,
			// the supervisor restarts, and buildLanes then refuses the
			// config by name until somebody renews it or removes rules.
			// Rules are never dropped from a live proxy to fit a smaller
			// license; that widens what the proxy allows.
			r.licRaw = newCfg.License
			r.lic.set(newCfg.lic)
			r.lic.overCap.Store(true)
			log.Warn("the "+from+"'s license no longer covers the running rules; "+
				"stopping the relay so it restarts under the new one",
				"license", newCfg.lic.Line(), "error", err)
			return reloadRefused
		}
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
		// An endpoint lane closes over its evaluator when the server is
		// built, so its rules cannot be swapped in place. Restarting is the
		// honest answer: applying the swap to the view alone would show an
		// operator rules that are not the rules being enforced.
		if isEndpointLane(ln.cfg) && !bytes.Equal(doc, r.laneDocs[ln.name]) {
			log.Warn("endpoint lane rules changed on the "+from+"; restart to apply them",
				"listener", ln.name, "protocol", ln.cfg.Protocol)
			return reloadRestart
		}
	}

	swapped, kept := 0, 0
	viewLanes := make([]lane, 0, len(lanes))
	for _, ln := range lanes {
		doc := docs[ln.name]
		if isEndpointLane(ln.cfg) {
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
		// The outgoing generation's analyzers stop being read after this
		// swap; bank what they did since the last usage event first.
		r.tel.retireAnalyzers(r.prevLanes[ln.name].analyzers)
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
	if licRotated {
		r.licRaw = newCfg.License
		r.lic.set(newCfg.lic)
		log.Info(newCfg.lic.Line())
	}
	// Published on every applied generation, not only on a rotation: rules
	// added by this reload can push a config past the free tier, and the
	// watchdog only stops a process that would lose something.
	r.lic.depends.Store(newCfg.dependsOnLicense())
	// Which sections moved, for the applied event: compared per lane
	// against the sections the previous generation resolved, then the
	// facts that travel outside the lanes.
	changed := make(map[string]bool, 5)
	sections := make(map[string]map[string][]byte, len(lanes))
	for _, ln := range lanes {
		next, serr := laneSectionDocs(newCfg, ln.cfg)
		if serr != nil {
			// The document already applied; a report that cannot say
			// which section moved says none rather than failing the swap.
			sections = r.sections
			break
		}
		sections[ln.name] = next
		for name, doc := range next {
			if !bytes.Equal(doc, r.sections[ln.name][name]) {
				changed[name] = true
			}
		}
	}
	if detChanged {
		changed["pii"] = true
	}
	if licRotated {
		changed["license"] = true
	}
	r.sections = sections
	r.gen++
	r.view.Store(&laneState{lanes: viewLanes, gen: r.gen})
	log.Info(from+" configuration applied",
		"generation", r.gen, "swapped", swapped, "kept", kept)
	r.tel.trackReload(reloadReport{
		outcome: reloadApplied, gen: r.gen, swapped: swapped, kept: kept,
		changed: sortedKeys(changed), cfg: newCfg, lanes: viewLanes, det: det,
	})
	return reloadApplied
}

// nonRuleDoc renders everything a hot swap cannot change: the config with
// every rule-bearing section removed. Two configs with equal documents
// differ in rules alone.
//
// A JSON render rather than a field-by-field compare, so a new Config field
// is restart-guarded by default; forgetting it here fails safe.
// BaselineDoc exposes nonRuleDoc to the control plane, which composes rules
// into a sidecar's document before serving it and must be able to PROVE the
// composition never reaches the baseline -- a rule edit that did would turn
// every rule edit into a fleet restart.
//
// Exported for the same reason as CheckLimits: the authority on what this
// build refuses, and on what it can hot-swap, is this build. A copy of the
// list on the gateway side would pass its own test and still be wrong the
// first time a Config field is added here.
func BaselineDoc(c *Config) ([]byte, error) { return nonRuleDoc(c) }

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

// laneSectionDocs renders the same resolved stack as laneRuleDoc, one
// document per section, so a reload can say WHICH section moved. Keys are
// the fixed vocabulary the config-applied event reports.
func laneSectionDocs(c *Config, lc ListenerConfig) (map[string][]byte, error) {
	gc, opa, mc := c.resolve(lc)
	out := make(map[string][]byte, 4)
	for name, v := range map[string]any{
		"guardrails": gc, "opa": opa, "mask": mc, "analyzer": lc.Analyzer,
	} {
		doc, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out[name] = doc
	}
	return out, nil
}
