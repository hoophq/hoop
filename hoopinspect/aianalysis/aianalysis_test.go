package aianalysis_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
	"github.com/hoophq/hoopinspect/aianalysis"
	"github.com/hoophq/hoopinspect/audit"
	"github.com/hoophq/hoopinspect/session"
)

// sqlStmt builds a Statement the way a codec would: the operation and tables
// come from the real classifier, so a test cannot accidentally hand the
// analyzer a hand-picked operation the parser would never produce.
func sqlStmt(sql string) hoopinspect.Statement {
	op, tables := hoopinspect.ClassifySQL(sql)
	return hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Text:      sql,
		Operation: op,
		Tables:    tables,
	}
}

func newAnalyzer(t *testing.T, cfg aianalysis.HeuristicConfig) *aianalysis.HeuristicAnalyzer {
	t.Helper()
	a, err := aianalysis.NewHeuristic(cfg)
	if err != nil {
		t.Fatalf("NewHeuristic: %v", err)
	}
	return a
}

func analyze(t *testing.T, a *aianalysis.HeuristicAnalyzer, stmt hoopinspect.Statement) *aianalysis.Verdict {
	t.Helper()
	v, err := a.Analyze(context.Background(), stmt)
	if err != nil {
		t.Fatalf("Analyze(%q): unexpected error %v", stmt.Text, err)
	}
	return v
}

func TestRiskLevelOrdering(t *testing.T) {
	if aianalysis.RiskHigh.Rank() <= aianalysis.RiskMedium.Rank() ||
		aianalysis.RiskMedium.Rank() <= aianalysis.RiskLow.Rank() ||
		aianalysis.RiskLow.Rank() <= aianalysis.RiskUnknown.Rank() {
		t.Fatalf("ranks are not strictly ordered: high=%d medium=%d low=%d unknown=%d",
			aianalysis.RiskHigh.Rank(), aianalysis.RiskMedium.Rank(),
			aianalysis.RiskLow.Rank(), aianalysis.RiskUnknown.Rank())
	}

	// An analyzer inventing a level must not outrank a real one.
	if aianalysis.RiskLevel("critical").Rank() >= aianalysis.RiskLow.Rank() {
		t.Error("an unrecognized risk level outranks low")
	}
	if aianalysis.RiskLevel("critical").Valid() {
		t.Error(`"critical" reported as a valid level`)
	}

	if got := aianalysis.MaxRisk(aianalysis.RiskLow, aianalysis.RiskHigh); got != aianalysis.RiskHigh {
		t.Errorf("MaxRisk(low, high) = %q, want high", got)
	}
	if got := aianalysis.MaxRisk(aianalysis.RiskHigh, aianalysis.RiskLow); got != aianalysis.RiskHigh {
		t.Errorf("MaxRisk(high, low) = %q, want high", got)
	}
}

func TestParseRiskLevel(t *testing.T) {
	// A store backend reads these back out of audit metadata, where casing and
	// whitespace survive whatever wrote them.
	for _, in := range []string{"high", "HIGH", " High "} {
		got, ok := aianalysis.ParseRiskLevel(in)
		if !ok || got != aianalysis.RiskHigh {
			t.Errorf("ParseRiskLevel(%q) = %q, %v; want high, true", in, got, ok)
		}
	}
	if _, ok := aianalysis.ParseRiskLevel("severe"); ok {
		t.Error(`ParseRiskLevel("severe") accepted an undefined level`)
	}
	if _, ok := aianalysis.ParseRiskLevel(""); ok {
		t.Error("ParseRiskLevel(\"\") accepted the unknown level as valid")
	}
}

func TestUnboundedDeleteIsHighAndBoundedIsNot(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	v := analyze(t, a, sqlStmt("DELETE FROM orders"))
	if v == nil || v.RiskLevel != aianalysis.RiskHigh {
		t.Fatalf("DELETE with no WHERE = %+v, want high", v)
	}
	if v.Rule != aianalysis.RuleUnboundedDelete {
		t.Errorf("rule = %q, want %q", v.Rule, aianalysis.RuleUnboundedDelete)
	}
	if !strings.Contains(v.Explanation, "orders") {
		t.Errorf("explanation does not name the table: %q", v.Explanation)
	}

	bounded := analyze(t, a, sqlStmt("DELETE FROM orders WHERE id = 1"))
	if bounded == nil {
		t.Fatal("bounded DELETE produced no verdict")
	}
	if bounded.RiskLevel == aianalysis.RiskHigh {
		t.Errorf("DELETE with a WHERE scored high: %+v", bounded)
	}
}

func TestUnboundedUpdateIsHigh(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	v := analyze(t, a, sqlStmt("UPDATE accounts SET balance = 0"))
	if v == nil || v.RiskLevel != aianalysis.RiskHigh || v.Rule != aianalysis.RuleUnboundedUpdate {
		t.Fatalf("unbounded UPDATE = %+v, want high/%s", v, aianalysis.RuleUnboundedUpdate)
	}

	bounded := analyze(t, a, sqlStmt("UPDATE accounts SET balance = 0 WHERE id = 7"))
	if bounded.RiskLevel == aianalysis.RiskHigh {
		t.Errorf("bounded UPDATE scored high: %+v", bounded)
	}
}

// The evasion: a WHERE that is not a WHERE. A substring match on "where" says
// every one of these is bounded, which is exactly the statement the analyzer
// exists to surface.
func TestWhereInsideCommentOrLiteralDoesNotCount(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	// Each of these contains the text "where" and yet has no WHERE clause.
	for name, sql := range map[string]string{
		"line comment":       "DELETE FROM audit_log -- WHERE keep = 1",
		"hash comment":       "DELETE FROM audit_log # WHERE keep = 1",
		"block comment":      "DELETE FROM audit_log /* WHERE keep = 1 */",
		"trailing comment":   "DELETE FROM notes /* nothing */ -- where body = 'x'",
		"string literal":     "UPDATE notes SET body = 'where clause'",
		"identifier prefix":  "DELETE FROM audit_log_where_archive",
		"quoted identifier":  `DELETE FROM "where"`,
		"unterminated block": "DELETE FROM audit_log /* WHERE keep = 1",
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(strings.ToLower(sql), "where") {
				t.Fatalf("test fixture %q has no 'where' text, so it proves nothing", sql)
			}
			v := analyze(t, a, sqlStmt(sql))
			if v == nil || v.RiskLevel != aianalysis.RiskHigh {
				t.Fatalf("%q = %+v, want high (the WHERE is not real)", sql, v)
			}
		})
	}

	// Controls: a real WHERE must still be honored, including when the table
	// itself is quoted. Blanking quoted-identifier contents must not blank the
	// clause keywords around them.
	for name, sql := range map[string]string{
		"after a comment": "/* cleanup job */ DELETE FROM audit_log WHERE ts < now()",
		"quoted table":    `DELETE FROM "audit_log" WHERE ts < now()`,
		"backtick table":  "DELETE FROM `audit_log` WHERE ts < now()",
		"bracket table":   "DELETE FROM [audit_log] WHERE ts < now()",
	} {
		t.Run(name, func(t *testing.T) {
			if v := analyze(t, a, sqlStmt(sql)); v.RiskLevel == aianalysis.RiskHigh {
				t.Errorf("%q: a real WHERE was not honored: %+v", sql, v)
			}
		})
	}
}

func TestDropTruncateGrantRevokeAreHigh(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	for _, tc := range []struct {
		sql  string
		rule string
	}{
		{"DROP TABLE customers", aianalysis.RuleDropObject},
		{"TRUNCATE TABLE events", aianalysis.RuleTruncate},
		{"GRANT ALL ON customers TO bob", aianalysis.RulePrivilegeChange},
		{"REVOKE SELECT ON customers FROM bob", aianalysis.RulePrivilegeChange},
	} {
		v := analyze(t, a, sqlStmt(tc.sql))
		if v == nil || v.RiskLevel != aianalysis.RiskHigh {
			t.Errorf("%q = %+v, want high", tc.sql, v)
			continue
		}
		if v.Rule != tc.rule {
			t.Errorf("%q rule = %q, want %q", tc.sql, v.Rule, tc.rule)
		}
	}
}

// A destructive verb hidden in a string literal must not promote the
// statement: the classifier strips literals, and the analyzer keys on its
// operation rather than on raw text.
func TestVerbInStringLiteralDoesNotEscalate(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	v := analyze(t, a, sqlStmt("SELECT 'DROP TABLE customers' AS msg WHERE 1 = 1"))
	if v == nil {
		t.Fatal("no verdict")
	}
	if v.RiskLevel == aianalysis.RiskHigh {
		t.Errorf("a DROP inside a literal escalated the statement: %+v", v)
	}
}

func TestBulkReadDetection(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	bulk := analyze(t, a, sqlStmt("SELECT * FROM orders"))
	if bulk == nil || bulk.RiskLevel != aianalysis.RiskMedium || bulk.Rule != aianalysis.RuleBulkRead {
		t.Fatalf("SELECT * FROM orders = %+v, want medium/%s", bulk, aianalysis.RuleBulkRead)
	}

	for name, sql := range map[string]string{
		"where bounds it":   "SELECT * FROM orders WHERE id = 3",
		"limit bounds it":   "SELECT * FROM orders LIMIT 10",
		"named columns":     "SELECT id, total FROM orders",
		"count is one row":  "SELECT count(*) FROM orders",
		"limit in comment":  "SELECT * FROM orders -- LIMIT 10",
		"top bounds it":     "SELECT TOP 10 * FROM orders",
		"fetch bounds it":   "SELECT * FROM orders FETCH FIRST 10 ROWS ONLY",
		"star in a literal": "SELECT id FROM orders",
	} {
		t.Run(name, func(t *testing.T) {
			v := analyze(t, a, sqlStmt(sql))
			// "limit in comment" is the inverse evasion: a commented-out LIMIT
			// bounds nothing, so it MUST still read as a bulk read.
			wantBulk := name == "limit in comment"
			gotBulk := v != nil && v.Rule == aianalysis.RuleBulkRead
			if gotBulk != wantBulk {
				t.Errorf("%q bulk=%v, want %v (verdict %+v)", sql, gotBulk, wantBulk, v)
			}
		})
	}
}

func TestSensitiveTableMatching(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{
		SensitiveTables: []string{"Customers", "hr.salaries"},
	})

	for name, tc := range map[string]struct {
		sql      string
		wantHigh bool
	}{
		"bare name":                {"SELECT id FROM customers WHERE id = 1", true},
		"schema qualified matches": {"SELECT id FROM public.customers WHERE id = 1", true},
		"config is case folded":    {"SELECT id FROM CUSTOMERS WHERE id = 1", true},
		"qualified config entry":   {"SELECT amount FROM hr.salaries WHERE id = 1", true},
		"different schema":         {"SELECT amount FROM payroll.salaries WHERE id = 1", false},
		"unrelated table":          {"SELECT id FROM orders WHERE id = 1", false},
		"suffix is not a match":    {"SELECT id FROM oldcustomers WHERE id = 1", false},
	} {
		t.Run(name, func(t *testing.T) {
			v := analyze(t, a, sqlStmt(tc.sql))
			if v == nil {
				t.Fatalf("%q produced no verdict", tc.sql)
			}
			gotHigh := v.RiskLevel == aianalysis.RiskHigh && v.Rule == aianalysis.RuleSensitiveTable
			if gotHigh != tc.wantHigh {
				t.Errorf("%q sensitive=%v, want %v (verdict %+v)", tc.sql, gotHigh, tc.wantHigh, v)
			}
			if tc.wantHigh && !strings.Contains(v.Explanation, "customers") && !strings.Contains(v.Explanation, "salaries") {
				t.Errorf("explanation does not name the table: %q", v.Explanation)
			}
		})
	}
}

func TestSchemaReadAndAlterAreMedium(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	for _, tc := range []struct {
		sql  string
		rule string
	}{
		{"SHOW TABLES", aianalysis.RuleSchemaRead},
		{"SELECT table_name FROM information_schema.tables WHERE 1=1", aianalysis.RuleSchemaRead},
		{"SELECT relname FROM pg_class WHERE 1=1", aianalysis.RuleSchemaRead},
		{"ALTER TABLE orders ADD COLUMN note text", aianalysis.RuleSchemaChange},
	} {
		v := analyze(t, a, sqlStmt(tc.sql))
		if v == nil || v.RiskLevel != aianalysis.RiskMedium || v.Rule != tc.rule {
			t.Errorf("%q = %+v, want medium/%s", tc.sql, v, tc.rule)
		}
	}
}

func TestHTTPRules(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	httpStmt := func(d *hoopinspect.HTTPDetail) hoopinspect.Statement {
		op := hoopinspect.OpGet
		if d.Method != "" {
			op = hoopinspect.Operation(strings.ToLower(d.Method))
		}
		return hoopinspect.Statement{Protocol: hoopinspect.HTTP, Operation: op, HTTP: d}
	}

	collection := analyze(t, a, httpStmt(&hoopinspect.HTTPDetail{
		Method: "DELETE", Path: "/users", Resource: "/users",
	}))
	if collection == nil || collection.RiskLevel != aianalysis.RiskHigh || collection.Rule != aianalysis.RuleHTTPUnboundedDel {
		t.Fatalf("DELETE /users = %+v, want high/%s", collection, aianalysis.RuleHTTPUnboundedDel)
	}
	if !strings.Contains(collection.Explanation, "/users") {
		t.Errorf("explanation does not name the resource: %q", collection.Explanation)
	}

	item := analyze(t, a, httpStmt(&hoopinspect.HTTPDetail{
		Method: "DELETE", Path: "/users/42", Resource: "/users/*",
	}))
	if item == nil || item.RiskLevel == aianalysis.RiskHigh {
		t.Errorf("DELETE /users/* = %+v, want below high", item)
	}

	filtered := analyze(t, a, httpStmt(&hoopinspect.HTTPDetail{
		Method: "DELETE", Path: "/users", Resource: "/users",
		Query: map[string][]string{"status": {"stale"}},
	}))
	if filtered == nil || filtered.RiskLevel == aianalysis.RiskHigh {
		t.Errorf("DELETE /users?status=stale = %+v, want below high", filtered)
	}

	fivexx := analyze(t, a, httpStmt(&hoopinspect.HTTPDetail{StatusCode: 503}))
	if fivexx == nil || fivexx.RiskLevel != aianalysis.RiskMedium || fivexx.Rule != aianalysis.RuleServerError {
		t.Fatalf("503 response = %+v, want medium/%s", fivexx, aianalysis.RuleServerError)
	}
	if !strings.Contains(fivexx.Explanation, "503") {
		t.Errorf("explanation does not name the status: %q", fivexx.Explanation)
	}

	// 4xx is the client's fault, not the server's, and flagging every 404
	// would drown the signal.
	fourxx := analyze(t, a, httpStmt(&hoopinspect.HTTPDetail{StatusCode: 404}))
	if fourxx != nil && fourxx.Rule == aianalysis.RuleServerError {
		t.Errorf("404 flagged as a server error: %+v", fourxx)
	}
}

func TestCustomPatternsRunAfterBuiltins(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{
		Patterns: []aianalysis.Pattern{{
			Name:        "nolock",
			Regex:       `\bWITH\s*\(\s*NOLOCK\s*\)`,
			RiskLevel:   aianalysis.RiskMedium,
			Title:       "Dirty read hint",
			Explanation: "NOLOCK reads uncommitted rows, so the result may contain data that was rolled back.",
		}},
	})

	hit := analyze(t, a, sqlStmt("SELECT id FROM ledger WITH (NOLOCK) WHERE id = 1"))
	if hit == nil || hit.RiskLevel != aianalysis.RiskMedium {
		t.Fatalf("custom pattern = %+v, want medium", hit)
	}
	if !strings.HasPrefix(hit.Rule, aianalysis.RuleCustomPattern+":") || !strings.HasSuffix(hit.Rule, "nolock") {
		t.Errorf("rule = %q, want the custom pattern name", hit.Rule)
	}

	// A built-in high finding must not be masked by a matching site rule.
	both := analyze(t, a, sqlStmt("DELETE FROM ledger WITH (NOLOCK)"))
	if both == nil || both.RiskLevel != aianalysis.RiskHigh {
		t.Errorf("a custom pattern masked an unbounded DELETE: %+v", both)
	}
}

func TestInvalidConfigRejectedNamingEveryProblem(t *testing.T) {
	_, err := aianalysis.NewHeuristic(aianalysis.HeuristicConfig{
		SensitiveTables: []string{"customers", "  "},
		Patterns: []aianalysis.Pattern{
			{Name: "bad-regex", Regex: "([", RiskLevel: aianalysis.RiskHigh, Explanation: "x"},
			{Name: "no-level", Regex: "x", Explanation: "x"},
			{Name: "no-explanation", Regex: "x", RiskLevel: aianalysis.RiskLow},
			{Name: "", Regex: "x", RiskLevel: aianalysis.RiskLow, Explanation: "x"},
			{Name: "dup", Regex: "x", RiskLevel: aianalysis.RiskLow, Explanation: "x"},
			{Name: "dup", Regex: "x", RiskLevel: aianalysis.RiskLow, Explanation: "x"},
			{Name: "no-regex", RiskLevel: aianalysis.RiskLow, Explanation: "x"},
		},
	})
	if err == nil {
		t.Fatal("invalid config accepted")
	}

	// One round trip to fix the config means one error naming all of them.
	for _, want := range []string{
		"sensitive_tables[1]",
		"bad-regex",
		"no-level",
		"no-explanation",
		"patterns[3]",
		"duplicate pattern name",
		"no-regex",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func TestValidConfigNormalizes(t *testing.T) {
	// Duplicate and differently-cased entries must not become two rules or a
	// config error.
	a := newAnalyzer(t, aianalysis.HeuristicConfig{
		SensitiveTables: []string{"Customers", "customers"},
	})
	v := analyze(t, a, sqlStmt("SELECT id FROM customers WHERE id = 1"))
	if v == nil || v.Rule != aianalysis.RuleSensitiveTable {
		t.Fatalf("case-folded duplicate table did not match: %+v", v)
	}
}

func TestUnknownOperationYieldsNoOpinion(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	// "we could not read this" must not be recorded as "we read it and it was
	// fine".
	v := analyze(t, a, hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Text:      "\x00\x01 garbage",
		Operation: hoopinspect.OpUnknown,
	})
	if v != nil {
		t.Errorf("unknown operation produced a verdict: %+v", v)
	}
}

func TestEveryVerdictHasASpecificExplanation(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{SensitiveTables: []string{"customers"}})

	stmts := []hoopinspect.Statement{
		sqlStmt("DELETE FROM orders"),
		sqlStmt("UPDATE orders SET x = 1"),
		sqlStmt("DROP TABLE orders"),
		sqlStmt("TRUNCATE TABLE orders"),
		sqlStmt("GRANT ALL ON orders TO bob"),
		sqlStmt("SELECT id FROM customers WHERE id = 1"),
		sqlStmt("SELECT * FROM orders"),
		sqlStmt("ALTER TABLE orders ADD COLUMN n int"),
		sqlStmt("SHOW TABLES"),
		sqlStmt("SELECT id FROM orders WHERE id = 1"),
		sqlStmt("INSERT INTO orders (id) VALUES (1)"),
	}

	seen := map[string]bool{}
	for _, stmt := range stmts {
		v := analyze(t, a, stmt)
		if v == nil {
			t.Errorf("no verdict for %q", stmt.Text)
			continue
		}
		if !v.RiskLevel.Valid() {
			t.Errorf("%q: invalid risk level %q", stmt.Text, v.RiskLevel)
		}
		if v.Title == "" {
			t.Errorf("%q: empty title", stmt.Text)
		}
		// Short or duplicated text is the generic-explanation failure mode:
		// "high risk detected" on every row teaches people to ignore the badge.
		if len(v.Explanation) < 40 {
			t.Errorf("%q: explanation too short to be specific: %q", stmt.Text, v.Explanation)
		}
		if v.Rule == "" {
			t.Errorf("%q: empty rule", stmt.Text)
		}
		if v.Score <= 0 || v.Score > 1 {
			t.Errorf("%q: score %v out of (0,1]", stmt.Text, v.Score)
		}
		if seen[v.Explanation] && v.Rule != aianalysis.RuleRecognizedRoutine {
			t.Errorf("%q: explanation reused verbatim by another rule: %q", stmt.Text, v.Explanation)
		}
		seen[v.Explanation] = true
	}
}

// --- session rollup ------------------------------------------------------

type fixedAnalyzer struct {
	verdicts []*aianalysis.Verdict
	errs     []error
	calls    int
}

func (f *fixedAnalyzer) Analyze(_ context.Context, _ hoopinspect.Statement) (*aianalysis.Verdict, error) {
	i := f.calls
	f.calls++
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	if err != nil {
		return nil, err
	}
	if i < len(f.verdicts) {
		return f.verdicts[i], nil
	}
	return nil, nil
}

func nStatements(n int) []hoopinspect.Statement {
	out := make([]hoopinspect.Statement, n)
	for i := range n {
		out[i] = sqlStmt("SELECT 1")
	}
	return out
}

func TestSessionRollupTakesMaxNotAverage(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	// Fifty harmless statements and one DROP. An average would bury it.
	stmts := make([]hoopinspect.Statement, 0, 51)
	for range 50 {
		stmts = append(stmts, sqlStmt("SELECT id FROM orders WHERE id = 1"))
	}
	stmts = append(stmts, sqlStmt("DROP TABLE customers"))

	sv := aianalysis.AnalyzeSession(context.Background(), a, stmts)
	if sv.RiskLevel != aianalysis.RiskHigh {
		t.Fatalf("session risk = %q, want high (one DROP among 50 reads)", sv.RiskLevel)
	}
	if len(sv.Findings) != 51 {
		t.Errorf("findings = %d, want 51", len(sv.Findings))
	}
	if !strings.Contains(sv.Explanation, "customers") {
		t.Errorf("session explanation does not name the dangerous statement: %q", sv.Explanation)
	}

	// Order must not matter: the DROP first is still high.
	reversed := append([]hoopinspect.Statement{sqlStmt("DROP TABLE customers")}, stmts[:50]...)
	if sv2 := aianalysis.AnalyzeSession(context.Background(), a, reversed); sv2.RiskLevel != aianalysis.RiskHigh {
		t.Errorf("reversed order risk = %q, want high", sv2.RiskLevel)
	}
}

func TestSessionRollupPicksHighestScoringFindingAtTopLevel(t *testing.T) {
	high1 := &aianalysis.Verdict{RiskLevel: aianalysis.RiskHigh, Title: "weaker", Explanation: "weaker high.", Score: 0.5}
	high2 := &aianalysis.Verdict{RiskLevel: aianalysis.RiskHigh, Title: "stronger", Explanation: "stronger high.", Score: 0.9}
	med := &aianalysis.Verdict{RiskLevel: aianalysis.RiskMedium, Title: "med", Explanation: "medium.", Score: 1.0}

	// The medium has the highest raw Score; level must still win.
	fa := &fixedAnalyzer{verdicts: []*aianalysis.Verdict{med, high1, high2}}
	sv := aianalysis.AnalyzeSession(context.Background(), fa, nStatements(3))

	if sv.RiskLevel != aianalysis.RiskHigh {
		t.Fatalf("risk = %q, want high", sv.RiskLevel)
	}
	if sv.Title != "stronger" {
		t.Errorf("title = %q, want the highest-scoring high finding", sv.Title)
	}
	if !strings.Contains(sv.Explanation, "1 other statement") {
		t.Errorf("explanation does not report the other high finding: %q", sv.Explanation)
	}
}

func TestSessionRollupSkipsNoOpinionAndErrors(t *testing.T) {
	boom := errors.New("model endpoint unreachable")
	high := &aianalysis.Verdict{RiskLevel: aianalysis.RiskHigh, Title: "t", Explanation: "e", Score: 1}
	low := &aianalysis.Verdict{RiskLevel: aianalysis.RiskLow, Title: "t", Explanation: "e", Score: 0.1}

	fa := &fixedAnalyzer{
		verdicts: []*aianalysis.Verdict{low, nil, nil, high},
		errs:     []error{nil, boom, nil, nil},
	}
	sv := aianalysis.AnalyzeSession(context.Background(), fa, nStatements(4))

	// Fail-open: the error did not abort the walk, so the later high-risk
	// statement was still scored.
	if fa.calls != 4 {
		t.Fatalf("analyzer called %d times, want 4 (an error must not abort the walk)", fa.calls)
	}
	if sv.RiskLevel != aianalysis.RiskHigh {
		t.Errorf("risk = %q, want high", sv.RiskLevel)
	}
	if len(sv.Findings) != 2 {
		t.Errorf("findings = %d, want 2 (the error and the no-opinion are skipped)", len(sv.Findings))
	}
}

func TestSessionRollupAllErrorsIsUnknownNotLow(t *testing.T) {
	boom := errors.New("nope")
	fa := &fixedAnalyzer{errs: []error{boom, boom, boom}}

	sv := aianalysis.AnalyzeSession(context.Background(), fa, nStatements(3))

	// Every analysis failed. Reporting "low" would claim a session was
	// checked and found fine when nothing was checked at all.
	if sv.RiskLevel != aianalysis.RiskUnknown {
		t.Errorf("risk = %q, want unknown when nothing could be analyzed", sv.RiskLevel)
	}
	if len(sv.Findings) != 0 {
		t.Errorf("findings = %d, want 0", len(sv.Findings))
	}
	if sv.Title != "" || sv.Explanation != "" {
		t.Errorf("summary invented text with no findings: %+v", sv)
	}
}

func TestAnalyzeSessionHandlesNilAnalyzerAndCancellation(t *testing.T) {
	if sv := aianalysis.AnalyzeSession(context.Background(), nil, nStatements(3)); sv.RiskLevel != aianalysis.RiskUnknown {
		t.Errorf("nil analyzer = %+v, want the zero verdict", sv)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fa := &fixedAnalyzer{}
	if sv := aianalysis.AnalyzeSession(ctx, fa, nStatements(5)); len(sv.Findings) != 0 {
		t.Errorf("cancelled context still produced findings: %+v", sv)
	}
	if fa.calls != 0 {
		t.Errorf("analyzer called %d times under a cancelled context, want 0", fa.calls)
	}
}

// --- recorder ------------------------------------------------------------

func TestRecorderStampsRiskMetadata(t *testing.T) {
	sink := audit.NewMemorySink(10)
	rec := aianalysis.NewRecorder(sink)

	s := session.New(hoopinspect.Postgres, session.Identity{Subject: "alice"})
	s.Connection = "prod-db"
	s.Metadata = map[string]string{"upstream": "db:5432"}

	v := aianalysis.Verdict{
		RiskLevel:   aianalysis.RiskHigh,
		Title:       "Unbounded DELETE",
		Explanation: "DELETE with no WHERE clause affects every row of orders.",
		Rule:        aianalysis.RuleUnboundedDelete,
	}
	if err := rec.Record(context.Background(), s, v); err != nil {
		t.Fatalf("Record: %v", err)
	}

	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]

	// No new Kind: an existing sink or query that switches on Kind must still
	// see this row.
	if ev.Kind != audit.KindStatement {
		t.Errorf("kind = %q, want %q", ev.Kind, audit.KindStatement)
	}
	// Analysis is advisory; it must never look like a denial.
	if !ev.Allowed {
		t.Error("event recorded as denied; analysis must never deny")
	}
	if ev.Rule != "" {
		t.Errorf("Event.Rule = %q; the analyzer rule belongs in metadata, not the policy-rule field", ev.Rule)
	}
	if ev.Principal != "alice" || ev.SessionID != s.ID || ev.Connection != "prod-db" {
		t.Errorf("event lost session identity: %+v", ev)
	}

	for key, want := range map[string]string{
		aianalysis.MetaRiskLevel: "high",
		aianalysis.MetaTitle:     "Unbounded DELETE",
		aianalysis.MetaRule:      aianalysis.RuleUnboundedDelete,
		"upstream":               "db:5432", // session metadata survives
	} {
		if got := ev.Metadata[key]; got != want {
			t.Errorf("metadata[%q] = %q, want %q", key, got, want)
		}
	}

	// The session's own map must not have been stamped: it is shared by every
	// later event of the session.
	if _, leaked := s.Metadata[aianalysis.MetaRiskLevel]; leaked {
		t.Error("Record mutated the session metadata map")
	}
}

func TestRecorderRejectsMissingSinkOrSession(t *testing.T) {
	if err := aianalysis.NewRecorder(nil).Record(context.Background(), session.New(hoopinspect.Postgres, session.Identity{}), aianalysis.Verdict{}); err == nil {
		t.Error("a recorder with no sink accepted a write")
	}
	if err := aianalysis.NewRecorder(audit.NewMemorySink(1)).Record(context.Background(), nil, aianalysis.Verdict{}); err == nil {
		t.Error("a nil session was accepted")
	}
}

func TestRecorderPropagatesSinkError(t *testing.T) {
	// A sink failure must reach the caller. Analysis does not deny on it, but
	// silently swallowing a write failure would hide a broken audit path.
	sink := audit.NewMemorySink(1)
	sink.Close()

	err := aianalysis.NewRecorder(sink).Record(context.Background(),
		session.New(hoopinspect.Postgres, session.Identity{Subject: "bob"}),
		aianalysis.Verdict{RiskLevel: aianalysis.RiskLow})
	if err == nil {
		t.Error("write to a closed sink reported success")
	}
}

func TestHeuristicAnalyzerIsConcurrencySafe(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{
		SensitiveTables: []string{"customers"},
		Patterns: []aianalysis.Pattern{{
			Name: "p", Regex: "nolock", RiskLevel: aianalysis.RiskLow, Explanation: "dirty read hint present.",
		}},
	})
	stmts := []hoopinspect.Statement{
		sqlStmt("DELETE FROM orders"),
		sqlStmt("SELECT * FROM customers"),
		sqlStmt("SELECT id FROM t WITH (NOLOCK) WHERE id = 1"),
	}

	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 50 {
				aianalysis.AnalyzeSession(context.Background(), a, stmts)
			}
		}()
	}
	for range 8 {
		<-done
	}
}

// Tables is best effort (see hoopinspect.ClassifySQL). An explanation that
// asserts "every row of orders" when the parser never found `orders` is a
// false statement in front of a person, so the wording must admit it.
func TestExplanationAdmitsUnknownTable(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	v := analyze(t, a, hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Text:      "DELETE",
		Operation: hoopinspect.OpDelete,
	})
	if v == nil || v.RiskLevel != aianalysis.RiskHigh {
		t.Fatalf("DELETE with no parseable table = %+v, want high", v)
	}
	if !strings.Contains(v.Explanation, "could not be determined") {
		t.Errorf("explanation asserts a table it never parsed: %q", v.Explanation)
	}

	// Multiple relations must all be named rather than silently dropped.
	multi := analyze(t, a, sqlStmt("UPDATE orders SET x = (SELECT 1 FROM audit_log)"))
	if multi == nil || multi.RiskLevel != aianalysis.RiskHigh {
		t.Fatalf("multi-table unbounded UPDATE = %+v, want high", multi)
	}
	for _, want := range []string{"orders", "audit_log"} {
		if !strings.Contains(multi.Explanation, want) {
			t.Errorf("explanation omits %q: %q", want, multi.Explanation)
		}
	}
}

func TestSessionSummaryPluralizesPeerCount(t *testing.T) {
	high := func(score float64) *aianalysis.Verdict {
		return &aianalysis.Verdict{RiskLevel: aianalysis.RiskHigh, Title: "t", Explanation: "e.", Score: score}
	}

	for _, tc := range []struct {
		verdicts []*aianalysis.Verdict
		want     string
	}{
		{[]*aianalysis.Verdict{high(0.9)}, ""},
		{[]*aianalysis.Verdict{high(0.9), high(0.5)}, "1 other statement at the same level"},
		{[]*aianalysis.Verdict{high(0.9), high(0.5), high(0.4)}, "2 other statements at the same level"},
	} {
		fa := &fixedAnalyzer{verdicts: tc.verdicts}
		sv := aianalysis.AnalyzeSession(context.Background(), fa, nStatements(len(tc.verdicts)))
		if tc.want == "" {
			if sv.Explanation != "e." {
				t.Errorf("single finding summary = %q, want the finding verbatim", sv.Explanation)
			}
			continue
		}
		if !strings.Contains(sv.Explanation, tc.want) {
			t.Errorf("summary = %q, want it to contain %q", sv.Explanation, tc.want)
		}
	}
}

func TestHTTPDeleteFallsBackToPathWhenResourceIsAbsent(t *testing.T) {
	// A codec that did not normalize the path still gets a verdict, and the
	// explanation names something the reader can find in the request log.
	a := newAnalyzer(t, aianalysis.HeuristicConfig{})

	v := analyze(t, a, hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Operation: hoopinspect.OpDelete,
		HTTP:      &hoopinspect.HTTPDetail{Method: "DELETE", Path: "/tenants"},
	})
	if v == nil || v.Rule != aianalysis.RuleHTTPUnboundedDel {
		t.Fatalf("DELETE with no Resource = %+v, want %s", v, aianalysis.RuleHTTPUnboundedDel)
	}
	if !strings.Contains(v.Explanation, "/tenants") {
		t.Errorf("explanation does not name the path: %q", v.Explanation)
	}

	// Neither Path nor Resource: no collection can be identified, so the
	// unbounded-delete claim would be unfounded.
	bare := analyze(t, a, hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Operation: hoopinspect.OpDelete,
		HTTP:      &hoopinspect.HTTPDetail{Method: "DELETE"},
	})
	if bare != nil && bare.Rule == aianalysis.RuleHTTPUnboundedDel {
		t.Errorf("an empty path was reported as an unbounded collection: %+v", bare)
	}
}

func TestPatternMatchIsCaseInsensitiveAndFirstWins(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{
		Patterns: []aianalysis.Pattern{
			{Name: "first", Regex: "ledger", RiskLevel: aianalysis.RiskMedium, Explanation: "touches the ledger."},
			{Name: "second", Regex: "ledger", RiskLevel: aianalysis.RiskHigh, Explanation: "also touches the ledger."},
		},
	})

	// Config order is precedence, matching policy.Rules: first match wins, so
	// a deployment can put a narrow exemption ahead of a broad rule.
	v := analyze(t, a, sqlStmt("SELECT id FROM LEDGER WHERE id = 1"))
	if v == nil || !strings.HasSuffix(v.Rule, "first") {
		t.Fatalf("pattern verdict = %+v, want the first configured pattern", v)
	}
	if v.RiskLevel != aianalysis.RiskMedium {
		t.Errorf("risk = %q, want the first pattern's medium", v.RiskLevel)
	}

	// A pattern that matches nothing must not produce a verdict of its own.
	none := analyze(t, a, sqlStmt("SELECT id FROM orders WHERE id = 1"))
	if none == nil || strings.HasPrefix(none.Rule, aianalysis.RuleCustomPattern) {
		t.Errorf("non-matching statement = %+v, want the routine verdict", none)
	}
}

// A low-risk explanation must describe what was actually checked. Telling a
// reader that an HTTP GET "touches no sensitive table" is a claim about a
// concept the request does not have.
func TestRoutineExplanationMatchesTheProtocol(t *testing.T) {
	a := newAnalyzer(t, aianalysis.HeuristicConfig{SensitiveTables: []string{"customers"}})

	sql := analyze(t, a, sqlStmt("INSERT INTO orders (id) VALUES (1)"))
	if sql == nil || sql.RiskLevel != aianalysis.RiskLow {
		t.Fatalf("routine INSERT = %+v, want low", sql)
	}
	if !strings.Contains(sql.Explanation, "table") {
		t.Errorf("SQL explanation does not mention tables: %q", sql.Explanation)
	}

	web := analyze(t, a, hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Operation: hoopinspect.OpGet,
		HTTP:      &hoopinspect.HTTPDetail{Method: "GET", Path: "/health", StatusCode: 200},
	})
	if web == nil || web.RiskLevel != aianalysis.RiskLow {
		t.Fatalf("routine GET = %+v, want low", web)
	}
	if strings.Contains(web.Explanation, "table") {
		t.Errorf("HTTP explanation claims something about tables: %q", web.Explanation)
	}
	if !strings.Contains(web.Explanation, "GET") {
		t.Errorf("HTTP explanation does not name the method: %q", web.Explanation)
	}
}
