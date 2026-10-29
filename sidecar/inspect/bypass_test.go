package inspect_test

import (
	"slices"
	"testing"

	_ "github.com/hoophq/hoop/sidecar/codec/all"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// Every case here defeated the previous classifier through the REAL wire
// path, not through ClassifySQL in isolation. The codec splits multi-statement
// query strings itself, so a bypass that survives here survived in production.
//
// This file is a permanent regression suite. A statement that lands in it
// stays in it: each row is a way somebody got a write past a policy that named
// the write.

// inspectOne runs sql through the postgres codec and returns the statements.
func inspectOne(t *testing.T, sql string) []inspect.Statement {
	t.Helper()
	insp, err := inspect.New(inspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stmts, err := insp.Inspect(inspect.FromClient, pgQuery(sql))
	if err != nil {
		t.Fatalf("Inspect(%q): %v", sql, err)
	}
	return stmts
}

// writesTo reports whether any statement in the batch writes to name.
func writesTo(stmts []inspect.Statement, name string) bool {
	for _, s := range stmts {
		for _, r := range s.Relations {
			if r.Name == name && r.Access == inspect.AccessWrite {
				return true
			}
		}
	}
	return false
}

func hasEffect(stmts []inspect.Statement, op inspect.Operation) bool {
	for _, s := range stmts {
		if s.Operation == op {
			return true
		}
		for _, e := range s.Effects {
			if e == op {
				return true
			}
		}
	}
	return false
}

// Each of these deletes from customers. A policy naming `delete`, or naming
// customers as a written relation, must fire on every one.
func TestBypassCorpusHidesNoWrites(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{
			// The worst of them: E'' honours backslash escapes in every
			// server configuration, so the previous scanner never closed
			// the literal, swallowed the semicolon, and reported one
			// harmless UPDATE. The DELETE was invisible to policy.
			"escape string hiding a second statement",
			`UPDATE audit SET note = E'O\'Brien'; DELETE FROM customers`,
		},
		{
			// The reported bug. The mutation lives one paren deep and a
			// depth counter discards it.
			"data-modifying CTE",
			`WITH doomed AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM doomed`,
		},
		{
			// A parenthesis inside an unstripped dollar-quote corrupted
			// the depth counter, and the `with` default then failed OPEN.
			"unbalanced paren inside a dollar-quoted string",
			`WITH x AS (SELECT $$a)b$$) DELETE FROM customers`,
		},
		{
			"unbalanced paren inside a tagged dollar quote",
			`WITH x AS (SELECT $tag$a(b$tag$) DELETE FROM customers`,
		},
		{
			// merge was absent from the verb table entirely.
			"MERGE with a conditional delete branch",
			`MERGE INTO customers c USING staging s ON c.id = s.id WHEN MATCHED THEN DELETE`,
		},
		{
			"COPY wrapping a data-modifying statement",
			`COPY (DELETE FROM customers RETURNING *) TO STDOUT`,
		},
		{
			// EXPLAIN ANALYZE executes. Plain EXPLAIN does not, and that
			// case is asserted separately below.
			"EXPLAIN ANALYZE executes",
			`EXPLAIN ANALYZE DELETE FROM customers`,
		},
		{
			"CTE named after a keyword",
			`WITH delete AS (SELECT 1) DELETE FROM customers`,
		},
		{
			// PostgreSQL ends a -- comment at a lone CR, and runs what
			// follows. Read to the next LF, the DELETE was a comment, and a
			// rule refusing delete let it through the postgres lane.
			"a carriage return ends a dash comment",
			"-- note\rDELETE FROM customers",
		},
		{
			"a carriage return ends a dash comment after a statement",
			"SELECT 1; -- note\rDELETE FROM customers",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stmts := inspectOne(t, tc.sql)
			if !hasEffect(stmts, inspect.OpDelete) {
				t.Errorf("no delete effect reported: %+v", stmts)
			}
			if !writesTo(stmts, "customers") {
				t.Errorf("customers not reported as written: %+v", stmts)
			}
		})
	}
}

// The inverse failure: a statement that touches nothing must not be denied,
// and a statement that only READS must not look like a write. Operators
// respond to false positives by widening rules until they protect nothing.
func TestBypassCorpusInventsNoWrites(t *testing.T) {
	for _, tc := range []struct {
		name, sql   string
		wantWrite   string
		wantRead    string
		wantNoWrite string
	}{
		{
			// Both engines NEST block comments. Stopping at the first
			// close read the tail of an outer comment as live SQL and
			// denied a harmless SELECT.
			name:        "nested block comment",
			sql:         `/* outer /* inner */ DELETE FROM customers */ SELECT 1`,
			wantNoWrite: "customers",
		},
		{
			// A plan is not an execution. Refusing this blocks the
			// command a developer uses to check their WHERE clause.
			name:        "EXPLAIN without ANALYZE",
			sql:         `EXPLAIN DELETE FROM customers`,
			wantRead:    "customers",
			wantNoWrite: "customers",
		},
		{
			name:      "read in a subquery is not a write",
			sql:       `INSERT INTO staging SELECT * FROM customers`,
			wantWrite: "staging",
			wantRead:  "customers",
		},
		{
			name:      "USING is a source, FROM is the target",
			sql:       `DELETE FROM sessions USING customers WHERE sessions.uid = customers.id`,
			wantWrite: "sessions",
			wantRead:  "customers",
		},
		{
			// A function DEFINITION performs one effect, a create. The
			// body is data until something calls it.
			name:        "function body is data",
			sql:         `CREATE FUNCTION f() RETURNS void AS $$ DELETE FROM customers $$ LANGUAGE plpgsql`,
			wantNoWrite: "customers",
		},
		{
			name:        "literal containing SQL",
			sql:         `SELECT 'DROP TABLE customers' AS msg FROM notes`,
			wantRead:    "notes",
			wantNoWrite: "customers",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stmts := inspectOne(t, tc.sql)
			if tc.wantWrite != "" && !writesTo(stmts, tc.wantWrite) {
				t.Errorf("%s not reported as written: %+v", tc.wantWrite, stmts)
			}
			if tc.wantNoWrite != "" && writesTo(stmts, tc.wantNoWrite) {
				t.Errorf("%s reported as written, but nothing writes it: %+v",
					tc.wantNoWrite, stmts)
			}
			if tc.wantRead == "" {
				return
			}
			var found bool
			for _, s := range stmts {
				for _, r := range s.Relations {
					if r.Name == tc.wantRead && r.Access == inspect.AccessRead {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("%s not reported as read: %+v", tc.wantRead, stmts)
			}
		})
	}
}

// The scanner names three verbs the Operation vocabulary does not: merge,
// copy and explain. They used to reach a rule verbatim, and because
// MatchOperation and the AI trigger compare for equality, a read-only
// credential naming insert/update/delete let all three through.
//
// These are the statements that got past it. Each must now be denied by the
// rule that names its real consequence, through the real wire path.
func TestFoldedVerbsReachTheRuleThatNamesThem(t *testing.T) {
	readOnly := rulesFor(t, "no-writes",
		inspect.OpInsert, inspect.OpUpdate, inspect.OpDelete)
	noExport := rulesFor(t, "no-bulk-read", inspect.OpSelect)

	for _, tc := range []struct {
		name, sql  string
		rules      *policy.Rules
		wantDenied bool
	}{
		{
			// MERGE's update branch reported `merge`, which nothing named.
			name:       "MERGE with an update branch is a write",
			sql:        `MERGE INTO customers c USING staging s ON c.id = s.id WHEN MATCHED THEN UPDATE SET name = s.name`,
			rules:      readOnly,
			wantDenied: true,
		},
		{
			name:       "MERGE with an insert branch is a write",
			sql:        `MERGE INTO customers c USING staging s ON c.id = s.id WHEN NOT MATCHED THEN INSERT VALUES (s.id, s.name)`,
			rules:      readOnly,
			wantDenied: true,
		},
		{
			// A bulk load reported `copy`. It is an INSERT of every row
			// the client can stream.
			name:       "COPY FROM is a bulk insert",
			sql:        `COPY customers FROM STDIN`,
			rules:      readOnly,
			wantDenied: true,
		},
		{
			// The other direction is the exfiltration path, and it must
			// not be mistaken for a write.
			name:       "COPY TO is a bulk read",
			sql:        `COPY customers TO STDOUT`,
			rules:      noExport,
			wantDenied: true,
		},
		{
			name:       "COPY TO writes nothing",
			sql:        `COPY customers TO STDOUT`,
			rules:      readOnly,
			wantDenied: false,
		},
		{
			// A plan executes nothing, so a write rule must stay quiet.
			name:       "EXPLAIN without ANALYZE writes nothing",
			sql:        `EXPLAIN DELETE FROM customers`,
			rules:      readOnly,
			wantDenied: false,
		},
		{
			// It still reads the relation, and Relations says so, so the
			// operation it reports has to agree.
			name:       "EXPLAIN reads the relations it names",
			sql:        `EXPLAIN DELETE FROM customers`,
			rules:      noExport,
			wantDenied: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stmts := inspectOne(t, tc.sql)
			if len(stmts) == 0 {
				t.Fatalf("no statements for %q", tc.sql)
			}
			var denied bool
			for _, s := range stmts {
				if tc.rules.Evaluate(s).Denied {
					denied = true
				}
			}
			if denied != tc.wantDenied {
				t.Errorf("denied = %v, want %v: %+v", denied, tc.wantDenied, stmts)
			}
		})
	}
}

// The false positive that took a read-only lane down, through the real wire
// path. A reserved word is a legal column alias when AS is written, and the
// scanner read `... AS delete` as a statement head, so a catalog SELECT was
// denied as a delete.
//
// The statement is Metabase's, trimmed: its schema sync asks which privileges
// it holds and names each column after the privilege it tested. It ran on
// every table, so the sync failed outright and the product reported an empty
// database. Any BI tool introspecting privileges writes some version of it.
//
// This belongs beside the bypasses rather than in lexer_test because the
// consequence is a policy verdict, and a verdict is what an operator sees.
func TestPrivilegeIntrospectionIsNotAWrite(t *testing.T) {
	readOnly := rulesFor(t, "no-writes",
		inspect.OpInsert, inspect.OpUpdate, inspect.OpDelete)

	const sql = `with table_privileges as (
  select
    t.schemaname as schema,
    t.objectname as table,
    pg_catalog.has_any_column_privilege(current_user, t.objectname, 'update') as update,
    pg_catalog.has_any_column_privilege(current_user, t.objectname, 'select') as select,
    pg_catalog.has_any_column_privilege(current_user, t.objectname, 'insert') as insert,
    pg_catalog.has_table_privilege(current_user, t.objectname, 'delete') as delete
  from (
    select schemaname, tablename as objectname from pg_catalog.pg_tables
    union
    select schemaname, viewname as objectname from pg_catalog.pg_views
  ) t
  where t.schemaname !~ '^pg_'
)
select t.* from table_privileges t`

	stmts := inspectOne(t, sql)
	if len(stmts) == 0 {
		t.Fatal("no statements produced")
	}
	for _, s := range stmts {
		if v := readOnly.Evaluate(s); v.Denied {
			t.Errorf("a catalog read was denied as %q by %q", s.Operation, v.Rule)
		}
		if s.Operation != inspect.OpSelect {
			t.Errorf("Operation = %q, want select", s.Operation)
		}
	}
	if writesTo(stmts, "pg_tables") || writesTo(stmts, "pg_views") {
		t.Errorf("catalog relations reported as written: %+v", stmts)
	}
}

// A rule that fences reads of salaries refuses every select of it, whatever
// its columns are called and wherever it stands in a FROM list. A column or
// alias spelling update, into or another relation introducer took the FROM
// after it for the relation, and an aliased item ended the FROM list, so the
// select passed while `SELECT s.amount FROM salaries s` was refused (ENG-859).
func TestReadFenceRefusesSelectsOfKeywordColumns(t *testing.T) {
	fence, err := policy.NewRules([]policy.Rule{{
		Name: "salaries-is-hidden", Type: policy.MatchTable, Tables: []string{"salaries"},
		Access: string(inspect.AccessRead), Message: "salaries is hidden",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	for _, sql := range []string{
		`SELECT s.amount FROM salaries s`,
		`SELECT s.update FROM salaries s`,
		`SELECT s.into FROM salaries s`,
		`SELECT s.update FROM (SELECT amount AS update FROM salaries) s`,
		`SELECT x.into FROM (SELECT * FROM salaries) x`,
		`SELECT amount AS update FROM salaries`,
		`SELECT update FROM salaries`,
		`SELECT * FROM t update JOIN salaries ON true`,
		`SELECT x.update FROM (VALUES (1)) x(update) JOIN salaries ON true`,
		`SELECT x.update FROM (VALUES (1)) x(update), salaries`,
		`WITH s AS (SELECT amount AS update FROM salaries) SELECT s.update FROM s`,
		`WITH s AS (SELECT 1 AS update) SELECT s.update, salaries.* FROM s JOIN salaries ON true`,
		`WITH s AS (SELECT 1 AS update) SELECT s.update, salaries.* FROM s, salaries`,
		// A FROM list continues past an aliased item, a subquery and a
		// function call.
		`SELECT * FROM t x, salaries`,
		`SELECT * FROM (SELECT 1) x, salaries`,
		`SELECT * FROM generate_series(1, 2) g, salaries`,
		// Spark does not reserve AS: here it is a column, and FROM is real.
		`SELECT as FROM salaries`,
	} {
		t.Run(sql, func(t *testing.T) {
			denied := false
			for _, s := range inspectOne(t, sql) {
				denied = denied || fence.Evaluate(s).Denied
			}
			if !denied {
				t.Errorf("postgres wire path: a read fence on salaries passed %s", sql)
			}
			for _, d := range []lexer.Dialect{lexer.MSSQL, lexer.MySQL, lexer.ClickHouse,
				lexer.GoogleSQL, lexer.Oracle, lexer.Spark} {
				a := inspect.AnalyzeSQLIn(sql, d)
				stmt := inspect.Statement{
					Direction: inspect.FromClient, Text: sql,
					Operation: a.Operation, Effects: a.Effects, Relations: a.Relations, Tables: a.Tables,
				}
				if !fence.Evaluate(stmt).Denied {
					t.Errorf("%s: a read fence on salaries passed, relations = %v", d, a.Relations)
				}
			}
		})
	}
}

// A read-only lane is one operation rule naming every write, the rule the
// GCP Marketplace read-only preset and the agents-cli template ship. These
// statements got past it and changed the database (ENG-803): the EXPLAIN
// forms ran their DELETE and were audited as an allowed select, and
// SELECT ... INTO created its table the same way, in parentheses as an
// allowed unknown. ANALYSE ran, while the rule refuses ANALYZE as other.
//
// The plans stay allowed. EXPLAIN without ANALYZE, or with ANALYZE false,
// runs nothing, and a quoted FORMAT value is not the ANALYZE option.
func TestReadOnlyRuleRefusesWritesBehindExplainAndSelectInto(t *testing.T) {
	readOnly := rulesFor(t, "read-only",
		inspect.OpInsert, inspect.OpUpdate, inspect.OpDelete, inspect.OpCreate,
		inspect.OpDrop, inspect.OpAlter, inspect.OpTruncate, inspect.OpGrant,
		inspect.OpRevoke, inspect.OpCall, inspect.OpOther)

	for _, tc := range []struct {
		name, sql  string
		wantDenied bool
		wantOp     inspect.Operation
	}{
		{"EXPLAIN ANALYSE, the British spelling, is refused as the delete it runs",
			`EXPLAIN ANALYSE DELETE FROM customers WHERE id = 2`, true, inspect.OpDelete},
		{"EXPLAIN (ANALYSE) is refused as the delete it runs",
			`EXPLAIN (ANALYSE) DELETE FROM customers WHERE id = 2`, true, inspect.OpDelete},
		{"a quoted analyze option is refused as the delete it runs",
			`EXPLAIN ("analyze") DELETE FROM customers WHERE id = 3`, true, inspect.OpDelete},
		{"a quoted analyze option after another option is refused as the delete it runs",
			`EXPLAIN (FORMAT JSON, "analyze") DELETE FROM customers WHERE id = 4`, true, inspect.OpDelete},
		{"SELECT INTO is refused as the create it is",
			`SELECT * INTO customers_copy FROM customers`, true, inspect.OpCreate},
		{"SELECT INTO TEMP is refused as the create it is",
			`SELECT * INTO TEMP t1 FROM customers`, true, inspect.OpCreate},
		{"SELECT INTO in parentheses is refused as the create it is",
			`(SELECT * INTO t6 FROM customers)`, true, inspect.OpCreate},
		{"SELECT INTO in two pairs of parentheses is refused as the create it is",
			`((SELECT * INTO t30 FROM customers))`, true, inspect.OpCreate},
		{"ANALYSE, the British spelling, is refused as ANALYZE is",
			`ANALYSE customers`, true, inspect.OpOther},
		{"a second ANALYZE turned off does not hide the first",
			`EXPLAIN (ANALYZE, ANALYZE false) DELETE FROM customers`, true, inspect.OpDelete},

		{"EXPLAIN without ANALYZE passes", `EXPLAIN DELETE FROM customers`, false, ""},
		{"EXPLAIN with plan options passes", `EXPLAIN (VERBOSE, FORMAT JSON) DELETE FROM customers`, false, ""},
		{"a quoted FORMAT value passes", `EXPLAIN (FORMAT "json") DELETE FROM customers`, false, ""},
		{"EXPLAIN with ANALYZE turned off passes", `EXPLAIN (ANALYZE false) DELETE FROM customers`, false, ""},
		{"a quoted analyze option turned off passes", `EXPLAIN ("analyze" off) DELETE FROM customers`, false, ""},
		{"a plain SELECT passes", `SELECT * FROM customers`, false, ""},
		{"a SELECT in parentheses passes as a select", `(SELECT * FROM customers)`, false, inspect.OpSelect},
		{"a UNION of parenthesized SELECTs passes", `(SELECT id FROM customers) UNION (SELECT id FROM orders)`, false, inspect.OpSelect},
		// A privilege table names its columns after the statements, and a
		// CASE branch in parentheses reads them. They are expressions.
		{"a parenthesized column called update in a CASE passes",
			`SELECT name, CASE WHEN owner THEN true ELSE (update) END AS can_update FROM privs`, false, inspect.OpSelect},
		{"a parenthesized column called delete in a CASE passes",
			`SELECT name, CASE WHEN owner THEN (delete) ELSE false END AS can_delete FROM privs`, false, inspect.OpSelect},
		{"a parenthesized expression of columns called insert and update passes",
			`SELECT CASE WHEN owner THEN true ELSE (insert OR update) END FROM privs`, false, inspect.OpSelect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stmts := inspectOne(t, tc.sql)
			if len(stmts) == 0 {
				t.Fatalf("no statements for %q", tc.sql)
			}
			for _, s := range stmts {
				if denied := readOnly.Evaluate(s).Denied; denied != tc.wantDenied {
					t.Errorf("denied = %v, want %v: operation %q, effects %v, relations %v",
						denied, tc.wantDenied, s.Operation, s.Effects, s.Relations)
				}
				if tc.wantOp != "" && s.Operation != tc.wantOp {
					t.Errorf("Operation = %q, want %q", s.Operation, tc.wantOp)
				}
			}
		})
	}
}

// rulesFor builds a single MatchOperation deny rule.
func rulesFor(t *testing.T, name string, ops ...inspect.Operation) *policy.Rules {
	t.Helper()
	rules, err := policy.NewRules([]policy.Rule{{
		Name: name, Type: policy.MatchOperation, Operations: ops, Message: name,
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	return rules
}

// Nothing may leave AnalyzeSQL that a config cannot name. The previous code
// passed the scanner's verb straight through, so widening the scanner's
// vocabulary silently widened Operation's and every equality match went stale.
func TestOperationVocabularyIsClosed(t *testing.T) {
	known := map[inspect.Operation]bool{}
	for _, op := range []inspect.Operation{
		inspect.OpSelect, inspect.OpInsert, inspect.OpUpdate,
		inspect.OpDelete, inspect.OpCreate, inspect.OpDrop,
		inspect.OpAlter, inspect.OpTruncate, inspect.OpGrant,
		inspect.OpRevoke, inspect.OpCall, inspect.OpShow,
		inspect.OpSet, inspect.OpBegin, inspect.OpCommit,
		inspect.OpRollback, inspect.OpOther, inspect.OpUnknown,
	} {
		known[op] = true
	}

	for _, sql := range []string{
		`SELECT 1`, `TABLE customers`, `VALUES (1)`,
		`INSERT INTO t VALUES (1)`, `UPDATE t SET a = 1`, `DELETE FROM t`,
		`MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET a = 1`,
		`CREATE TABLE t (a int)`, `DROP TABLE t`, `ALTER TABLE t ADD b int`,
		`TRUNCATE t`, `GRANT SELECT ON t TO alice`, `REVOKE SELECT ON t FROM alice`,
		`CALL purge()`, `DO $$ BEGIN END $$`, `EXECUTE p`,
		`COPY t FROM STDIN`, `COPY t TO STDOUT`,
		`SHOW search_path`, `SET work_mem = '1MB'`, `RESET work_mem`,
		`BEGIN`, `START TRANSACTION`, `COMMIT`, `END`, `ROLLBACK`, `ABORT`,
		`EXPLAIN SELECT 1`, `EXPLAIN ANALYZE DELETE FROM t`,
		`SAVEPOINT a`, `VACUUM`, `ANALYZE t`, `LOCK TABLE t`, `DECLARE c CURSOR FOR SELECT 1`,
		`WITH x AS (DELETE FROM t RETURNING *) SELECT count(*) FROM x`,
		``, `not sql at all`,
	} {
		a := inspect.AnalyzeSQL(sql, inspect.Postgres)
		if !known[a.Operation] {
			t.Errorf("Operation = %q, outside the vocabulary: %s", a.Operation, sql)
		}
		for _, e := range a.Effects {
			if !known[e] {
				t.Errorf("effect = %q, outside the vocabulary: %s", e, sql)
			}
		}
	}
}

// A statement the scanner cannot read must come back as OpUnknown with a
// reason, so `operations: [unknown]` refuses it and an operator can tell a
// stored procedure from a malformed literal.
func TestUnreadableStatementsFailClosed(t *testing.T) {
	for _, sql := range []string{
		`DO $$ BEGIN DELETE FROM customers; END $$`,
		`CALL purge_everything()`,
		`EXECUTE prepared_delete`,
		`SELECT * FROM customers WHERE note = 'unterminated`,
	} {
		stmts := inspectOne(t, sql)
		if len(stmts) == 0 {
			t.Fatalf("no statements for %q", sql)
		}
		for _, s := range stmts {
			if s.Operation != inspect.OpUnknown {
				t.Errorf("Operation = %q, want unknown: %s", s.Operation, sql)
			}
			if s.Metadata[inspect.MetadataSQLIncomplete] == "" {
				t.Errorf("no %s metadata: %s", inspect.MetadataSQLIncomplete, sql)
			}
		}
	}
}

// The multi-statement split must survive every quoting form, or a `;` inside
// a literal ends one statement early and the rest becomes invisible.
func TestStatementSplitSurvivesQuoting(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want int
	}{
		{`SELECT 1; DELETE FROM customers`, 2},
		{`SELECT 'a;b'; DELETE FROM customers`, 2},
		{`SELECT $$a;b$$; DELETE FROM customers`, 2},
		{`SELECT E'a\';b'; DELETE FROM customers`, 2},
		{`SELECT 1 -- ; not a statement` + "\n" + `; DELETE FROM customers`, 2},
	} {
		stmts := inspectOne(t, tc.sql)
		if len(stmts) != tc.want {
			t.Errorf("got %d statements, want %d: %s", len(stmts), tc.want, tc.sql)
		}
		if !writesTo(stmts, "customers") {
			t.Errorf("the trailing delete was lost: %s", tc.sql)
		}
	}
}

// AnalyzeSQL must hand the lexer the dialect the protocol implies. Passing
// mysql traffic through the Postgres rules is not a cosmetic mismatch: each
// case below is a statement the wrong dialect reads as something SAFER than
// it is, which is a denial that never fires.
func TestMySQLIsAnalyzedWithTheMySQLDialect(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		why  string
		want inspect.Operation
	}{
		// MySQL reads `--` glued to a token as two minus signs, so the
		// delete is a second live statement. The Postgres rule comments
		// it out and the delete is never analyzed at all.
		{`SELECT 1--2; DELETE FROM customers`, "glued dash comment", inspect.OpDelete},
		// Block comments do not nest in MySQL, so the comment ends at the
		// first `*/` and the delete is live SQL.
		{`/* a /* b */ DELETE FROM customers */`, "non-nesting block comment", inspect.OpDelete},
	} {
		if got := inspect.AnalyzeSQL(tc.sql, inspect.MySQL).Operation; got != tc.want {
			t.Errorf("%s: op = %q, want %q: %s", tc.why, got, tc.want, tc.sql)
		}
	}

	// Backticks are MySQL's only quoted identifier. Scanned as stray
	// punctuation the delete is reported with no target, and a rule naming
	// that table never matches.
	a := inspect.AnalyzeSQL("DELETE FROM `orders`", inspect.MySQL)
	if a.Operation != inspect.OpDelete || !slices.Contains(a.Tables, "orders") {
		t.Errorf("backtick identifier: op = %q, tables = %v", a.Operation, a.Tables)
	}
}

// ClickHouse has the same policy consequence as MySQL: applying PostgreSQL
// lexical rules either hides live writes or invents writes from comments and
// string data. AnalyzeSQL must select the dialect from the native protocol.
func TestClickHouseIsAnalyzedWithTheClickHouseDialect(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		why  string
		want inspect.Operation
	}{
		{`/* a /* b */ DELETE FROM customers */`, "non-nesting block comment", inspect.OpDelete},
		{`SELECT 1 # DELETE FROM customers`, "hash comment", inspect.OpSelect},
		{`SELECT 'a\'; DELETE FROM customers; --'`, "backslash-escaped string", inspect.OpSelect},
	} {
		if got := inspect.AnalyzeSQL(tc.sql, inspect.ClickHouse).Operation; got != tc.want {
			t.Errorf("%s: op = %q, want %q: %s", tc.why, got, tc.want, tc.sql)
		}
	}

	a := inspect.AnalyzeSQL("DELETE FROM `orders`", inspect.ClickHouse)
	if a.Operation != inspect.OpDelete || !slices.Contains(a.Tables, "orders") {
		t.Errorf("backtick identifier: op = %q, tables = %v", a.Operation, a.Tables)
	}
}

// The oracle lane speaks Oracle SQL and PL/SQL. Read as PostgreSQL, BEGIN
// opens a transaction, so a PL/SQL block hid its DELETE from a rule naming
// delete, and the END that closes a block read as a COMMIT.
func TestOracleIsAnalyzedWithTheOracleDialect(t *testing.T) {
	a := inspect.AnalyzeSQL(`BEGIN DELETE FROM customers; END;`, inspect.Oracle)
	if !slices.Contains(a.Effects, inspect.OpDelete) || slices.Contains(a.Effects, inspect.OpBegin) || a.Complete {
		t.Errorf("PL/SQL block: op = %q, effects = %v, complete = %v", a.Operation, a.Effects, a.Complete)
	}
	if got := inspect.AnalyzeSQL(`SELECT q'[']' FROM dual; DELETE FROM customers; --'`, inspect.Oracle); !slices.Contains(got.Effects, inspect.OpDelete) {
		t.Errorf("q-quote swallowed the DELETE: effects = %v", got.Effects)
	}
}

// The spanner lane speaks GoogleSQL, and AnalyzeSQL must select that
// dialect for inspect.Spanner. Each case is phrased as the verdict an
// operator gets: the first hides a live DELETE that a wrong-dialect read
// swallows into a literal — a rule naming `delete` or customers-as-written
// never fires and the statement executes unseen. The rest are the inverse:
// string data and hint syntax that a wrong-dialect read classifies as
// something it is not, which denies a harmless select and trains the
// operator to widen the rule.
func TestSpannerIsAnalyzedWithTheGoogleSQLDialect(t *testing.T) {
	// In r'...' the backslash is an ordinary byte, so the literal closes
	// at the first quote and the DELETE after it is a live statement. A
	// lexer honouring the escape swallows the terminator and reports one
	// select; the delete reaches Spanner having never been evaluated.
	raw := inspect.AnalyzeSQL(`SELECT r'\'; DELETE FROM customers; --'`, inspect.Spanner)
	if raw.Operation != inspect.OpDelete {
		t.Errorf("raw string: op = %q, want %q; the DELETE was hidden in a phantom literal", raw.Operation, inspect.OpDelete)
	}
	if !slices.Contains(raw.Tables, "customers") {
		t.Errorf("raw string: tables = %v, want customers; the written relation was lost", raw.Tables)
	}

	// The false-positive direction: DELETEs that are string DATA. A
	// triple-quoted body may contain bare quotes, and '"' delimits a
	// string in GoogleSQL, not an identifier — a scanner without either
	// rule classifies literal content and refuses a select that touches
	// nothing.
	for _, tc := range []struct {
		sql string
		why string
	}{
		{`SELECT '''; DELETE FROM customers; '''`, "delete inside a triple-quoted string"},
		{`SELECT "a\";DELETE FROM customers;--"`, "delete inside a double-quoted string"},
	} {
		a := inspect.AnalyzeSQL(tc.sql, inspect.Spanner)
		if a.Operation != inspect.OpSelect {
			t.Errorf("%s: op = %q, want %q: %s", tc.why, a.Operation, inspect.OpSelect, tc.sql)
		}
		if len(a.Tables) != 0 {
			t.Errorf("%s: tables = %v, want none; literal content leaked a relation: %s", tc.why, a.Tables, tc.sql)
		}
	}

	// Hints are optimizer advice. Glued to a relation the hint must not
	// eat the name — a `tables: [t]` rule that stops matching is a
	// guardrail that stops guarding — and before the statement it must
	// not cost the SELECT its classification.
	glued := inspect.AnalyzeSQL("SELECT x FROM t@{FORCE_INDEX=idx}", inspect.Spanner)
	if glued.Operation != inspect.OpSelect || !slices.Contains(glued.Tables, "t") {
		t.Errorf("hint-glued relation: op = %q, tables = %v, want select on t", glued.Operation, glued.Tables)
	}
	stmt := inspect.AnalyzeSQL("@{USE_ADDITIONAL_PARALLELISM=TRUE} SELECT x FROM t", inspect.Spanner)
	if stmt.Operation != inspect.OpSelect || !slices.Contains(stmt.Tables, "t") {
		t.Errorf("statement-level hint: op = %q, tables = %v, want select on t", stmt.Operation, stmt.Tables)
	}
}

// Databricks SQL is Spark SQL, and a lane reads it with AnalyzeSQLIn and
// lexer.Spark. Each case is named for the verdict an operator gets.
//
// The refusals ran on a Databricks SQL warehouse (2026.42) and dropped,
// vacuumed or rewrote a table there, except the flat-comment VACUUM, which
// only open-source Delta Lake reads that way. Under every dialect that existed
// before Spark, each one read as a select, a begin, an `other` or nothing,
// so a guardrail refusing writes let it through. The passes are the inverse:
// reads that a wrong reading refuses, which costs an operator their tool.
func TestSparkIsAnalyzedWithTheSparkDialect(t *testing.T) {
	noWrites := rulesFor(t, "no-writes",
		inspect.OpInsert, inspect.OpUpdate, inspect.OpDelete, inspect.OpDrop,
		inspect.OpAlter, inspect.OpCreate, inspect.OpTruncate)
	unreadable := rulesFor(t, "unreadable", inspect.OpUnknown)
	fenceT, err := policy.NewRules([]policy.Rule{{
		Name: "t-is-read-only", Type: policy.MatchTable, Tables: []string{"t"},
		Access: string(inspect.AccessWrite), Message: "t is read-only",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}
	fenceReadT, err := policy.NewRules([]policy.Rule{{
		Name: "t-is-hidden", Type: policy.MatchTable, Tables: []string{"t"},
		Access: string(inspect.AccessRead), Message: "t is hidden",
	}})
	if err != nil {
		t.Fatalf("NewRules: %v", err)
	}

	for _, tc := range []struct {
		name, sql  string
		rules      *policy.Rules
		wantDenied bool
	}{
		{"a DROP after a backslash-continued comment is refused", "-- x \\\nSELECT '\nDROP TABLE t -- '", noWrites, true},
		// \xc2\xa0 is U+00A0, a no-break space: whitespace to Spark.
		{"a DROP joined to TABLE by a no-break space is refused", "DROP\xc2\xa0TABLE t", noWrites, true},
		{"a DROP inside a BEGIN ... END script is refused", "BEGIN DROP TABLE t; END", noWrites, true},
		{"a DROP after a nested comment is refused", "/* /* */ SELECT 'x */ DROP TABLE t; -- '", noWrites, true},
		{"a DROP behind a string in a hint is refused", "BEGIN SELECT /*+ X('*/') */ 1; DROP TABLE t; END; --'", noWrites, true},
		{"a VACUUM behind Delta's flat comment is refused", "/* /* */ VACUUM t RETAIN 0 HOURS -- */", noWrites, true},
		{"a DROP in a script whose first label is work is refused", "BEGIN work: BEGIN DROP TABLE t; END work; END", noWrites, true},
		{"a REPLACE TABLE in a handler is refused", "BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION REPLACE TABLE t AS SELECT 1; SELECT 1/0; END", noWrites, true},
		{"a DROP after a hint opener inside a comment is refused", "/* /*+ */ DROP TABLE t -- */", noWrites, true},
		{"a DROP after a dash comment ended by a carriage return is refused", "-- x\rDROP TABLE t", noWrites, true},
		{"dynamic SQL only released Spark reads is refused as unreadable", "/* /*+ */ EXECUTE IMMEDIATE 'DROP TABLE t' -- */", unreadable, true},
		{"GENERATE is refused as an alter", "GENERATE symlink_format_manifest FOR TABLE t", noWrites, true},
		{"DESCRIBE TABLE EXTENDED is refused by a rule fencing reads of its table", "DESCRIBE TABLE EXTENDED t", fenceReadT, true},
		{"table_changes is refused by a rule fencing reads of its table", "SELECT * FROM table_changes('t', 1)", fenceReadT, true},
		{"a qualified table_changes is refused by a rule fencing reads of its table", "SELECT * FROM system.builtin.table_changes('t', 1)", fenceReadT, true},
		{"table_changes over an expression is refused as unreadable", "SELECT * FROM table_changes(concat('t', ''), 1)", unreadable, true},
		{"a column aliased overwrite is refused by a rule fencing reads of its table", "SELECT id overwrite FROM t", fenceReadT, true},
		{"EXPLAIN INSERT OVERWRITE is refused by a rule fencing reads of its table", "EXPLAIN INSERT OVERWRITE t SELECT 1", fenceReadT, true},
		{"a DROP in an IF inside a handler's block is refused", "BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN IF true THEN DROP TABLE t; END IF; END; SELECT 1/0; END", noWrites, true},
		{"a DELETE in a CASE statement inside a handler's block is refused by a rule fencing its target", "BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN CASE WHEN true THEN DELETE FROM t; END CASE; END; SELECT 1/0; END", fenceT, true},
		{"a DROP in a CASE statement on a variable called case is refused", "BEGIN DECLARE case INT DEFAULT 1; CASE case WHEN 1 THEN DROP TABLE t; END CASE; END", noWrites, true},
		{"a DELETE in a FOR loop over a column called case is refused by a rule fencing its target", "BEGIN FOR r AS SELECT case FROM s DO CASE WHEN r.case = 1 THEN DELETE FROM t; END CASE; END FOR; END", fenceT, true},
		{"VACUUM is refused as a delete", "VACUUM t RETAIN 0 HOURS", noWrites, true},
		{"OPTIMIZE is refused as an alter", "OPTIMIZE t ZORDER BY (a)", noWrites, true},
		{"RESTORE is refused as an alter", "RESTORE TABLE t TO VERSION AS OF 1", noWrites, true},
		{"REPLACE TABLE is refused as a create", "REPLACE TABLE t AS SELECT * FROM s", noWrites, true},
		{"COPY INTO is refused as an insert", "COPY INTO t FROM 's3://b/p' FILEFORMAT = CSV", noWrites, true},
		{"a FROM-first INSERT is refused", "FROM s INSERT INTO t SELECT a", noWrites, true},
		{"INSERT OVERWRITE is refused by a rule fencing its target", "INSERT OVERWRITE t SELECT * FROM s", fenceT, true},
		{"INSERT WITH SCHEMA EVOLUTION OVERWRITE is refused by a rule fencing its target", "INSERT WITH SCHEMA EVOLUTION OVERWRITE t SELECT * FROM s", fenceT, true},
		{"COPY INTO is refused by a rule fencing its target", "COPY INTO t FROM 's3://b/p' FILEFORMAT = CSV", fenceT, true},
		{"VACUUM is refused by a rule fencing its target", "VACUUM t", fenceT, true},
		{"VACUUM of a table called over is refused as a delete", "VACUUM over RETAIN 0 HOURS", noWrites, true},
		{"a substituted variable is refused as unreadable", "SELECT * FROM ${tbl}", unreadable, true},
		{"a script is refused as unreadable", "BEGIN SELECT * FROM t; END", unreadable, true},

		{"a read-only script passes a guardrail refusing writes", "BEGIN SELECT * FROM t; END", noWrites, false},
		{"SHOW CREATE TABLE in a script passes", "BEGIN SHOW CREATE TABLE t; END", noWrites, false},
		{"a column called load in a script passes", "BEGIN SELECT load FROM metrics; END", noWrites, false},
		{"a column called generate in a script passes", "BEGIN SELECT generate FROM t; END", noWrites, false},
		{"a GENERATE in a handler is refused", "BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION GENERATE symlink_format_manifest FOR TABLE t; SELECT 1/0; END", noWrites, true},
		{"a MERGE join hint in a script passes", "BEGIN SELECT /*+ MERGE */ * FROM a JOIN b ON a.id = b.id; END", noWrites, false},
		{"a CASE that calls replace() passes", "SELECT CASE WHEN a = 1 THEN replace(s, 'x', 'y') ELSE s END AS r FROM t", noWrites, false},
		{"a CASE that returns a column called load passes", "SELECT CASE WHEN a = 1 THEN load ELSE 0 END FROM t", noWrites, false},
		{"a parenthesized query that calls replace() passes", "(SELECT CASE WHEN a = 1 THEN replace(s, 'x', 'y') END FROM t)", noWrites, false},
		{"a CASE in an IF condition that calls replace() passes", "BEGIN IF CASE WHEN a THEN replace(s, 'x', 'y') ELSE s END = 'z' THEN SELECT 1; END IF; END", noWrites, false},
		{"every MERGE clause still counts", "MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET a = s.a WHEN NOT MATCHED BY SOURCE THEN DELETE", rulesFor(t, "no-deletes", inspect.OpDelete), true},
		{"a MERGE join hint passes", "SELECT /*+ MERGE(t, s) */ * FROM t JOIN s ON t.id = s.id", noWrites, false},
		{"a DELETE in a double-quoted string passes", `SELECT "a\"; DELETE FROM t; --"`, noWrites, false},
		{"a DELETE after a continued comment passes", "SELECT 1 -- note \\\n; DELETE FROM t", noWrites, false},
		{"DESCRIBE passes", "DESCRIBE TABLE t", noWrites, false},
		{"BEGIN TRANSACTION passes", "BEGIN TRANSACTION", noWrites, false},
		{"CACHE TABLE passes a rule fencing writes to its target", "CACHE TABLE t", fenceT, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := inspect.AnalyzeSQLIn(tc.sql, lexer.Spark)
			stmt := inspect.Statement{
				Protocol: inspect.HTTP, Direction: inspect.FromClient, Text: tc.sql,
				Operation: a.Operation, Effects: a.Effects, Relations: a.Relations, Tables: a.Tables,
			}
			if denied := tc.rules.Evaluate(stmt).Denied; denied != tc.wantDenied {
				t.Errorf("denied = %v, want %v: op = %q, effects = %v, relations = %v, complete = %v (%s)",
					denied, tc.wantDenied, a.Operation, a.Effects, a.Relations, a.Complete, a.Reason)
			}
		})
	}

	// A `:name` parameter may spell a keyword. Read as a word, `:into` made
	// the word after it a written relation, which a table rule then acts on.
	for _, r := range inspect.AnalyzeSQLIn("SELECT * FROM s WHERE a = :into AND b = ?", lexer.Spark).Relations {
		if r.Access == inspect.AccessWrite {
			t.Errorf("a parameter spelled into wrote %q", r.Name)
		}
	}
}
