package daemon

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// analyzerMetrics counts what the analyzer does, for GET /metrics in the
// Prometheus text format.
//
// Hand-written against the exposition format rather than built on
// client_golang, because the root module takes no dependency but libhoop.
// Three series families need about a hundred lines; the library would bring
// a dependency tree for them.
//
// One per process, held by analyzerDeps, so it outlives hot reloads: a
// reload rebuilds the evaluators, and counters kept on them would reset,
// which a scraper reads as a process restart. Provider and model are fixed
// for the process, since the analyzer section is restart-guarded.
type analyzerMetrics struct {
	provider, model string

	mu        sync.Mutex
	durations map[durationKey]*histogram
	retries   map[string]uint64
	outcomes  map[outcomeKey]uint64
}

type durationKey struct {
	rule    string
	outcome analyzer.CallOutcome
}

type outcomeKey struct {
	rule, status, level, action string
}

// durationBuckets are upper bounds in seconds. Dense below 10s, where the
// default timeout sits, and reaching 60s, past any timeout worth setting.
var durationBuckets = []float64{0.25, 0.5, 1, 2, 3, 5, 7.5, 10, 15, 20, 30, 45, 60}

// histogram is one series: counts per bucket, not cumulative, plus the sum.
type histogram struct {
	counts []uint64 // len(durationBuckets)+1; the last is +Inf
	sum    float64
	count  uint64
}

func newAnalyzerMetrics(provider, model string) *analyzerMetrics {
	return &analyzerMetrics{
		provider:  provider,
		model:     model,
		durations: map[durationKey]*histogram{},
		retries:   map[string]uint64{},
		outcomes:  map[outcomeKey]uint64{},
	}
}

// call records one classification's provider call.
func (m *analyzerMetrics) call(c analyzer.Call) {
	secs := c.Duration.Seconds()
	i, _ := slices.BinarySearch(durationBuckets, secs)
	m.mu.Lock()
	defer m.mu.Unlock()
	k := durationKey{c.Rule, c.Outcome}
	h := m.durations[k]
	if h == nil {
		h = &histogram{counts: make([]uint64, len(durationBuckets)+1)}
		m.durations[k] = h
	}
	h.counts[i]++
	h.sum += secs
	h.count++
	if c.Attempts > 1 {
		m.retries[c.Rule] += uint64(c.Attempts - 1)
	}
}

// outcome records what happened to one eligible statement.
func (m *analyzerMetrics) outcome(o analyzer.Outcome) {
	m.mu.Lock()
	m.outcomes[outcomeKey{o.Rule, o.Status, string(o.RiskLevel), string(o.Action)}]++
	m.mu.Unlock()
}

// writeTo renders every family and writes it to w. A nil receiver writes
// nothing, which is a valid exposition: the process has no analyzer.
//
// The lock is released before the write. Observers run inline on data
// connections, and a scraper that stops reading must not stall them.
func (m *analyzerMetrics) writeTo(w io.Writer) error {
	if m == nil {
		return nil
	}
	_, err := io.WriteString(w, m.render())
	return err
}

// render formats every family under the lock, into memory only. Series are
// sorted so two scrapes of the same state are byte-identical.
func (m *analyzerMetrics) render() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder

	const dur = "hoop_inspect_analyzer_request_duration_seconds"
	b.WriteString("# HELP " + dur + " Time one classification spent with the provider, retries included. A timeout reports about timeout_sec.\n")
	b.WriteString("# TYPE " + dur + " histogram\n")
	dkeys := make([]durationKey, 0, len(m.durations))
	for k := range m.durations {
		dkeys = append(dkeys, k)
	}
	slices.SortFunc(dkeys, func(a, b durationKey) int {
		return strings.Compare(a.rule+"\x00"+string(a.outcome), b.rule+"\x00"+string(b.outcome))
	})
	for _, k := range dkeys {
		h := m.durations[k]
		base := labels("analyzer", k.rule, "provider", m.provider, "model", m.model, "outcome", string(k.outcome))
		var cum uint64
		for i, le := range durationBuckets {
			cum += h.counts[i]
			fmt.Fprintf(&b, "%s_bucket{%s,le=%q} %d\n", dur, base, strconv.FormatFloat(le, 'f', -1, 64), cum)
		}
		fmt.Fprintf(&b, "%s_bucket{%s,le=\"+Inf\"} %d\n", dur, base, h.count)
		fmt.Fprintf(&b, "%s_sum{%s} %s\n", dur, base, strconv.FormatFloat(h.sum, 'g', -1, 64))
		fmt.Fprintf(&b, "%s_count{%s} %d\n", dur, base, h.count)
	}

	const ret = "hoop_inspect_analyzer_retries_total"
	b.WriteString("# HELP " + ret + " Classifications re-sent after a 408, 429 or 5xx answer.\n")
	b.WriteString("# TYPE " + ret + " counter\n")
	rkeys := make([]string, 0, len(m.retries))
	for k := range m.retries {
		rkeys = append(rkeys, k)
	}
	slices.Sort(rkeys)
	for _, k := range rkeys {
		fmt.Fprintf(&b, "%s{%s} %d\n", ret,
			labels("analyzer", k, "provider", m.provider, "model", m.model), m.retries[k])
	}

	const st = "hoop_inspect_analyzer_statements_total"
	b.WriteString("# HELP " + st + " Statements the analyzer was eligible to classify, by ai_status, risk level and action.\n")
	b.WriteString("# TYPE " + st + " counter\n")
	okeys := make([]outcomeKey, 0, len(m.outcomes))
	for k := range m.outcomes {
		okeys = append(okeys, k)
	}
	slices.SortFunc(okeys, func(a, b outcomeKey) int {
		return strings.Compare(
			a.rule+"\x00"+a.status+"\x00"+a.level+"\x00"+a.action,
			b.rule+"\x00"+b.status+"\x00"+b.level+"\x00"+b.action)
	})
	for _, k := range okeys {
		fmt.Fprintf(&b, "%s{%s} %d\n", st,
			labels("analyzer", k.rule, "status", k.status, "risk_level", k.level, "action", k.action), m.outcomes[k])
	}

	return b.String()
}

// labels renders name="value" pairs, escaped as the exposition format
// requires: backslash, double quote and newline.
func labels(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(labelEscaper.Replace(kv[i+1]))
		b.WriteByte('"')
	}
	return b.String()
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// callObserver is the OnCall hook: the histogram, and a debug line per call.
// Nil when there is nothing to report into, which is every build that never
// serves (-validate, tests).
func (ac *analyzerDeps) callObserver() func(analyzer.Call) {
	if ac.metrics == nil && ac.log == nil {
		return nil
	}
	return func(c analyzer.Call) {
		if ac.metrics != nil {
			ac.metrics.call(c)
		}
		if ac.log != nil {
			ac.log.Debug("analyzer call",
				"analyzer", c.Rule,
				"outcome", string(c.Outcome),
				"duration_ms", c.Duration.Milliseconds(),
				"timeout_ms", c.Timeout.Milliseconds(),
				"attempts", c.Attempts)
		}
	}
}

// outcomeObserver is the OnOutcome hook. Nil without metrics.
func (ac *analyzerDeps) outcomeObserver() func(analyzer.Outcome) {
	if ac.metrics == nil {
		return nil
	}
	return ac.metrics.outcome
}

// metricsContentType is the Prometheus text exposition format, version 0.0.4.
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"
