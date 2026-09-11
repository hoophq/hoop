package daemon

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

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
	cfg.cp = &controlPlane{url: "http://plane", token: "hsc_x", lastRaw: []byte(raw)}

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
