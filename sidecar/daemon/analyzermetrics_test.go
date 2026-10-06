package daemon

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// The exposition is what Prometheus parses: buckets cumulative and ending in
// +Inf equal to _count, label values escaped, retries and statements counted.
// A wrong bucket count here is a wrong p99 on every dashboard.
func TestAnalyzerMetricsExposition(t *testing.T) {
	m := newAnalyzerMetrics("vertex", "gemini-3.8-flash")
	m.call(analyzer.Call{Rule: "k8sapi", Outcome: analyzer.CallOK, Duration: 300 * time.Millisecond, Attempts: 1})
	m.call(analyzer.Call{Rule: "k8sapi", Outcome: analyzer.CallOK, Duration: 2 * time.Second, Attempts: 3})
	m.call(analyzer.Call{Rule: "k8sapi", Outcome: analyzer.CallTimeout, Duration: 90 * time.Second, Attempts: 1,
		Err: errors.New("deadline")})
	m.call(analyzer.Call{Rule: `we"ird`, Outcome: analyzer.CallOK, Duration: time.Second, Attempts: 1})
	m.outcome(analyzer.Outcome{Rule: "k8sapi", Status: analyzer.StatusCached, RiskLevel: analyzer.RiskLow, Action: analyzer.ActionAllow})
	m.outcome(analyzer.Outcome{Rule: "k8sapi", Status: analyzer.StatusCached, RiskLevel: analyzer.RiskLow, Action: analyzer.ActionAllow})

	var b strings.Builder
	if err := m.writeTo(&b); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	const ok = `analyzer="k8sapi",provider="vertex",model="gemini-3.8-flash",outcome="ok"`
	const timeout = `analyzer="k8sapi",provider="vertex",model="gemini-3.8-flash",outcome="timeout"`
	for _, line := range []string{
		"# TYPE hoop_inspect_analyzer_request_duration_seconds histogram",
		`hoop_inspect_analyzer_request_duration_seconds_bucket{` + ok + `,le="0.25"} 0`,
		`hoop_inspect_analyzer_request_duration_seconds_bucket{` + ok + `,le="0.5"} 1`,
		`hoop_inspect_analyzer_request_duration_seconds_bucket{` + ok + `,le="2"} 2`,
		`hoop_inspect_analyzer_request_duration_seconds_bucket{` + ok + `,le="60"} 2`,
		`hoop_inspect_analyzer_request_duration_seconds_bucket{` + ok + `,le="+Inf"} 2`,
		`hoop_inspect_analyzer_request_duration_seconds_sum{` + ok + `} 2.3`,
		`hoop_inspect_analyzer_request_duration_seconds_count{` + ok + `} 2`,
		// Past the last bound: only +Inf holds it.
		`hoop_inspect_analyzer_request_duration_seconds_bucket{` + timeout + `,le="60"} 0`,
		`hoop_inspect_analyzer_request_duration_seconds_bucket{` + timeout + `,le="+Inf"} 1`,
		`analyzer="we\"ird"`,
		`hoop_inspect_analyzer_retries_total{analyzer="k8sapi",provider="vertex",model="gemini-3.8-flash"} 2`,
		`hoop_inspect_analyzer_statements_total{analyzer="k8sapi",status="cached",risk_level="low",action="allow"} 2`,
	} {
		if !strings.Contains(out, line) {
			t.Errorf("missing %s\n---\n%s", line, out)
		}
	}
}

// A process with no analyzer answers an empty document, which Prometheus
// accepts, rather than failing the scrape.
func TestNoAnalyzerWritesNothing(t *testing.T) {
	var m *analyzerMetrics
	var b strings.Builder
	if err := m.writeTo(&b); err != nil || b.Len() != 0 {
		t.Errorf("nil metrics wrote %q, err %v", b.String(), err)
	}
}

// Observers run inline on data connections. A scraper that stops reading
// holds its write open, and that must not hold the lock the next call
// reports under.
func TestAStalledScrapeDoesNotBlockObservers(t *testing.T) {
	m := newAnalyzerMetrics("p", "m")
	m.call(analyzer.Call{Rule: "lane", Outcome: analyzer.CallOK, Duration: time.Second, Attempts: 1})

	r, w := io.Pipe() // nothing reads r, so the write blocks
	defer r.Close()
	go func() { _ = m.writeTo(w) }()
	time.Sleep(20 * time.Millisecond) // let writeTo reach the write

	done := make(chan struct{})
	go func() {
		m.call(analyzer.Call{Rule: "lane", Outcome: analyzer.CallOK, Duration: time.Second, Attempts: 1})
		m.outcome(analyzer.Outcome{Rule: "lane", Status: analyzer.StatusOK})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("an observer waited on a scrape the client stopped reading")
	}
}
