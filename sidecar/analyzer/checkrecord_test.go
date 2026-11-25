package analyzer_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
)

// recordEvaluator is checkEvaluator with a record redactor, as a build with a
// detector has.
func recordEvaluator(t *testing.T, p analyzer.Provider) *analyzer.Evaluator {
	t.Helper()
	return mustNew(t, analyzer.Config{
		Provider: p, Trigger: analyzer.Trigger{All: true}, CacheSize: 100, CacheTTL: time.Minute,
		ResourceCheck: true,
		RecordRedact:  func(s string) string { return strings.ReplaceAll(s, "4111111111111111", "<CREDIT_CARD>") },
	})
}

func TestTheTrailCarriesTheCheckAndTheProse(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow}
	ev := recordEvaluator(t, p)
	chk := bigOrders()
	stmt := sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders")

	got := ev.EvaluateWith(stmt, &policy.EvalContext{ResourceCheck: chk}).Annotations
	if got[analyzer.MetadataAITitle] != "dangerous statement" || got[analyzer.MetadataAIExplanation] != "because reasons" {
		t.Errorf("prose = %q / %q", got[analyzer.MetadataAITitle], got[analyzer.MetadataAIExplanation])
	}
	if got[analyzer.MetadataResourceCheckStatus] != string(analyzer.CheckRan) || got[analyzer.MetadataResourceCheckReason] != "" {
		t.Errorf("status %q, reason %q", got[analyzer.MetadataResourceCheckStatus], got[analyzer.MetadataResourceCheckReason])
	}
	text := got[analyzer.MetadataResourceCheck]
	if !strings.HasPrefix(text, "Table orders: about 1.2M rows") || !strings.Contains(text, "\nPlan:\n") {
		t.Errorf("check text:\n%s", text)
	}
	if !strings.HasSuffix(lastContent(p), text) {
		t.Errorf("the trail text is not what the model read:\n%s", lastContent(p))
	}
	if _, err := strconv.Atoi(got[analyzer.MetadataResourceCheckMS]); err != nil {
		t.Errorf("duration %q: %v", got[analyzer.MetadataResourceCheckMS], err)
	}

	again := ev.EvaluateWith(stmt, &policy.EvalContext{ResourceCheck: chk}).Annotations
	if again[analyzer.MetadataResourceCheckStatus] != string(analyzer.CheckCached) || again[analyzer.MetadataResourceCheck] != text {
		t.Errorf("cached check: %+v", again)
	}
	if _, ok := again[analyzer.MetadataResourceCheckMS]; ok {
		t.Error("a check that sent no query has a duration")
	}
}

func TestTheTrailSaysWhyTheCheckDidNotRunFully(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stmt   inspect.Statement
		chk    func(*fakeChecker)
		status analyzer.CheckStatus
		reason string
		text   bool
		ms     bool
	}{
		{"ddl", sqlStmt("DROP TABLE orders", inspect.OpDrop, "orders"), nil,
			analyzer.CheckSkipped, "only select, insert, update and delete are checked", false, false},
		{"no table", sqlStmt("SELECT 1", inspect.OpSelect), nil,
			analyzer.CheckSkipped, "the statement names no table", false, false},
		{"skip hook", sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"), func(f *fakeChecker) { f.skip = true },
			analyzer.CheckSkipped, "the statement can change the role or a setting", false, false},
		{"busy", sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"), func(f *fakeChecker) { f.ready = false },
			analyzer.CheckSkipped, "the connection was busy; cached facts only", false, false},
		{"failed", sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"),
			func(f *fakeChecker) { f.err = errors.New("write tcp 10.0.0.9:5432: broken pipe") },
			analyzer.CheckFailed, "the check failed; classified without it", false, true},
		{"cancelled", sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"),
			func(f *fakeChecker) { f.err = policy.ErrCheckCancelled; f.planErr = "SQLSTATE 57014 (query_canceled)" },
			analyzer.CheckCancelled, "cancelled after 500ms; plan: SQLSTATE 57014 (query_canceled)", true, true},
		{"partial", sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"),
			func(f *fakeChecker) { f.planErr = "SQLSTATE 42501 (insufficient_privilege)" },
			analyzer.CheckPartial, "plan: SQLSTATE 42501 (insufficient_privilege)", true, true},
		{"every part refused", sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"),
			func(f *fakeChecker) {
				f.tablesErr = "SQLSTATE 42501 (insufficient_privilege)"
				f.planErr = "SQLSTATE 42P01 (undefined_table)"
			},
			analyzer.CheckFailed, "tables: SQLSTATE 42501 (insufficient_privilege); plan: SQLSTATE 42P01 (undefined_table)", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chk := bigOrders()
			if tc.chk != nil {
				tc.chk(chk)
			}
			got := checkEvaluator(t, &stubProvider{level: analyzer.RiskLow}, nil).
				EvaluateWith(tc.stmt, &policy.EvalContext{ResourceCheck: chk}).Annotations
			if got[analyzer.MetadataResourceCheckStatus] != string(tc.status) || got[analyzer.MetadataResourceCheckReason] != tc.reason {
				t.Errorf("status %q, reason %q", got[analyzer.MetadataResourceCheckStatus], got[analyzer.MetadataResourceCheckReason])
			}
			if _, ok := got[analyzer.MetadataResourceCheck]; ok != tc.text {
				t.Errorf("text present %v, want %v", ok, tc.text)
			}
			if _, ok := got[analyzer.MetadataResourceCheckMS]; ok != tc.ms {
				t.Errorf("duration present %v, want %v", ok, tc.ms)
			}
		})
	}
}

func TestNoCheckNoCheckKeys(t *testing.T) {
	stmt := sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders")
	off := mustNew(t, analyzer.Config{Provider: &stubProvider{level: analyzer.RiskLow}, Trigger: analyzer.Trigger{All: true},
		RecordRedact: func(s string) string { return s }})
	for name, got := range map[string]map[string]string{
		"field off":  off.EvaluateWith(stmt, &policy.EvalContext{ResourceCheck: bigOrders()}).Annotations,
		"no checker": recordEvaluator(t, &stubProvider{level: analyzer.RiskLow}).EvaluateWith(stmt, &policy.EvalContext{}).Annotations,
	} {
		for k := range got {
			if strings.HasPrefix(k, "resource_check") {
				t.Errorf("%s: %s = %q", name, k, got[k])
			}
		}
		if got[analyzer.MetadataAITitle] == "" {
			t.Errorf("%s: the title is missing", name)
		}
	}
}

func TestARefusedBlockLeavesItsReasonInTheTrail(t *testing.T) {
	ev := mustNew(t, analyzer.Config{Provider: &stubProvider{level: analyzer.RiskLow}, Trigger: analyzer.Trigger{All: true},
		ResourceCheck: true, Redact: func(s string) string {
			if strings.Contains(s, "Resource check") {
				return analyzer.RefuseSentinel
			}
			return s
		}})
	got := ev.EvaluateWith(sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"),
		&policy.EvalContext{ResourceCheck: bigOrders()}).Annotations
	if got[analyzer.MetadataResourceCheck] != "" ||
		got[analyzer.MetadataResourceCheckReason] != "the redactor refused the check block; classified without it" {
		t.Fatalf("annotations = %+v", got)
	}
}

func TestTheTrailTextAndProseGoThroughTheRecordRedactor(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow, title: "reads card 4111111111111111"}
	ev := mustNew(t, analyzer.Config{Provider: p, Trigger: analyzer.Trigger{All: true}, ResourceCheck: true,
		Redact: func(s string) string { return s + "\n\n[proxy: this statement contained NAME]" },
		RecordRedact: func(s string) string {
			return strings.ReplaceAll(strings.ReplaceAll(s, "orders_pkey", "<NAME>"), "4111111111111111", "<CREDIT_CARD>")
		}})
	got := ev.EvaluateWith(sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"),
		&policy.EvalContext{ResourceCheck: bigOrders()}).Annotations
	text := got[analyzer.MetadataResourceCheck]
	if strings.Contains(text, "orders_pkey") || !strings.Contains(text, "<NAME>") || strings.Contains(text, "[proxy:") {
		t.Errorf("check text:\n%s", text)
	}
	if !strings.Contains(lastContent(p), "[proxy:") {
		t.Error("the model did not read the send redaction")
	}
	if got[analyzer.MetadataAITitle] != "reads card <CREDIT_CARD>" {
		t.Errorf("title = %q", got[analyzer.MetadataAITitle])
	}
}

func TestNoDetectorNoProse(t *testing.T) {
	got := checkEvaluator(t, &stubProvider{level: analyzer.RiskHigh}, nil).EvaluateWith(
		sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"), &policy.EvalContext{ResourceCheck: bigOrders()}).Annotations
	for _, k := range []string{analyzer.MetadataAITitle, analyzer.MetadataAIExplanation} {
		if _, ok := got[k]; ok {
			t.Errorf("%s = %q with no record redactor", k, got[k])
		}
	}
	if got[analyzer.MetadataResourceCheckStatus] != string(analyzer.CheckRan) {
		t.Errorf("the check is missing: %+v", got)
	}
}

func TestALaterLevelClearsWhatItDoesNotWrite(t *testing.T) {
	ec := &policy.EvalContext{ResourceCheck: bigOrders(), Annotations: map[string]string{
		analyzer.MetadataRiskLevel:           "low",
		analyzer.MetadataAITitle:             "A low title",
		analyzer.MetadataAIExplanation:       "A reasons",
		analyzer.MetadataResourceCheck:       "Table orders: about 1.2M rows",
		analyzer.MetadataResourceCheckStatus: "ran",
		analyzer.MetadataResourceCheckMS:     "3",
	}}
	noCheck := mustNew(t, analyzer.Config{Provider: &stubProvider{level: analyzer.RiskHigh, title: "B high title"},
		Trigger: analyzer.Trigger{All: true}, RecordRedact: func(s string) string { return s }})
	got := noCheck.EvaluateWith(sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"), ec).Annotations
	if got[analyzer.MetadataAITitle] != "B high title" {
		t.Errorf("title = %q", got[analyzer.MetadataAITitle])
	}
	for _, k := range []string{analyzer.MetadataResourceCheck, analyzer.MetadataResourceCheckStatus, analyzer.MetadataResourceCheckMS} {
		if v, ok := got[k]; !ok || v != "" {
			t.Errorf("%s = %q, %v: the low analyzer's check stays beside the high level", k, v, ok)
		}
	}
	if _, ok := got[analyzer.MetadataResourceCheckReason]; ok {
		t.Error("a key no analyzer wrote was added")
	}
}

func TestTheTrailShowsCostsAsTheModelReadsThem(t *testing.T) {
	chk := bigOrders()
	chk.plan = []inspect.PlanNode{{Node: "Limit", Rows: 3, Cost: 4.6}, {Depth: 1, Node: "Seq Scan", Relation: "orders", Rows: 3, Cost: 0.046}}
	text := recordEvaluator(t, &stubProvider{level: analyzer.RiskLow}).EvaluateWith(
		sqlStmt("SELECT * FROM orders LIMIT 3", inspect.OpSelect, "orders"), &policy.EvalContext{ResourceCheck: chk}).
		Annotations[analyzer.MetadataResourceCheck]
	if !strings.Contains(text, "cost 4.6)") || !strings.Contains(text, "cost 0.046)") {
		t.Errorf("check text:\n%s", text)
	}
}

func TestAnIndexDefinitionThatCanHoldALiteralStaysOutOfTheTrail(t *testing.T) {
	p := &stubProvider{level: analyzer.RiskLow}
	chk := bigOrders()
	f := chk.tables["orders"]
	f.Indexes = append(f.Indexes,
		inspect.IndexFacts{Name: "orders_vip", Definition: "btree (status) WHERE status = 'vip'"},
		inspect.IndexFacts{Name: "orders_big", Definition: "btree (total) WHERE total > 1000"},
		inspect.IndexFacts{Name: "orders_note", Definition: "gin (expression) partial"})
	chk.tables["orders"] = f

	text := checkEvaluator(t, p, nil).EvaluateWith(sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders"),
		&policy.EvalContext{ResourceCheck: chk}).Annotations[analyzer.MetadataResourceCheck]
	for _, want := range []string{"orders_pkey (primary key btree (id))", "orders_vip (definition withheld)",
		"orders_big (definition withheld)", "orders_note (gin (expression) partial)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "vip'") || strings.Contains(text, "1000") {
		t.Errorf("a literal reached the trail:\n%s", text)
	}
	if !strings.Contains(lastContent(p), "status = 'vip'") {
		t.Error("the model's block changed")
	}
	if chk.tables["orders"].Indexes[1].Definition != "btree (status) WHERE status = 'vip'" {
		t.Error("the checker's facts were rewritten")
	}
}

func TestOnlyTheLevelTheTrailKeepsWritesItsProse(t *testing.T) {
	stmt := sqlStmt("SELECT * FROM orders", inspect.OpSelect, "orders")
	for _, tc := range []struct {
		prior string
		level analyzer.RiskLevel
		want  bool
	}{
		{"", analyzer.RiskLow, true},
		{"low", analyzer.RiskHigh, true},
		{"high", analyzer.RiskLow, false},
		{"medium", analyzer.RiskMedium, false},
	} {
		ec := &policy.EvalContext{ResourceCheck: bigOrders(), Annotations: map[string]string{}}
		if tc.prior != "" {
			ec.Annotations[analyzer.MetadataRiskLevel] = tc.prior
		}
		got := recordEvaluator(t, &stubProvider{level: tc.level}).EvaluateWith(stmt, ec).Annotations
		_, title := got[analyzer.MetadataAITitle]
		_, check := got[analyzer.MetadataResourceCheckStatus]
		if title != tc.want || check != tc.want {
			t.Errorf("prior %q, level %s: title %v, check %v, want %v", tc.prior, tc.level, title, check, tc.want)
		}
	}
}
