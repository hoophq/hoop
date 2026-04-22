// Package bootstrap renders the gateway's startup sequence as phased,
// timed progress output. In TTY mode it prints a grouped, colored view
// to stdout; in non-TTY mode it emits zap JSON records via common/log
// so log aggregators keep working unchanged.
//
// The renderer is chosen once on first use by inspecting LOG_ENCODING
// and, when unset, whether stdout is a terminal.
package bootstrap

import (
	"sync"
	"time"
)

var (
	initOnce     sync.Once
	chosen       renderer
	processStart time.Time
	startMu      sync.Mutex
	phaseMu      sync.Mutex
	currentPhase string
	agentOnce    sync.Once
)

func active() renderer {
	initOnce.Do(func() {
		if shouldUseTTY() {
			chosen = newTTYRenderer()
		} else {
			chosen = newJSONRenderer()
		}
	})
	return chosen
}

// Start records the process start time, used later by Ready for the
// "Ready in N.Ns" summary. Safe to call more than once; only the first
// call takes effect.
func Start() {
	startMu.Lock()
	defer startMu.Unlock()
	if processStart.IsZero() {
		processStart = time.Now()
	}
}

// Header prints the top-of-boot version banner.
func Header(version, platform, commit string) {
	Start()
	active().header(version, platform, commit)
}

// Phase prints a new phase header and sets it as the current phase for
// subsequent Step calls.
func Phase(name string) {
	phaseMu.Lock()
	currentPhase = name
	phaseMu.Unlock()
	active().phase(name)
}

// StepHandle tracks an in-flight step. Callers must invoke exactly one of
// OK / Skip / Fail; repeated finalizers are ignored.
type StepHandle struct {
	name  string
	phase string
	start time.Time
	mu    sync.Mutex
	done  bool
}

// Step begins a new step under the current phase. The step's elapsed time
// is measured from this call until OK/Skip/Fail is invoked.
func Step(name string) *StepHandle {
	phaseMu.Lock()
	p := currentPhase
	phaseMu.Unlock()
	return &StepHandle{name: name, phase: p, start: time.Now()}
}

// OK marks the step as complete. detail is optional context (e.g. ":8010").
func (h *StepHandle) OK(detail string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return
	}
	h.done = true
	active().stepOK(h.name, time.Since(h.start), detail)
}

// Skip marks the step as intentionally skipped (e.g. feature disabled).
func (h *StepHandle) Skip(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return
	}
	h.done = true
	active().stepSkip(h.name, reason)
}

// Fail marks the step as failed. The caller typically follows this with
// log.Fatal or a returned error.
func (h *StepHandle) Fail(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return
	}
	h.done = true
	active().stepFail(h.name, time.Since(h.start), err)
}

// Ready prints the "Ready in N.Ns" summary plus a URL table. Elapsed is
// computed from the Start() call; if Start was never called, elapsed is 0.
func Ready(urls map[string]string) {
	startMu.Lock()
	s := processStart
	startMu.Unlock()
	var elapsed time.Duration
	if !s.IsZero() {
		elapsed = time.Since(s)
	}
	active().ready(elapsed, urls)
}

// AgentConnected announces an agent handshake. Guarded by sync.Once so
// reconnects don't reprint the Agents phase header.
func AgentConnected(name, mode string) {
	agentOnce.Do(func() {
		Phase("Agents")
		active().agentConnected(name, mode)
	})
}
