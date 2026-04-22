package bootstrap

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"go.uber.org/zap"
)

// jsonLogger hands back a sugared logger whose caller annotation skips past
// the renderer + public API frames so JSON records report the gateway call
// site (e.g. gateway/main.go:NN) instead of renderer.go.
func jsonLogger() *zap.SugaredLogger {
	return log.GetLogger().WithOptions(zap.AddCallerSkip(2)).Sugar()
}

type renderer interface {
	header(version, platform, commit string)
	phase(name string)
	stepOK(name string, elapsed time.Duration, detail string)
	stepSkip(name string, reason string)
	stepFail(name string, elapsed time.Duration, err error)
	ready(total time.Duration, urls map[string]string)
	agentConnected(name, mode string)
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiGray   = "\x1b[90m"

	stepNameWidth = 28
)

type ttyRenderer struct {
	mu       sync.Mutex
	w        io.Writer
	useColor bool
}

func newTTYRenderer() *ttyRenderer {
	return &ttyRenderer{w: os.Stdout, useColor: !noColor()}
}

func (r *ttyRenderer) paint(code, s string) string {
	if !r.useColor {
		return s
	}
	return code + s + ansiReset
}

func (r *ttyRenderer) glyph(unicode, ascii string) string {
	if noColor() {
		return ascii
	}
	return unicode
}

func (r *ttyRenderer) header(version, platform, commit string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	triangle := r.paint(ansiCyan+ansiBold, r.glyph("▲", ">"))
	name := r.paint(ansiBold, "hoop")
	dot := r.paint(ansiGray, "·")
	fmt.Fprintln(r.w)
	fmt.Fprintf(r.w, "  %s %s  gateway v%s %s %s %s commit %s\n",
		triangle, name, version, dot, platform, dot, short)
}

func (r *ttyRenderer) phase(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	bullet := r.paint(ansiCyan, r.glyph("◐", "*"))
	fmt.Fprintln(r.w)
	fmt.Fprintf(r.w, "  %s %s\n", bullet, r.paint(ansiBold, name))
}

func (r *ttyRenderer) stepOK(name string, elapsed time.Duration, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	check := r.paint(ansiGreen, r.glyph("✓", "[ok]"))
	// Suppress elapsed column for near-instant steps (server starts, config
	// reads) so the detail column carries the useful signal.
	if elapsed < 10*time.Millisecond {
		fmt.Fprintf(r.w, "    %s %-*s %s\n",
			check, stepNameWidth, name, detail)
		return
	}
	fmt.Fprintf(r.w, "    %s %-*s %s  %s\n",
		check, stepNameWidth, name,
		r.paint(ansiGray, formatElapsed(elapsed)),
		detail)
}

func (r *ttyRenderer) stepSkip(name, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mark := r.paint(ansiGray, r.glyph("∘", "[-]"))
	fmt.Fprintf(r.w, "    %s %-*s %s  %s\n",
		mark, stepNameWidth, name,
		r.paint(ansiGray, "skipped"),
		r.paint(ansiGray, reason))
}

func (r *ttyRenderer) stepFail(name string, elapsed time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cross := r.paint(ansiRed, r.glyph("✗", "[x]"))
	fmt.Fprintf(r.w, "    %s %-*s %s  %s\n",
		cross, stepNameWidth, name,
		r.paint(ansiGray, formatElapsed(elapsed)),
		r.paint(ansiRed, err.Error()))
}

func (r *ttyRenderer) ready(total time.Duration, urls map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintln(r.w)
	summary := fmt.Sprintf("Ready in %s", formatElapsed(total))
	fmt.Fprintf(r.w, "  %s %s\n", r.paint(ansiGreen+ansiBold, r.glyph("✓", "[ok]")), r.paint(ansiBold, summary))
	if len(urls) == 0 {
		return
	}
	fmt.Fprintln(r.w)
	for _, k := range sortedKeys(urls) {
		arrow := r.paint(ansiGray, "→")
		fmt.Fprintf(r.w, "    %-12s %s %s\n", k, arrow, urls[k])
	}
}

func (r *ttyRenderer) agentConnected(name, mode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	check := r.paint(ansiGreen, r.glyph("✓", "[ok]"))
	detail := "connected"
	if mode != "" {
		detail = fmt.Sprintf("connected · %s mode", mode)
	}
	fmt.Fprintf(r.w, "    %s %-*s %s\n", check, stepNameWidth, name, detail)
}

func formatElapsed(d time.Duration) string {
	if d < time.Millisecond {
		return "0ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	secs := float64(d) / float64(time.Second)
	return fmt.Sprintf("%.1fs", secs)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type jsonRenderer struct{}

func newJSONRenderer() *jsonRenderer { return &jsonRenderer{} }

func (jsonRenderer) header(version, platform, commit string) {
	jsonLogger().With(
		"event", "bootstrap.header",
		"version", version,
		"platform", platform,
		"commit", commit,
	).Infof("gateway starting, version=%s, platform=%s, commit=%s", version, platform, commit)
}

func (jsonRenderer) phase(name string) {
	jsonLogger().With("event", "bootstrap.phase", "phase", name).Infof("phase: %s", name)
}

func (jsonRenderer) stepOK(name string, elapsed time.Duration, detail string) {
	jsonLogger().With(
		"event", "bootstrap.step",
		"step", name,
		"status", "ok",
		"elapsed_ms", elapsed.Milliseconds(),
		"detail", detail,
	).Infof("step complete: %s", strings.TrimSpace(name+" "+detail))
}

func (jsonRenderer) stepSkip(name, reason string) {
	jsonLogger().With(
		"event", "bootstrap.step",
		"step", name,
		"status", "skipped",
		"reason", reason,
	).Infof("step skipped: %s (%s)", name, reason)
}

func (jsonRenderer) stepFail(name string, elapsed time.Duration, err error) {
	jsonLogger().With(
		"event", "bootstrap.step",
		"step", name,
		"status", "failed",
		"elapsed_ms", elapsed.Milliseconds(),
		"error", err.Error(),
	).Errorf("step failed: %s, err=%v", name, err)
}

func (jsonRenderer) ready(total time.Duration, urls map[string]string) {
	jsonLogger().With(
		"event", "bootstrap.ready",
		"elapsed_ms", total.Milliseconds(),
		"urls", urls,
	).Infof("gateway ready, elapsed=%s", formatElapsed(total))
}

func (jsonRenderer) agentConnected(name, mode string) {
	jsonLogger().With(
		"event", "bootstrap.agent_connected",
		"agent", name,
		"mode", mode,
	).Infof("agent connected: name=%s, mode=%s", name, mode)
}
