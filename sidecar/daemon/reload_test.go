package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// reloadBase is the running config every reload test starts from: one
// postgres lane with one guardrail rule, the shape the gateway emits.
const reloadBase = `{
  "listeners": [{
    "name": "appdb", "protocol": "postgres",
    "listen": "127.0.0.1:0", "upstream": "h:5432",
    "guardrails": {"mode": "enforce", "rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]}
    ]}
  }],
  "audit": {"file": "-"},
  "log_level": "info"
}`

// editJSON applies a crude replacement so each case states only its drift.
func editJSON(t *testing.T, doc, old, new string) string {
	t.Helper()
	if !strings.Contains(doc, old) {
		t.Fatalf("test bug: %q not in the base document", old)
	}
	return strings.Replace(doc, old, new, 1)
}

// testReloader builds a reloader over a running-shaped config, with a real
// (unserved) proxy.Server per relay lane so a swap has a target. It returns
// the log buffer; startupLanes are reachable through rl.prevLanes.
func testReloader(t *testing.T, raw string) (*reloader, *bytes.Buffer) {
	t.Helper()
	cfg, err := LoadConfigBytes([]byte(raw))
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	// Mirror what resolveConfigSource does: a license in the document is the
	// plane's, adopted and moved onto the connection, so the reloader starts
	// from the same state a running process would.
	if cfg.License != "" {
		if lerr := cfg.UseLicense(license.Ref{Value: cfg.License, Source: PlaneLicenseSource}); lerr != nil {
			t.Fatalf("UseLicense: %v", lerr)
		}
	}
	cfg.cp = &controlPlane{url: "http://plane", token: "hsc_x", lastRaw: []byte(raw),
		license: cfg.License, licenseManaged: true}

	lanes, err := buildLanes(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	servers := map[string]*proxy.Server{}
	for _, ln := range lanes {
		if isGRPCTransport(ln.cfg) {
			continue
		}
		srv, serr := buildServer(ln, cfg.Audit, nil, slog.Default())
		if serr != nil {
			t.Fatalf("buildServer: %v", serr)
		}
		servers[ln.name] = srv
	}
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, servers, nil, nil, view, newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatalf("newReloader: %v", err)
	}
	var buf bytes.Buffer
	return rl, &buf
}

func applyWith(rl *reloader, buf *bytes.Buffer, raw string) reloadOutcome {
	log := slog.New(slog.NewTextHandler(buf, nil))
	return rl.apply(log, []byte(raw))
}

func handleWith(rl *reloader, buf *bytes.Buffer, raw string) reloadOutcome {
	log := slog.New(slog.NewTextHandler(buf, nil))
	return rl.handle(log, []byte(raw))
}

func TestARuleOnlyDriftIsApplied(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	if got := applyWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if !strings.Contains(buf.String(), "configuration applied") {
		t.Errorf("no applied log line:\n%s", buf)
	}
	if rl.gen != 1 {
		t.Errorf("generation = %d, want 1", rl.gen)
	}
}

// The generation advances per applied reload, so an operator can match a log
// line to the config edit that produced it.
func TestEachAppliedReloadAdvancesTheGeneration(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	first := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	second := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["delete from"]`)
	applyWith(rl, buf, first)
	applyWith(rl, buf, second)
	if rl.gen != 2 {
		t.Fatalf("generation = %d, want 2", rl.gen)
	}
}

func TestATopologyDriftKeepsTheRestartPath(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase, `"upstream": "h:5432"`, `"upstream": "other:5432"`)
	if got := applyWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(buf.String(), "restart to apply it") {
		t.Errorf("no restart log line:\n%s", buf)
	}
}

// An added listener is topology, not rules: sockets would have to bind.
func TestAnAddedListenerKeepsTheRestartPath(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase, `"listeners": [{`,
		`"listeners": [{"name": "second", "protocol": "postgres", "listen": "127.0.0.1:1", "upstream": "h2:5432"}, {`)
	if got := applyWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
}

// A document startup would refuse is refused on reload with the running
// rules kept: the heartbeat must never be a side door past validation.
func TestABrokenDocumentIsRefused(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	if got := applyWith(rl, buf, `{"listeners": [{"protocol": "nope"}]}`); got != reloadRefused {
		t.Fatalf("outcome = %v, want refused; log:\n%s", got, buf)
	}
}

// The caps bind on reload exactly as at startup: this process runs the free
// tier, and the plane sending two guardrail rules must not lift it.
func TestAnOverCapReloadIsRefused(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase,
		`{"name": "r0", "type": "deny_words_list", "words": ["drop table"]}`,
		`{"name": "r0", "type": "deny_words_list", "words": ["drop table"]},
		 {"name": "r1", "type": "deny_words_list", "words": ["truncate"]}`)
	if got := applyWith(rl, buf, drifted); got != reloadRefused {
		t.Fatalf("outcome = %v, want refused; log:\n%s", got, buf)
	}
	if !strings.Contains(buf.String(), "keeping the running rules") {
		t.Errorf("the refusal does not say the running rules survive:\n%s", buf)
	}
}

// Without a retained detector builder, a pii drift cannot be swapped: the
// running detector was built from the old section.
func TestAPIIDriftWithoutABuilderKeepsTheRestartPath(t *testing.T) {
	base := editJSON(t, reloadBase, `"log_level": "info"`,
		`"log_level": "info", "pii": {"entities": ["EMAIL_ADDRESS"]}`)
	// A pii section needs a detector at Run time, but the reloader only
	// compares bytes when no builder exists; det stays nil in this harness.
	rl, buf := testReloader(t, base)

	drifted := editJSON(t, base, `["EMAIL_ADDRESS"]`, `["CREDIT_CARD"]`)
	if got := applyWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !strings.Contains(buf.String(), "no detector builder") {
		t.Errorf("the log does not name the missing builder:\n%s", buf)
	}
}

// A reload that changes nothing but rules back and forth still applies: the
// swap is idempotent and cheap, and refusing a revert would strand a fleet
// on an edit the operator undid.
func TestARevertToTheBaselineRulesStillApplies(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)

	drifted := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	applyWith(rl, buf, drifted)
	if got := applyWith(rl, buf, reloadBase); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
}

func TestNonRuleDocIgnoresEveryRuleSection(t *testing.T) {
	a, err := LoadConfigBytes([]byte(reloadBase))
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadConfigBytes([]byte(editJSON(t, reloadBase,
		`"words": ["drop table"]`, `"words": ["truncate"]`)))
	if err != nil {
		t.Fatal(err)
	}
	docA, errA := nonRuleDoc(a)
	docB, errB := nonRuleDoc(b)
	if errA != nil || errB != nil {
		t.Fatalf("nonRuleDoc: %v, %v", errA, errB)
	}
	if !bytes.Equal(docA, docB) {
		t.Fatalf("rule drift leaked into the non-rule document:\n%s\n%s", docA, docB)
	}
}

// A two-lane config where only one lane's rules drift: the untouched lane
// keeps its RUNNING evaluator instances, so analyzer call budgets, verdict
// caches and counters survive reloads that never edited it. The free tier
// allows one guardrail rule per process, so lane A carries it and lane B
// consults OPA, which gives B a non-nil evaluator without touching a cap.
const twoLaneBase = `{
  "listeners": [
    {"name": "appdb", "protocol": "postgres",
     "listen": "127.0.0.1:0", "upstream": "h:5432",
     "guardrails": {"mode": "enforce", "rules": [
       {"name": "r0", "type": "deny_words_list", "words": ["drop table"]}
     ]}},
    {"name": "webdb", "protocol": "postgres",
     "listen": "127.0.0.1:1", "upstream": "h2:5432",
     "opa": {"url": "http://opa.local/v1/data/hoop/allow"}}
  ],
  "audit": {"file": "-"},
  "log_level": "info"
}`

func TestAnUntouchedLaneKeepsItsRunningEvaluators(t *testing.T) {
	rl, buf := testReloader(t, twoLaneBase)
	before, ok := rl.prevLanes["webdb"]
	if !ok {
		t.Fatal("test bug: no webdb lane")
	}
	if before.policy == nil {
		t.Fatal("test bug: webdb built no evaluator, identity would compare nil to nil")
	}

	drifted := editJSON(t, twoLaneBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	if got := handleWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}

	st := rl.view.Load()
	if st.gen != 1 {
		t.Fatalf("published generation = %d, want 1", st.gen)
	}
	var after *lane
	for i := range st.lanes {
		if st.lanes[i].name == "webdb" {
			after = &st.lanes[i]
		}
	}
	if after == nil {
		t.Fatal("webdb missing from the published generation")
	}
	// Instance identity, not equality: a policy.Chain is a slice, so the
	// underlying data pointer is what proves the running evaluator (and
	// its budgets and counters) kept serving.
	if reflect.ValueOf(after.policy).Pointer() != reflect.ValueOf(before.policy).Pointer() {
		t.Error("webdb's evaluator was rebuilt by a drift that never touched it")
	}
	if !strings.Contains(buf.String(), "kept=1") {
		t.Errorf("the applied line does not count the kept lane:\n%s", buf)
	}
}

// withLicense renders the base document carrying a license, the way the
// gateway serves the organization's.
func withLicense(t *testing.T, doc, licenseDoc string) string {
	t.Helper()
	quoted, err := json.Marshal(licenseDoc)
	if err != nil {
		t.Fatal(err)
	}
	return editJSON(t, doc, `"log_level": "info"`, `"log_level": "info", "license": `+string(quoted))
}

// An admin renews the license in the control plane. The sidecar adopts it on
// the next heartbeat: no restart, and the lanes keep serving. This is the case
// the feature exists for -- a fleet whose license is edited in one place.
func TestARotatedLicenseIsAdoptedWithoutARestart(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	doc := licensetest.Document(t, licensetest.Enterprise())

	if got := applyWith(rl, buf, withLicense(t, reloadBase, doc)); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if got := rl.lic.get().State(); got != license.StateValid {
		t.Fatalf("license state = %q, want valid; log:\n%s", got, buf)
	}
	if got := rl.lic.get().Source; got != PlaneLicenseSource {
		t.Errorf("license source = %q, want %q", got, PlaneLicenseSource)
	}
}

// A license the trust root refuses must not also freeze the rules: the
// running license stays and the rest of the document is applied.
func TestARefusedLicenseKeepsTheOneInUse(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	// Signed, then tampered: a real forgery rather than malformed JSON.
	// The payload no longer matches the signature over it.
	signed := licensetest.Document(t, licensetest.Enterprise())
	forged := strings.Replace(signed, "Acme Corp", "Forged Corp", 1)
	if forged == signed {
		t.Fatal("test bug: the document was not tampered with")
	}

	if got := applyWith(rl, buf, withLicense(t, reloadBase, forged)); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if got := rl.lic.get().State(); got == license.StateValid {
		t.Error("a forged license was adopted")
	}
	if !strings.Contains(buf.String(), "keeping the license in use") {
		t.Errorf("the refusal was not logged:\n%s", buf)
	}
}

// The expiry watchdog only stops a process that would lose something, and
// what it would lose changes with the rules. A reload that pushes the config
// past the free tier has to say so, or a term ending later takes nothing
// away from a config that now depends on it.
func TestAnAppliedReloadPublishesWhetherTheConfigNeedsALicense(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	if rl.lic.depends.Load() {
		t.Fatal("the base config fits the free tier; it must not depend on a license")
	}
	doc := licensetest.Document(t, licensetest.Enterprise())

	// Enough guardrail rules to exceed the free tier cap, so the config
	// stops being one the free tier would serve.
	overCapRules := `"rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]},
      {"name": "r1", "type": "deny_words_list", "words": ["truncate"]},
      {"name": "r2", "type": "deny_words_list", "words": ["delete from"]},
      {"name": "r3", "type": "deny_words_list", "words": ["alter table"]},
      {"name": "r4", "type": "deny_words_list", "words": ["grant"]},
      {"name": "r5", "type": "deny_words_list", "words": ["revoke"]}
    ]`
	drifted := editJSON(t, withLicense(t, reloadBase, doc),
		`"rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]}
    ]`, overCapRules)

	if got := applyWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if !rl.lic.depends.Load() {
		t.Error("the reloaded config exceeds the free tier and was not reported as needing a license")
	}
}

// An analyzer edit on a gRPC-transport lane cannot swap (its callbacks bind
// at server construction), so the whole document keeps the restart path:
// swapping the other lanes and logging "applied" would leave the gRPC lane
// serving old rules under a log line that says otherwise.
func TestAGRPCLaneAnalyzerEditKeepsTheRestartPath(t *testing.T) {
	desc := writeGRPCTestDescriptors(t)
	base := fmt.Sprintf(`{
  "analyzer": {"provider": "stub", "model": "m"},
  "listeners": [{
    "name": "g", "protocol": "grpc",
    "listen": "127.0.0.1:0", "upstream": "h:50051",
    "grpc": {"capture_payload": true, "descriptors": [%q]},
    "analyzer": {"trigger": {"operations": ["delete"]}, "high": "block"}
  }],
  "audit": {"file": "-"}
}`, desc)

	cfg, err := LoadConfigBytes([]byte(base))
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	cfg.cp = &controlPlane{url: "http://plane", token: "hsc_x", lastRaw: []byte(base)}
	ac, err := setupAnalyzer(cfg, nil)
	if err != nil {
		t.Fatalf("setupAnalyzer: %v", err)
	}
	lanes, err := buildLanes(cfg, nil, ac)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, map[string]*proxy.Server{}, nil, ac, view,
		newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatalf("newReloader: %v", err)
	}
	var buf bytes.Buffer

	drifted := editJSON(t, base, `"high": "block"`, `"high": "warn"`)
	if got := handleWith(rl, &buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, &buf)
	}
	if !strings.Contains(buf.String(), "restart to apply them") {
		t.Errorf("no restart log line:\n%s", &buf)
	}
	if strings.Contains(buf.String(), "configuration applied") {
		t.Errorf("a document a grpc lane cannot absorb was reported applied:\n%s", &buf)
	}
	if rl.gen != 0 {
		t.Errorf("generation = %d, want 0: nothing was applied", rl.gen)
	}
}

// A refused reload must not leak its candidate detector into the running
// analyzer dependencies: the pii section was not committed, so a later
// reload must not combine the uncommitted redaction behavior with the
// running maskers and pii evaluators.
func TestARefusedReloadDoesNotLeakTheDetector(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(reloadBase))
	if err != nil {
		t.Fatalf("LoadConfigBytes: %v", err)
	}
	det0 := &stubPlugin{entities: []string{"US_SSN"}}
	det1 := &stubPlugin{entities: []string{"CREDIT_CARD"}}
	cfg.cp = &controlPlane{
		url: "http://plane", token: "hsc_x", lastRaw: []byte(reloadBase),
		build: func(json.RawMessage) (Plugin, error) { return det1, nil },
	}
	ac := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: stubAnalyzerProvider{},
		det:      det0,
	}
	lanes, err := buildLanes(cfg, det0, ac)
	if err != nil {
		t.Fatalf("buildLanes: %v", err)
	}
	servers := map[string]*proxy.Server{}
	for _, ln := range lanes {
		srv, serr := buildServer(ln, cfg.Audit, nil, slog.Default())
		if serr != nil {
			t.Fatalf("buildServer: %v", serr)
		}
		servers[ln.name] = srv
	}
	view := &atomic.Pointer[laneState]{}
	view.Store(&laneState{lanes: lanes})
	rl, err := newReloader(cfg, lanes, servers, det0, ac, view,
		newLicenseState(cfg.lic, cfg.dependsOnLicense()))
	if err != nil {
		t.Fatalf("newReloader: %v", err)
	}
	var buf bytes.Buffer

	// A pii drift plus a second guardrail rule: the document decodes and
	// validates, the detector rebuilds, and then the free-tier cap refuses
	// the lanes. The candidate detector must go down with the document.
	refused := editJSON(t, reloadBase, `"audit"`, `"pii": {"entities": ["US_SSN"]}, "audit"`)
	refused = editJSON(t, refused,
		`{"name": "r0", "type": "deny_words_list", "words": ["drop table"]}`,
		`{"name": "r0", "type": "deny_words_list", "words": ["drop table"]},
		 {"name": "r1", "type": "deny_words_list", "words": ["truncate"]}`)
	if got := applyWith(rl, &buf, refused); got != reloadRefused {
		t.Fatalf("outcome = %v, want refused; log:\n%s", got, &buf)
	}
	if rl.ac.det != Plugin(det0) {
		t.Fatal("a refused reload leaked its candidate detector into the running deps")
	}
	if rl.det != Plugin(det0) {
		t.Fatal("a refused reload committed its detector")
	}

	// The same pii drift without the extra rule applies, and everything
	// moves together.
	applied := editJSON(t, reloadBase, `"audit"`, `"pii": {"entities": ["US_SSN"]}, "audit"`)
	if got := applyWith(rl, &buf, applied); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, &buf)
	}
	if rl.ac.det != Plugin(det1) || rl.det != Plugin(det1) {
		t.Fatal("an applied pii drift did not commit the detector everywhere")
	}
}

// In disk mode the same flag with different bytes means the license moved.
// The file is re-adopted and the rotation publishes the verified document --
// a renewal must not cost an outage -- with the plane as its source.
func TestADiskModeLicenseDriftIsAppliedHot(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.diskMode = true
	rl.configPath = writeConfig(t, reloadBase)
	rl.load = LoadConfig

	doc := licensetest.Document(t, licensetest.Enterprise())
	tiny, err := json.Marshal(map[string]any{"load_from_disk": true, "license": doc})
	if err != nil {
		t.Fatal(err)
	}
	if got := applyWith(rl, buf, string(tiny)); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	cur := rl.lic.get()
	if cur.State() != license.StateValid || cur.Source != PlaneLicenseSource {
		t.Errorf("license = %q from %q, want valid from the control plane", cur.State(), cur.Source)
	}
}

// A document the verifier refuses is kept out without freezing the rules:
// the reload still applies, the license in use stays, and handle remembers
// the document so a broken plane logs once, not once per tick.
func TestADiskModeGarbageLicenseKeepsTheLicenseInUse(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.diskMode = true
	rl.configPath = writeConfig(t, reloadBase)
	rl.load = LoadConfig
	before := rl.lic.get()

	tiny := `{"load_from_disk":true,"license":"not a license"}`
	if got := handleWith(rl, buf, tiny); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	after := rl.lic.get()
	if after.State() != before.State() || after.Source != before.Source {
		t.Errorf("a refused license changed the one in use: %q/%q -> %q/%q",
			before.State(), before.Source, after.State(), after.Source)
	}
	if !strings.Contains(buf.String(), "license this build refuses") {
		t.Errorf("the refusal was not logged:\n%s", buf)
	}
	if got := handleWith(rl, buf, tiny); got != reloadUnchanged {
		t.Fatalf("second handle = %v, want unchanged; log:\n%s", got, buf)
	}
}

// The plane removes the license while the file's rules fit the free tier:
// the process drops to the caps hot, the same answer a plane-owned removal
// gets, and a restart would resolve the same way.
func TestADiskModeLicenseRemovalDropsToTheFreeTier(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.diskMode = true
	rl.configPath = writeConfig(t, reloadBase)
	rl.load = LoadConfig

	doc := licensetest.Document(t, licensetest.Enterprise())
	tiny, err := json.Marshal(map[string]any{"load_from_disk": true, "license": doc})
	if err != nil {
		t.Fatal(err)
	}
	if got := applyWith(rl, buf, string(tiny)); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if rl.lic.get().State() != license.StateValid {
		t.Fatalf("license state = %q, want valid before the removal", rl.lic.get().State())
	}

	if got := applyWith(rl, buf, `{"load_from_disk":true}`); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if rl.lic.get().State() != license.StateMissing {
		t.Fatalf("license state = %q, want missing after the removal", rl.lic.get().State())
	}
}

// The plane taking the config back (a full document instead of the flag)
// flips ownership hot when the document's non-rule half matches what runs:
// rules swap, the mode flips, and nothing restarts.
func TestAPlaneTakeoverAppliesHotWhenOnlyRulesMoved(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.diskMode = true

	drifted := editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`)
	if got := applyWith(rl, buf, drifted); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if rl.diskMode {
		t.Error("the mode did not flip to plane-owned")
	}
	if !strings.Contains(buf.String(), "took ownership") {
		t.Errorf("no ownership log line:\n%s", buf)
	}
}

// A takeover whose document also moves the topology cannot flip hot:
// sockets would have to rebind. The restart path keeps the file serving,
// and the mode stays put so the next tick is not misread as owned drift.
func TestAPlaneTakeoverWithTopologyDriftKeepsTheRestartPath(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.diskMode = true

	drifted := editJSON(t, reloadBase, `"upstream": "h:5432"`, `"upstream": "other:5432"`)
	if got := applyWith(rl, buf, drifted); got != reloadRestart {
		t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
	}
	if !rl.diskMode {
		t.Error("a restart-bound takeover flipped the mode")
	}
	if !strings.Contains(buf.String(), "restart to apply it") {
		t.Errorf("no restart log line:\n%s", buf)
	}
}

// The flip the other way: the plane hands ownership to the file, and the
// file is re-read and adopted hot when its non-rule half matches what runs.
func TestAHandoverAdoptsTheFileHot(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.configPath = writeConfig(t,
		editJSON(t, reloadBase, `"words": ["drop table"]`, `"words": ["truncate"]`))
	rl.load = LoadConfig

	if got := applyWith(rl, buf, `{"load_from_disk":true}`); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if !rl.diskMode {
		t.Error("the mode did not flip to disk")
	}
	if !strings.Contains(buf.String(), "handed the configuration to the local file") {
		t.Errorf("no handover log line:\n%s", buf)
	}
}

// A handover's tiny document still carries the license, and it rides the
// same rotation as a plane-owned one: verified, then published as the
// plane's.
func TestAHandoverAdoptsThePlaneLicense(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	rl.configPath = writeConfig(t, reloadBase)
	rl.load = LoadConfig

	doc := licensetest.Document(t, licensetest.Enterprise())
	tiny, err := json.Marshal(map[string]any{"load_from_disk": true, "license": doc})
	if err != nil {
		t.Fatal(err)
	}
	if got := applyWith(rl, buf, string(tiny)); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	cur := rl.lic.get()
	if cur.State() != license.StateValid || cur.Source != PlaneLicenseSource {
		t.Errorf("license = %q from %q, want valid from the control plane", cur.State(), cur.Source)
	}
}

// A handover this process cannot absorb stays on the restart path: no file,
// a file that moved the topology, or a file writing the plane-only key. The
// mode never flips on a document that did not apply.
func TestAHandoverTheProcessCannotAbsorbKeepsTheRestartPath(t *testing.T) {
	topo := editJSON(t, reloadBase, `"upstream": "h:5432"`, `"upstream": "other:5432"`)
	withKey := editJSON(t, reloadBase, `"listeners": [{`, `"load_from_disk": false, "listeners": [{`)
	for _, tc := range []struct {
		name, file, wants string
	}{
		{"no file", "", "started without a config file"},
		{"topology drift", topo, "restart to apply it"},
		{"file writes the key", withKey, "load_from_disk"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rl, buf := testReloader(t, reloadBase)
			if tc.file != "" {
				rl.configPath = writeConfig(t, tc.file)
				rl.load = LoadConfig
			}
			if got := applyWith(rl, buf, `{"load_from_disk":true}`); got != reloadRestart {
				t.Fatalf("outcome = %v, want restart; log:\n%s", got, buf)
			}
			if rl.diskMode {
				t.Error("a restart-bound handover flipped the mode")
			}
			if !strings.Contains(buf.String(), tc.wants) {
				t.Errorf("the log does not name the problem (%q):\n%s", tc.wants, buf)
			}
		})
	}
}

// The steady state: every tick answers the same tiny doc the startup
// handshake seeded, and nothing runs or logs.
func TestAnUnchangedDiskModeDocIsDropped(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	tiny := `{"load_from_disk":true,"license":"L"}`
	rl.diskMode = true
	rl.lastHandled = []byte(tiny) // what cp.lastRaw seeds after a disk-mode handshake

	if got := handleWith(rl, buf, tiny); got != reloadUnchanged {
		t.Fatalf("outcome = %v, want unchanged; log:\n%s", got, buf)
	}
	if buf.Len() != 0 {
		t.Errorf("an unchanged doc logged:\n%s", buf)
	}
}

// The admin removes the organization's license in the control plane. The
// process drops to the free tier, and does NOT fall back to a license sitting
// in a local source: the plane owns the fleet's license, so removing it there
// has to remove it here. Startup answers the same way, which is the point --
// a restart must not relicense what a heartbeat just unlicensed.
func TestALicenseRemovedFromThePlaneDropsToTheFreeTier(t *testing.T) {
	t.Setenv(license.EnvVar, licensetest.Document(t, licensetest.Enterprise()))
	rl, buf := testReloader(t, reloadBase)
	doc := licensetest.Document(t, licensetest.Enterprise())

	if got := applyWith(rl, buf, withLicense(t, reloadBase, doc)); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if got := rl.lic.get().State(); got != license.StateValid {
		t.Fatalf("license state = %q, want valid before the removal", got)
	}

	if got := applyWith(rl, buf, reloadBase); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	if got := rl.lic.get().State(); got != license.StateMissing {
		t.Fatalf("license state = %q, want missing: a local source relicensed the process", got)
	}
}

// The plane goes unreachable. The heartbeat degrades and the license in use
// stays: killing the caps over a lost connection would be an outage, and the
// document the plane last sent is still the one it issued.
func TestAnUnreachablePlaneKeepsTheLicenseInUse(t *testing.T) {
	rl, buf := testReloader(t, reloadBase)
	doc := licensetest.Document(t, licensetest.Enterprise())
	if got := applyWith(rl, buf, withLicense(t, reloadBase, doc)); got != reloadApplied {
		t.Fatalf("outcome = %v, want applied; log:\n%s", got, buf)
	}
	before := rl.lic.get()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cp := &controlPlane{url: srv.URL, token: "hsc_x", every: time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	log := slog.New(slog.NewTextHandler(buf, nil))
	cp.heartbeat(ctx, log, rl)

	if !strings.Contains(buf.String(), "serving the last good config") {
		t.Fatalf("the failed handshake was not reported:\n%s", buf)
	}
	after := rl.lic.get()
	if after.State() != before.State() || after.Source != before.Source {
		t.Errorf("the license changed while the plane was unreachable: %q/%q -> %q/%q",
			before.State(), before.Source, after.State(), after.Source)
	}
	if after.State() != license.StateValid {
		t.Errorf("license state = %q, want valid", after.State())
	}
}

// A downgrade the running rules do not fit under. The old license must not
// keep serving them: `handle` records this document as handled, so no later
// heartbeat revisits it, and without the stop the organization would serve
// paid rules until the term ended or somebody restarted the process.
func TestANarrowerLicenseStopsTheRelay(t *testing.T) {
	// A config that needs a license: six guardrail rules, over the free cap.
	overCap := editJSON(t, reloadBase,
		`"rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]}
    ]`,
		`"rules": [
      {"name": "r0", "type": "deny_words_list", "words": ["drop table"]},
      {"name": "r1", "type": "deny_words_list", "words": ["truncate"]},
      {"name": "r2", "type": "deny_words_list", "words": ["delete from"]},
      {"name": "r3", "type": "deny_words_list", "words": ["alter table"]},
      {"name": "r4", "type": "deny_words_list", "words": ["grant"]},
      {"name": "r5", "type": "deny_words_list", "words": ["revoke"]}
    ]`)
	doc := licensetest.Document(t, licensetest.Enterprise())
	rl, buf := testReloader(t, withLicense(t, overCap, doc))

	// The plane removes the license. The rules no longer fit.
	if got := applyWith(rl, buf, overCap); got != reloadRefused {
		t.Fatalf("outcome = %v, want refused; log:\n%s", got, buf)
	}
	if got := rl.lic.get().State(); got != license.StateMissing {
		t.Fatalf("license state = %q, want missing: the old license kept serving", got)
	}
	if !rl.lic.overCap.Load() {
		t.Fatal("the relay was not told to stop under a license that no longer covers its rules")
	}
	if !strings.Contains(buf.String(), "stopping the relay") {
		t.Errorf("the stop was not logged:\n%s", buf)
	}
}

// The watchdog is what turns that flag into a stop, by the same controlled
// drain an ended term gets.
func TestTheWatchdogStopsOnAnUncoveredLicense(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := newLicenseState(licensetest.Status(t, licensetest.Enterprise()), false)
	var buf bytes.Buffer
	expired := watchLicense(ctx, st, time.Millisecond, slog.New(slog.NewTextHandler(&buf, nil)))

	select {
	case <-expired:
		t.Fatal("the watchdog stopped a relay whose license still covers it")
	case <-time.After(30 * time.Millisecond):
	}

	st.overCap.Store(true)
	select {
	case <-expired:
	case <-time.After(2 * time.Second):
		t.Fatal("the watchdog did not stop a relay the license stopped covering")
	}
	if !strings.Contains(buf.String(), "no longer covers") {
		t.Errorf("the stop was not explained:\n%s", &buf)
	}
}
