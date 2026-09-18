package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
)

// TestAppendGuardrailsPreservesTheOptOut guards the one merge subtlety that is
// invisible in the type. `rules: []` is how a lane says "none of the
// defaults"; composing must add to it, never replace it, or a lane that opted
// out silently starts enforcing again.
func TestAppendGuardrailsPreservesTheOptOut(t *testing.T) {
	// An opt-out is an empty but NON-nil slice: that is exactly the
	// distinction the daemon's own merge reads, and it does not survive a
	// length test.
	optedOut := &daemon.GuardrailsConfig{Rules: []policy.Rule{}}
	added := []policy.Rule{{Name: "r", Type: "deny_words_list", Words: []string{"X"}}}

	out := appendGuardrails(optedOut, added)
	if len(out.Rules) != 1 {
		t.Fatalf("want the bound rule appended to the opt-out, got %d rules", len(out.Rules))
	}
	// The caller's block must not be mutated: the stored row is shared with
	// whatever else reads it this request.
	if len(optedOut.Rules) != 0 {
		t.Errorf("the source block was mutated: %+v", optedOut.Rules)
	}

	// An absent block is created rather than skipped.
	fresh := appendGuardrails(nil, added)
	if fresh == nil || len(fresh.Rules) != 1 {
		t.Errorf("want a block created for a listener that had none, got %+v", fresh)
	}
}

// boundEverything is one sidecar with a rule of every kind bound to one of its
// two lanes. Every spec is written the way a control plane stores it: the
// block the listener receives, in the sidecar's own vocabulary.
func boundEverything() (daemon.Config, []models.BoundRule, []models.BoundRule, []models.BoundRule) {
	cfg := daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "appdb", Protocol: "postgres", Listen: ":5432", Upstream: "db:5432",
			Analyzer: &daemon.LaneAnalyzerConfig{MaxCalls: 40}},
		{Name: "reporting", Protocol: "mysql", Listen: ":3306", Upstream: "warehouse:3306"},
	}}
	return cfg,
		[]models.BoundRule{
			// A rule type the GATEWAY has no concept of, which is the whole
			// point of storing the sidecar's own vocabulary: `operation` reads
			// the scanner's classification, so it survives
			// `SELECT 'DROP TABLE customers'` where a word list does not.
			{RuleName: "no-destructive-sql", ListenerName: "appdb", Spec: json.RawMessage(
				`{"rules":[{"name":"no-destructive-sql","type":"operation",` +
					`"operations":["drop","truncate"],"message":"ask the data team"}]}`)},
			{RuleName: "read-only-customers", ListenerName: "appdb", Spec: json.RawMessage(
				`{"rules":[{"name":"read-only-customers","type":"table","tables":["customers"],` +
					`"access":"write","require_table_match":true}]}`)},
		},
		[]models.BoundRule{
			{RuleName: "mask-customer-pii", ListenerName: "appdb", Spec: json.RawMessage(
				`{"rules":[{"name":"ssn","entities":["US_SSN"],"strategy":"partial","keep_last":4},` +
					`{"name":"ssn-column","columns":["ssn"],"strategy":"hash"}]}`)},
		},
		[]models.BoundRule{
			{RuleName: "risky-writes", ListenerName: "appdb", Spec: json.RawMessage(
				`{"trigger":{"operations":["update","delete"]},"high":"block","medium":"warn",` +
					`"prompt":"Treat the payments schema as high risk."}`)},
		}
}

// TestComposedDocumentStillDecodesStrictly is the test that stands between this
// feature and a bricked fleet.
//
// A sidecar decodes its document with DisallowUnknownFields at three layers: a
// key it does not declare refuses the WHOLE configuration, and the sidecar
// cannot recover on its own. The control plane's own strict parser is what
// checks it here, rather than a hand-listed set of keys.
func TestComposedDocumentStillDecodesStrictly(t *testing.T) {
	cfg, guardrails, masking, analyzers := boundEverything()
	composed, err := foldSidecarRules(cfg, guardrails, masking, analyzers)
	if err != nil {
		t.Fatalf("composition failed: %v", err)
	}
	raw, err := json.Marshal(composed)
	if err != nil {
		t.Fatalf("the composed document does not marshal: %v", err)
	}
	if _, err := ParseSidecarConfiguration(raw); err != nil {
		t.Fatalf("the composed document would be refused by a sidecar: %v\ndocument: %s", err, raw)
	}

	// Nothing is written at the top level: a rule binds to listeners only.
	if composed.Guardrails != nil || composed.Mask != nil {
		t.Errorf("composition wrote a top-level block that no binding targets: guardrails=%+v mask=%+v",
			composed.Guardrails, composed.Mask)
	}

	appdb := composed.Listeners[0]
	// Guardrails CONCATENATE, so both bound rules are on the lane, and the
	// sidecar's own vocabulary survived verbatim.
	if appdb.Guardrails == nil || len(appdb.Guardrails.Rules) != 2 {
		t.Fatalf("want both guardrails on the lane: %+v", appdb.Guardrails)
	}
	if got := appdb.Guardrails.Rules[0]; string(got.Type) != "operation" ||
		len(got.Operations) != 2 || string(got.Operations[0]) != "drop" {
		t.Errorf("the operation rule did not survive composition: %+v", got)
	}
	if got := appdb.Guardrails.Rules[1]; got.Access != "write" || !got.RequireTableMatch {
		t.Errorf("the table rule's access split did not survive: %+v", got)
	}

	// Masking REPLACES, and both entries of the one bound rule are there.
	var maskRules []map[string]any
	if appdb.Mask == nil {
		t.Fatal("the masking rule is missing from its lane")
	}
	if err := json.Unmarshal(appdb.Mask.Rules, &maskRules); err != nil {
		t.Fatalf("the composed mask rules are not a list: %v", err)
	}
	if len(maskRules) != 2 || maskRules[0]["strategy"] != "partial" || maskRules[1]["strategy"] != "hash" {
		t.Errorf("the strategies did not survive composition: %+v", maskRules)
	}

	// The analyzer block replaces the lane's, carrying the trigger.
	if appdb.Analyzer == nil || appdb.Analyzer.HighRisk != "block" || appdb.Analyzer.Trigger == nil {
		t.Errorf("the analyzer block did not land on its lane: %+v", appdb.Analyzer)
	}

	// A lane nothing was bound to is left exactly as its author wrote it.
	if composed.Listeners[1].Guardrails != nil || composed.Listeners[1].Mask != nil {
		t.Errorf("an unbound lane was modified: %+v", composed.Listeners[1])
	}
}

// TestCompositionNeverReachesTheBaseline is the invariant the whole design
// rests on.
//
// A sidecar hot-swaps the rule sections with no dropped connection, and
// RESTARTS for anything else. If composition ever wrote outside those
// sections, every rule edit would become a fleet restart -- the opposite of
// what configuring from a control plane is for. daemon.BaselineDoc is the
// authority on where that line is, so the test asks it rather than repeating
// the list.
func TestCompositionNeverReachesTheBaseline(t *testing.T) {
	cfg, guardrails, masking, analyzers := boundEverything()

	before, err := daemon.BaselineDoc(&cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	composed, err := foldSidecarRules(cfg, guardrails, masking, analyzers)
	if err != nil {
		t.Fatalf("composition failed: %v", err)
	}
	after, err := daemon.BaselineDoc(&composed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("composition changed the restart-bound baseline, so every rule edit "+
			"would restart the fleet\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestCompositionLeavesTheStoredDocumentAlone pins the other half of
// compose-on-read: the row the request loaded is shared, and folding in place
// would make the next handshake compose an already-composed document.
func TestCompositionLeavesTheStoredDocumentAlone(t *testing.T) {
	cfg, guardrails, masking, analyzers := boundEverything()
	stored, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := foldSidecarRules(cfg, guardrails, masking, analyzers); err != nil {
		t.Fatalf("composition failed: %v", err)
	}
	again, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stored) != string(again) {
		t.Fatalf("the stored document was mutated by composing it\nbefore: %s\nafter:  %s", stored, again)
	}
}

// TestCompositionRefusesARuleBoundToAMissingListener pins the failure mode a
// rename creates. Skipping the binding would serve a document enforcing less
// than the admin sees bound, silently; the refusal reaches the handshake,
// which a sidecar survives by keeping the rules it already has.
func TestCompositionRefusesARuleBoundToAMissingListener(t *testing.T) {
	cfg, guardrails, _, _ := boundEverything()
	cfg.Listeners[0].Name = "appdb-renamed"

	_, err := foldSidecarRules(cfg, guardrails, nil, nil)
	if err == nil {
		t.Fatal("want a refusal for a rule bound to a listener that no longer exists")
	}
	for _, want := range []string{"no-destructive-sql", "appdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the rule and the listener, it omits %q: %v", want, err)
		}
	}
}

// TestABindingWithNoListenerIsRefused pins the scope decision. A rule bound to
// a sidecar rather than a listener would write the document's top-level block,
// which a lane carrying its own mask block REPLACES -- so the same rule would
// apply on some lanes and be ignored on others, with nothing in the UI saying
// which. The listener is also what carries the protocol, and the protocol
// decides which rule types and masking strategies the lane can run at all.
func TestABindingWithNoListenerIsRefused(t *testing.T) {
	cfg, guardrails, masking, analyzers := boundEverything()
	for _, tt := range []struct {
		name  string
		bound []models.BoundRule
		fold  func(daemon.Config, []models.BoundRule) error
	}{
		{"guardrail", guardrails, func(c daemon.Config, b []models.BoundRule) error {
			_, err := foldSidecarRules(c, b, nil, nil)
			return err
		}},
		{"data masking", masking, func(c daemon.Config, b []models.BoundRule) error {
			_, err := foldSidecarRules(c, nil, b, nil)
			return err
		}},
		{"analyzer", analyzers, func(c daemon.Config, b []models.BoundRule) error {
			_, err := foldSidecarRules(c, nil, nil, b)
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			unbound := append([]models.BoundRule{}, tt.bound...)
			unbound[0].ListenerName = ""
			err := tt.fold(cfg, unbound)
			if err == nil {
				t.Fatal("want a refusal for a binding that names no listener")
			}
			if !strings.Contains(err.Error(), "listener") {
				t.Errorf("the refusal must say what is missing: %v", err)
			}
		})
	}
}

// TestTwoAnalyzerRulesOnOneListenerAreRefused pins the one merge the daemon
// has no shape for: a listener runs ONE analyzer block, so two bound rules is
// a conflict rather than a union, and resolving it by whichever row came back
// last would make the served config depend on a sort order.
func TestTwoAnalyzerRulesOnOneListenerAreRefused(t *testing.T) {
	cfg, _, _, analyzers := boundEverything()
	second := analyzers[0]
	second.RuleName = "also-risky"

	_, err := foldSidecarRules(cfg, nil, nil, append(analyzers, second))
	if err == nil {
		t.Fatal("want a refusal for two analyzer rules on one listener")
	}
	for _, want := range []string{"risky-writes", "also-risky", "appdb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name both rules and the listener, it omits %q: %v", want, err)
		}
	}
}

// TestASpecWithAnUnknownKeyIsRefused pins the strict decode. sidecar_spec is a
// JSONB column and nothing in the database constrains its shape, so a key the
// daemon does not declare would otherwise reach a sidecar that refuses its
// whole document over it and cannot recover on its own.
func TestASpecWithAnUnknownKeyIsRefused(t *testing.T) {
	cfg, guardrails, _, _ := boundEverything()

	// `mode: observe` is a real field the daemon declares, and storing the
	// block verbatim is what makes it reachable from a control plane at all.
	// It must decode.
	guardrails[0].Spec = json.RawMessage(`{"mode":"observe","rules":[{"name":"r",` +
		`"type":"operation","operations":["drop"]}]}`)
	if _, err := foldSidecarRules(cfg, guardrails, nil, nil); err != nil {
		t.Fatalf("a field the daemon declares must decode: %v", err)
	}

	guardrails[0].Spec = json.RawMessage(`{"rules":[{"name":"r","type":"operation",` +
		`"operations":["drop"]}],"enforcement":"observe"}`)

	if _, err := foldSidecarRules(cfg, guardrails, nil, nil); err == nil {
		t.Fatal("want a refusal for a spec carrying a key no sidecar declares")
	}
}
