package lexer_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
)

// writes reports the relations the analysis says are changed.
func writes(a lexer.Analysis) []string {
	var out []string
	for _, r := range a.Relations {
		if r.Access == lexer.Write {
			out = append(out, r.Name)
		}
	}
	return out
}

func reads(a lexer.Analysis) []string {
	var out []string
	for _, r := range a.Relations {
		if r.Access == lexer.Read {
			out = append(out, r.Name)
		}
	}
	return out
}

// The reported bug. A mutation inside a CTE body is a mutation, and reading
// only the tail verb reports a select for a statement that empties a table.
func TestDataModifyingCTEIsAWrite(t *testing.T) {
	for _, sql := range []string{
		`WITH doomed AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM doomed`,
		`WITH a AS (SELECT 1), b AS (DELETE FROM customers RETURNING *) SELECT 1`,
		`WITH moved AS (DELETE FROM src RETURNING *) INSERT INTO dst SELECT * FROM moved`,
	} {
		a := lexer.Analyze(sql, lexer.Postgres)
		if !a.Writes() {
			t.Errorf("Writes() = false for %q; effects=%v", sql, a.Effects)
		}
		if !slices.Contains(a.Effects, lexer.Delete) {
			t.Errorf("effects = %v, want a delete: %s", a.Effects, sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s) for %q", a.Reason, sql)
		}
	}
}

// The top-level form already worked and must keep working.
func TestPlainCTEStillClassifies(t *testing.T) {
	a := lexer.Analyze(`WITH recent AS (SELECT id FROM orders) DELETE FROM customers WHERE id IN (SELECT id FROM recent)`, lexer.Postgres)
	if got := a.Severity(); got != lexer.Delete {
		t.Errorf("Severity() = %q, want delete", got)
	}
	if got := writes(a); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("writes = %v, want [customers]", got)
	}
}

// A CTE alias is not a base relation. Reporting it puts a name nobody created
// into a table list beside real objects, and lets a `tables: [x]` rule match
// somebody's scratch CTE.
func TestCTEAliasesAreNotRelations(t *testing.T) {
	a := lexer.Analyze(`WITH doomed AS (SELECT id FROM customers) SELECT * FROM doomed`, lexer.Postgres)
	for _, r := range a.Relations {
		if r.Name == "doomed" {
			t.Errorf("the CTE alias was reported as a relation: %v", a.Relations)
		}
	}
}

// A CTE named after a keyword must not hijack the statement verb. The old
// walk started one token in and tested the NAME against the verb table.
func TestCTENameDoesNotHijackTheVerb(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want lexer.Verb
	}{
		{`WITH set AS (SELECT 1) SELECT * FROM set`, lexer.Select},
		{`WITH copy AS (SELECT 1) SELECT * FROM copy`, lexer.Select},
		{`WITH delete AS (SELECT 1) SELECT * FROM delete`, lexer.Select},
	} {
		if got := lexer.Analyze(tc.sql, lexer.Postgres).Severity(); got != tc.want {
			t.Errorf("Severity() = %q, want %q: %s", got, tc.want, tc.sql)
		}
	}
}

// A reserved word used as an ALIAS is not a statement verb. PostgreSQL allows
// one exactly when AS is written, and `SELECT 1 AS delete` is a select.
//
// The false positive was found in production traffic, not in review: Metabase
// asks the catalog which privileges it holds and names each column after the
// privilege it tested, so its schema sync was refused by a read-only lane on
// every table. Any BI tool introspecting privileges writes some version of it.
func TestReservedWordAliasIsNotAStatementHead(t *testing.T) {
	for _, sql := range []string{
		`SELECT 1 AS delete`,
		`SELECT 1 AS update, 2 AS insert, 3 AS drop`,
		`SELECT x FROM t AS delete`,
		`WITH p AS (SELECT 1 AS delete) SELECT * FROM p`,
		// Trimmed from the statement Metabase's sync actually sends.
		`WITH table_privileges AS (
		   SELECT has_table_privilege(current_user, t.tablename, 'delete') AS delete,
		          has_table_privilege(current_user, t.tablename, 'update') AS update
		   FROM pg_catalog.pg_tables t
		 ) SELECT tp.* FROM table_privileges tp`,
	} {
		a := lexer.Analyze(sql, lexer.Postgres)
		if got := a.Severity(); got != lexer.Select {
			t.Errorf("Severity() = %q, want select: %s", got, sql)
		}
		if a.Writes() {
			t.Errorf("Writes() = true for a read: %v: %s", a.Effects, sql)
		}
	}
}

// The other half of that fix: AS still heads a statement where it genuinely
// does. Suppressing it everywhere would hide the SELECT inside a CTAS, which
// is a real read of a real table.
func TestASStillHeadsAStatementUnderDDL(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want lexer.Verb
	}{
		{`CREATE TABLE snapshot AS SELECT * FROM customers`, lexer.Select},
		{`CREATE VIEW v AS SELECT * FROM customers`, lexer.Select},
		{`CREATE MATERIALIZED VIEW m AS SELECT * FROM customers`, lexer.Select},
		{`PREPARE p AS SELECT * FROM customers`, lexer.Select},
	} {
		a := lexer.Analyze(tc.sql, lexer.Postgres)
		if !slices.Contains(a.Effects, tc.want) {
			t.Errorf("effects = %v, want to contain %q: %s", a.Effects, tc.want, tc.sql)
		}
		if !slices.Contains(reads(a), "customers") {
			t.Errorf("reads = %v, want to contain customers: %s", reads(a), tc.sql)
		}
	}
	// A CTE body is head position because of the parenthesis that opens it,
	// not because of the AS in front of it, so the fix must not touch it.
	a := lexer.Analyze(`WITH doomed AS (DELETE FROM customers RETURNING *) SELECT 1`, lexer.Postgres)
	if got := a.Severity(); got != lexer.Delete {
		t.Errorf("Severity() = %q, want delete", got)
	}
}

// E” honours backslash escapes in every server configuration, so a scanner
// that stops at the backslash-quote swallows the semicolon and loses the
// whole statement that follows. This is the worst of the bypasses because it
// hides a statement rather than mislabelling one.
func TestEscapeStringDoesNotSwallowTheNextStatement(t *testing.T) {
	a := lexer.Analyze(`UPDATE audit SET note = E'O\'Brien'; DELETE FROM customers`, lexer.Postgres)

	if !slices.Contains(a.Effects, lexer.Delete) {
		t.Fatalf("the DELETE was swallowed by the escape string: effects=%v", a.Effects)
	}
	if got := writes(a); !slices.Contains(got, "customers") {
		t.Errorf("writes = %v, want customers", got)
	}
}

// The same shape under standard_conforming_strings=off, which this package
// cannot detect. The literal then scans short and the trailing quote is left
// open, so the honest outcome is Complete=false rather than a confident
// misread.
func TestNonStandardBackslashFailsClosed(t *testing.T) {
	a := lexer.Analyze(`UPDATE audit SET note = 'O\'Brien'; DELETE FROM customers`, lexer.Postgres)
	if a.Complete {
		t.Errorf("Complete = true on an ambiguous backslash literal: %+v", a)
	}
}

// A dollar-quoted body is data. Scanning it as SQL reports writes a function
// DEFINITION never performs, and an unbalanced parenthesis inside it corrupts
// the region stack.
func TestDollarQuotedBodiesAreData(t *testing.T) {
	a := lexer.Analyze(
		`CREATE FUNCTION f() RETURNS void AS $$ DELETE FROM customers $$ LANGUAGE plpgsql`,
		lexer.Postgres)
	if slices.Contains(writes(a), "customers") {
		t.Errorf("a function definition reported a phantom write: %v", a.Relations)
	}
	// Defining a function performs exactly one effect, a create, and the
	// body is data at that moment. The unanalyzable event is the
	// INVOCATION, which CALL and DO already report. Marking every
	// migration incomplete would make the flag noise.
	if !a.Complete {
		t.Errorf("Complete = false (%s); a definition is fully understood", a.Reason)
	}
	if got := a.Severity(); got != lexer.Create {
		t.Errorf("Severity() = %q, want create", got)
	}
}

func TestParenInsideDollarQuoteDoesNotCorruptTheStack(t *testing.T) {
	for _, sql := range []string{
		`WITH x AS (SELECT $$a)b$$) DELETE FROM customers`,
		`WITH x AS (SELECT $$a(b$$) DELETE FROM customers`,
		`WITH x AS (SELECT $tag$a)b$tag$) DELETE FROM customers`,
	} {
		a := lexer.Analyze(sql, lexer.Postgres)
		if got := a.Severity(); got != lexer.Delete {
			t.Errorf("Severity() = %q, want delete: %s", got, sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, sql)
		}
	}
}

// A $1 placeholder is not a dollar tag. Every driver emits them.
func TestParameterPlaceholdersAreNotDollarQuotes(t *testing.T) {
	a := lexer.Analyze(`DELETE FROM customers WHERE id = $1 AND org = $2`, lexer.Postgres)
	if got := a.Severity(); got != lexer.Delete {
		t.Fatalf("Severity() = %q, want delete", got)
	}
	if !a.Complete {
		t.Errorf("Complete = false (%s)", a.Reason)
	}
}

// Both engines nest block comments, unlike the standard. Stopping at the
// first close reads the tail of an outer comment as live SQL.
// PostgreSQL ends a -- comment at a lone CR as well as at LF; the DELETE
// after `-- x<CR>` runs. A CR LF line end reads as before (ENG-855).
func TestDashCommentEndsAtCarriageReturn(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want lexer.Verb
	}{
		{"-- note\rDELETE FROM customers", lexer.Delete},
		{"SELECT 1; -- note\rDELETE FROM customers", lexer.Delete},
		{"SELECT 1 -- note\r\n; DELETE FROM customers", lexer.Delete},
		{"SELECT 1 -- note\r\n", lexer.Select},
		{"SELECT 1 -- note DELETE FROM customers", lexer.Select},
	} {
		a := lexer.Analyze(tc.sql, lexer.Postgres)
		if a.Severity() != tc.want || !a.Complete {
			t.Errorf("severity = %q, complete = %v, want %q: %q", a.Severity(), a.Complete, tc.want, tc.sql)
		}
	}
	if got := lexer.Split("SELECT 1; -- note\rDELETE FROM customers", lexer.Postgres); len(got) != 2 {
		t.Errorf("Split = %q, want the DELETE as its own statement", got)
	}
}

func TestNestedBlockComments(t *testing.T) {
	a := lexer.Analyze(`/* outer /* inner */ DELETE FROM customers */ SELECT 1`, lexer.Postgres)
	if a.Writes() {
		t.Errorf("commented-out SQL was executed as live: effects=%v rels=%v", a.Effects, a.Relations)
	}
	if got := a.Severity(); got != lexer.Select {
		t.Errorf("Severity() = %q, want select", got)
	}
}

// A quoted identifier is never a keyword, however it spells.
func TestQuotedIdentifiersAreNotKeywords(t *testing.T) {
	a := lexer.Analyze(`DELETE FROM "select"`, lexer.Postgres)
	if got := writes(a); !slices.Equal(got, []string{"select"}) {
		t.Errorf("writes = %v, want [select]; the relation was lost to the keyword table", got)
	}
}

// The read/write split, which no amount of verb classification provides.
func TestReadWriteAttribution(t *testing.T) {
	for _, tc := range []struct {
		sql         string
		write, read []string
	}{
		{`INSERT INTO staging SELECT * FROM customers`, []string{"staging"}, []string{"customers"}},
		{`DELETE FROM sessions WHERE uid IN (SELECT id FROM customers)`, []string{"sessions"}, []string{"customers"}},
		{`UPDATE accounts SET bal = 1 FROM ledger WHERE ledger.id = accounts.id`, []string{"accounts"}, []string{"ledger"}},
		{`DELETE FROM a USING b WHERE a.id = b.id`, []string{"a"}, []string{"b"}},
		{`SELECT * FROM customers JOIN orders ON true`, nil, []string{"customers", "orders"}},
		{`TRUNCATE TABLE logs`, []string{"logs"}, nil},
	} {
		a := lexer.Analyze(tc.sql, lexer.Postgres)
		if got := writes(a); !slices.Equal(got, tc.write) {
			t.Errorf("writes = %v, want %v: %s", got, tc.write, tc.sql)
		}
		if got := reads(a); !slices.Equal(got, tc.read) {
			t.Errorf("reads = %v, want %v: %s", got, tc.read, tc.sql)
		}
	}
}

// EXPLAIN plans, EXPLAIN ANALYZE executes. Refusing the first would block the
// command a developer uses to check their WHERE clause.
func TestExplainPlansButAnalyzeExecutes(t *testing.T) {
	plan := lexer.Analyze(`EXPLAIN DELETE FROM customers`, lexer.Postgres)
	if plan.Writes() {
		t.Errorf("plain EXPLAIN reported a write: %v", plan.Effects)
	}
	run := lexer.Analyze(`EXPLAIN ANALYZE DELETE FROM customers`, lexer.Postgres)
	if !run.Writes() {
		t.Errorf("EXPLAIN ANALYZE reported no write: %v", run.Effects)
	}
	paren := lexer.Analyze(`EXPLAIN (ANALYZE, BUFFERS) DELETE FROM customers`, lexer.Postgres)
	if !paren.Writes() {
		t.Errorf("EXPLAIN (ANALYZE) reported no write: %v", paren.Effects)
	}

	// ANALYSE is the same option, and PostgreSQL compares a quoted option
	// name verbatim. A quoted name the option table does not know is
	// rejected by PostgreSQL or is an escape the scanner does not decode,
	// so it counts as ANALYZE.
	for _, sql := range []string{
		`EXPLAIN ANALYSE DELETE FROM customers`,
		`EXPLAIN ANALYSE VERBOSE DELETE FROM customers`,
		`EXPLAIN (ANALYSE) DELETE FROM customers`,
		`EXPLAIN ("analyze") DELETE FROM customers`,
		`EXPLAIN (FORMAT JSON, "analyze") DELETE FROM customers`,
		`EXPLAIN (U&"\0061nalyze") DELETE FROM customers`,
	} {
		if a := lexer.Analyze(sql, lexer.Postgres); !slices.Contains(a.Effects, lexer.Delete) {
			t.Errorf("EXPLAIN that executes reported no delete: %v: %s", a.Effects, sql)
		}
	}
	// A quoted FORMAT value and a quoted CTE column are not the ANALYZE
	// option, so these stay plans.
	for _, sql := range []string{
		`EXPLAIN (FORMAT "json") DELETE FROM customers`,
		`EXPLAIN ("verbose") DELETE FROM customers`,
		`EXPLAIN WITH x("analyze") AS (SELECT 1) DELETE FROM customers`,
	} {
		if a := lexer.Analyze(sql, lexer.Postgres); a.Writes() {
			t.Errorf("a plan reported a write: %v: %s", a.Effects, sql)
		}
	}
}

// A conditional clause is still a statement head.
func TestMergeConditionalClauses(t *testing.T) {
	a := lexer.Analyze(
		`MERGE INTO customers c USING staging s ON c.id = s.id
		 WHEN MATCHED THEN DELETE
		 WHEN NOT MATCHED THEN INSERT VALUES (s.id)`, lexer.Postgres)

	if !slices.Contains(a.Effects, lexer.Delete) {
		t.Errorf("the MERGE delete branch was missed: %v", a.Effects)
	}
	if got := writes(a); !slices.Contains(got, "customers") {
		t.Errorf("writes = %v, want customers", got)
	}
	if got := reads(a); !slices.Contains(got, "staging") {
		t.Errorf("reads = %v, want staging", got)
	}
}

func TestCopyAndCreateAsUnwrap(t *testing.T) {
	copyOut := lexer.Analyze(`COPY (DELETE FROM customers RETURNING *) TO STDOUT`, lexer.Postgres)
	if !slices.Contains(copyOut.Effects, lexer.Delete) {
		t.Errorf("COPY hid a delete: %v", copyOut.Effects)
	}
	ctas := lexer.Analyze(`CREATE TABLE snapshot AS SELECT * FROM customers`, lexer.Postgres)
	if got := writes(ctas); !slices.Equal(got, []string{"snapshot"}) {
		t.Errorf("writes = %v, want [snapshot]", got)
	}
	if got := reads(ctas); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("reads = %v, want [customers]", got)
	}
}

// The three shapes no parser resolves. The only correct answer is to say so.
func TestOpaqueStatementsAreIncomplete(t *testing.T) {
	for _, sql := range []string{
		`DO $$ BEGIN DELETE FROM customers; END $$`,
		`CALL purge_everything()`,
		`EXECUTE prepared_delete`,
	} {
		a := lexer.Analyze(sql, lexer.Postgres)
		if a.Complete {
			t.Errorf("Complete = true for an opaque statement: %s", sql)
		}
		if a.Reason == "" {
			t.Errorf("no Reason given: %s", sql)
		}
	}
}

// An unbalanced statement must not come back confident.
func TestUnbalancedInputFailsClosed(t *testing.T) {
	for _, sql := range []string{
		`SELECT * FROM (SELECT 1`,
		`SELECT 'unterminated`,
		`SELECT $$unterminated`,
		`SELECT * FROM "unterminated`,
		`/* unterminated`,
	} {
		if a := lexer.Analyze(sql, lexer.Postgres); a.Complete {
			t.Errorf("Complete = true for %q", sql)
		}
	}
}

// T-SQL brackets are identifiers; PostgreSQL brackets are array subscripts.
// One lexer cannot be both, which is why Dialect exists.
func TestDialectBrackets(t *testing.T) {
	ms := lexer.Analyze(`SELECT * FROM [dbo].[customers]`, lexer.MSSQL)
	if got := reads(ms); !slices.Equal(got, []string{"dbo.customers"}) {
		t.Errorf("mssql reads = %v, want [dbo.customers]", got)
	}
	msEsc := lexer.Analyze(`SELECT * FROM [odd]]name]`, lexer.MSSQL)
	if got := reads(msEsc); !slices.Equal(got, []string{"odd]name"}) {
		t.Errorf("mssql reads = %v, want [odd]name] (doubled-bracket escape)", got)
	}
	pg := lexer.Analyze(`SELECT tags[1] FROM customers WHERE a[1] = 'x'`, lexer.Postgres)
	if got := reads(pg); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("postgres reads = %v, want [customers]; '[' is a subscript here", got)
	}
	if !pg.Complete {
		t.Errorf("Complete = false (%s) on an array subscript", pg.Reason)
	}
}

// A string literal's CONTENT must never reach a token. It ends up in an audit
// record and a policy decision log.
func TestLiteralsAreNotClassified(t *testing.T) {
	a := lexer.Analyze(`SELECT 'DROP TABLE customers' AS msg FROM t`, lexer.Postgres)
	if a.Writes() {
		t.Errorf("a string literal was read as SQL: %v", a.Effects)
	}
	if got := reads(a); !slices.Equal(got, []string{"t"}) {
		t.Errorf("reads = %v, want [t]", got)
	}
}

// Names must survive the doubled-quote escape rather than truncating at it.
func TestDoubledQuoteEscapeInIdentifiers(t *testing.T) {
	a := lexer.Analyze(`DELETE FROM "cust""omers"`, lexer.Postgres)
	if got := writes(a); !slices.Equal(got, []string{`cust"omers`}) {
		t.Errorf("writes = %v, want [cust\"omers]", got)
	}
}

func TestSchemaQualifiedNames(t *testing.T) {
	a := lexer.Analyze(`DELETE FROM public.customers`, lexer.Postgres)
	if got := writes(a); !slices.Equal(got, []string{"public.customers"}) {
		t.Errorf("writes = %v, want [public.customers]", got)
	}
}

// A set-returning function is not a relation.
func TestFunctionCallsAreNotRelations(t *testing.T) {
	a := lexer.Analyze(`SELECT * FROM generate_series(1, 10)`, lexer.Postgres)
	if len(a.Relations) != 0 {
		t.Errorf("relations = %v, want none", a.Relations)
	}
}

// Severity is what a caller wanting one verb should read.
func TestSeverityReportsTheWorstEffect(t *testing.T) {
	a := lexer.Analyze(`WITH x AS (DROP SCHEMA s) SELECT 1`, lexer.Postgres)
	if got := a.Severity(); got != lexer.Drop {
		t.Errorf("Severity() = %q, want drop", got)
	}
}

// Every Verb constant must equal the inspect.Operation string it maps to.
// They are separate vocabularies so lexer stays a leaf, and a drift between
// them would silently stop policies matching.
func TestVerbStringsAreStable(t *testing.T) {
	for v, want := range map[lexer.Verb]string{
		lexer.Select: "select", lexer.Insert: "insert", lexer.Update: "update",
		lexer.Delete: "delete", lexer.Create: "create", lexer.Drop: "drop",
		lexer.Alter: "alter", lexer.Truncate: "truncate", lexer.Grant: "grant",
		lexer.Revoke: "revoke", lexer.Call: "call", lexer.Show: "show",
		lexer.Set: "set", lexer.Begin: "begin", lexer.Commit: "commit",
		lexer.Rollback: "rollback", lexer.Other: "other", lexer.Unknown: "unknown",
	} {
		if string(v) != want {
			t.Errorf("Verb %q != %q", v, want)
		}
	}
}

func TestAnalyzeDoesNotPanic(t *testing.T) {
	for _, sql := range []string{
		"", " ", ";", "(", ")", "'", `"`, "$", "$$", "--", "/*", "*/",
		"`", "``", "#", "-", `'a\`, "`a``",
		"r'", `rb"`, "'''", `"""`, "r'''", "@", "@{", "@{}",
		"q'", "q'[", "nq'", "Nq'x", "q'\xff", "q'\xc3", ":", ":a", "a@", "a@b.", "begin", "declare",
		"create trigger", "create trigger t on", "create trigger t on nested", "create procedure",
		"with", "with as", "with x as", "select from", "delete from",
		"/*+", "/*+ x", "/*+ /*", "${", "--\\", "--\\\n", "\u00a0", "\xc2", "r'", `R"`, "begin",
		"begin transaction", "from", "copy into", "insert overwrite", "convert", "convert to",
		"convert to delta", "cache", "cache lazy", "vacuum", "restore table",
		strings.Repeat("(", 200), strings.Repeat(")", 200),
		strings.Repeat("with x as (", 50), strings.Repeat("/*+ ", 50),
	} {
		for _, d := range []lexer.Dialect{lexer.Postgres, lexer.MSSQL, lexer.MySQL, lexer.ClickHouse, lexer.GoogleSQL, lexer.Oracle, lexer.Spark} {
			lexer.Analyze(sql, d)
			lexer.Split(sql, d)
		}
	}
}

// COPY's direction is a keyword AFTER the relation, so it is the one access
// that cannot be decided from the introducer.
//
// Filing an export as a write is not a harmless imprecision. It is exactly
// `COPY customers TO PROGRAM 'curl ...'`, and the rule that catches that is a
// rule watching READS of customers.
func TestCopyDirectionDecidesAccess(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		name string
		want lexer.Access
	}{
		{`COPY t TO STDOUT`, "t", lexer.Read},
		{`COPY t (a, b) TO STDOUT WITH CSV HEADER`, "t", lexer.Read},
		{`COPY public.t TO PROGRAM 'sink'`, "public.t", lexer.Read},
		{`COPY t FROM STDIN`, "t", lexer.Write},
		{`COPY t (a, b) FROM STDIN WITH CSV HEADER`, "t", lexer.Write},
		{`COPY public.t FROM PROGRAM 'src'`, "public.t", lexer.Write},
	} {
		a := lexer.Analyze(tc.sql, lexer.Postgres)
		if len(a.Relations) != 1 {
			t.Errorf("relations = %v, want exactly one: %s", a.Relations, tc.sql)
			continue
		}
		got := a.Relations[0]
		if got.Name != tc.name || got.Access != tc.want {
			t.Errorf("got %v, want {%s %v}: %s", got, tc.name, tc.want, tc.sql)
		}
	}
}

// A relation list under one keyword. Stopping at the head means a rule
// guarding the second name never fires.
func TestCommaSeparatedRelationLists(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want []string
	}{
		{`TRUNCATE TABLE a, b`, []string{"a", "b"}},
		{`DROP TABLE IF EXISTS public.a, b`, []string{"public.a", "b"}},
		{`GRANT ALL ON warehouse.stock, warehouse.audit TO app`,
			[]string{"warehouse.stock", "warehouse.audit"}},
	} {
		if got := writes(lexer.Analyze(tc.sql, lexer.Postgres)); !slices.Equal(got, tc.want) {
			t.Errorf("writes = %v, want %v: %s", got, tc.want, tc.sql)
		}
	}
}

// A bare clause keyword occupies a relation position without naming one.
// `WHEN MATCHED THEN UPDATE SET n = 1` has an UPDATE with no target, and the
// scanner used to record a relation called "set".
func TestClauseKeywordsAreNotRelations(t *testing.T) {
	merge := lexer.Analyze(
		`MERGE INTO customers c USING staging s ON c.id = s.id WHEN MATCHED THEN UPDATE SET n = s.n`,
		lexer.Postgres)
	if got := writes(merge); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("writes = %v, want [customers]", got)
	}
	upsert := lexer.Analyze(
		`INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET a = 2`, lexer.Postgres)
	for _, r := range upsert.Relations {
		if r.Name == "set" || r.Name == "conflict" {
			t.Errorf("invented a relation %q: %v", r.Name, upsert.Relations)
		}
	}
	// A table genuinely named "set" is quoted, and quoted identifiers must
	// bypass the keyword filter entirely.
	quoted := lexer.Analyze(`DELETE FROM "set"`, lexer.Postgres)
	if got := writes(quoted); !slices.Equal(got, []string{"set"}) {
		t.Errorf("writes = %v, want [set]; a quoted name was filtered as a keyword", got)
	}
}

// ON introduces a relation in DDL and a join predicate everywhere else.
// Treating it as an introducer unconditionally invents one out of a join.
func TestOnIntroducesOnlyUnderDDL(t *testing.T) {
	join := lexer.Analyze(`SELECT * FROM a JOIN b ON a.id = b.id`, lexer.Postgres)
	if got := reads(join); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("reads = %v, want [a b]; ON fabricated a relation", got)
	}
	for _, tc := range []struct{ sql, want string }{
		{`CREATE INDEX i ON t (c)`, "t"},
		{`GRANT SELECT ON customers TO app`, "customers"},
		{`REVOKE INSERT ON customers FROM app`, "customers"},
	} {
		if got := writes(lexer.Analyze(tc.sql, lexer.Postgres)); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("writes = %v, want [%s]: %s", got, tc.want, tc.sql)
		}
	}
	// REVOKE's FROM names a role, not a relation.
	rev := lexer.Analyze(`REVOKE INSERT ON customers FROM app`, lexer.Postgres)
	for _, r := range rev.Relations {
		if r.Name == "app" {
			t.Errorf("a role was reported as a relation: %v", rev.Relations)
		}
	}
}

// A column or alias that spells a relation introducer introduces nothing.
// Read as one, `s.update` or `amount AS update` took the FROM after it for the
// relation and lost the table, so a rule fencing reads of salaries let the
// select through (ENG-859). An alias is enough to name a column update.
func TestKeywordColumnIntroducesNoRelation(t *testing.T) {
	type tc struct {
		sql   string
		reads []string
	}
	salaries := []string{"salaries"}
	var cases []tc
	for _, w := range []string{"from", "join", "into", "update", "table", "view", "truncate",
		"using", "copy", "on", "overwrite"} {
		cases = append(cases, tc{`SELECT s.` + w + ` FROM salaries s`, salaries})
	}
	cases = append(cases,
		tc{`SELECT s.update FROM (SELECT amount AS update FROM salaries) s`, salaries},
		tc{`SELECT x.into FROM (SELECT * FROM salaries) x`, salaries},
		tc{`SELECT amount AS update FROM salaries`, salaries},
		tc{`SELECT amount truncate FROM salaries`, salaries},
		tc{`SELECT update FROM salaries`, salaries},
		tc{`SELECT view FROM salaries`, salaries},
		tc{`SELECT 1 AS from FROM salaries`, salaries},
		tc{`SELECT * FROM salaries update JOIN secrets ON true`, []string{"salaries", "secrets"}},
		tc{`SELECT * FROM salaries AS join JOIN secrets ON true`, []string{"salaries", "secrets"}},
		// A keyword alias on a VALUES list and in a CTE, joined and in a
		// comma list. The comma forms also need the FROM list to continue
		// past an aliased item; see TestFromListContinuesPastAnyItem.
		tc{`SELECT x.update FROM (VALUES (1)) x(update) JOIN salaries ON true`, salaries},
		tc{`SELECT x.update FROM (VALUES (1)) x(update), salaries`, salaries},
		tc{`WITH s AS (SELECT amount AS update FROM salaries) SELECT s.update FROM s`, salaries},
		tc{`WITH s AS (SELECT 1 AS update) SELECT s.update, salaries.* FROM s JOIN salaries ON true`, salaries},
		tc{`WITH s AS (SELECT 1 AS update) SELECT s.update, salaries.* FROM s, salaries`, salaries},
	)
	for _, d := range []lexer.Dialect{lexer.Postgres, lexer.MSSQL, lexer.MySQL, lexer.ClickHouse,
		lexer.GoogleSQL, lexer.Oracle, lexer.Spark} {
		// ClickHouse and Spark do not reserve AS, so `AS from FROM` may be
		// a column called as and a real FROM, and the alias after AS is
		// recorded too. Every relation the statement reads must still be.
		permissive := d == lexer.ClickHouse || d == lexer.Spark
		for _, c := range cases {
			a := lexer.Analyze(c.sql, d)
			got := reads(a)
			missing := slices.ContainsFunc(c.reads, func(r string) bool { return !slices.Contains(got, r) })
			if missing || len(writes(a)) != 0 || !permissive && !slices.Equal(got, c.reads) {
				t.Errorf("%s: reads = %v, writes = %v, want reads %v: %s", d, got, writes(a), c.reads, c.sql)
			}
		}
	}
}

// A FROM list continues past an item with an alias, a subquery, a CTE name or
// a function call. The scanner followed a list only across bare names, so
// `SELECT * FROM a x, salaries` read a, and a rule fencing reads of salaries
// let it through (ENG-859). The same list after DELETE's USING, UPDATE's FROM
// and MySQL's multi-table UPDATE names relations too.
func TestFromListContinuesPastAnyItem(t *testing.T) {
	all := []lexer.Dialect{lexer.Postgres, lexer.MSSQL, lexer.MySQL, lexer.ClickHouse,
		lexer.GoogleSQL, lexer.Oracle, lexer.Spark}
	for _, tc := range []struct {
		ds            []lexer.Dialect
		sql           string
		reads, writes []string
	}{
		{all, `SELECT * FROM a x, salaries`, []string{"a", "salaries"}, nil},
		{all, `SELECT * FROM a AS x, b y, salaries`, []string{"a", "b", "salaries"}, nil},
		{all, `SELECT * FROM (SELECT 1) x, salaries`, []string{"salaries"}, nil},
		{all, `SELECT * FROM a x, (SELECT * FROM salaries) y`, []string{"a", "salaries"}, nil},
		{all, `WITH s AS (SELECT 1) SELECT * FROM s, salaries`, []string{"salaries"}, nil},
		{all, `SELECT * FROM generate_series(1, 2) g, salaries`, []string{"salaries"}, nil},
		{all, `SELECT * FROM a JOIN b ON a.id = b.id, salaries`, []string{"a", "b", "salaries"}, nil},
		{all, `SELECT * FROM a JOIN b USING (id), salaries`, []string{"a", "b", "salaries"}, nil},
		{all, `SELECT * FROM a x LEFT OUTER JOIN b y ON true, salaries`, []string{"a", "b", "salaries"}, nil},
		// A clause word as a member or an alias ends nothing.
		{all, `SELECT * FROM a JOIN b ON a.limit = b.limit, salaries`, []string{"a", "b", "salaries"}, nil},
		{all, `SELECT * FROM a AS limit, salaries`, []string{"a", "salaries"}, nil},
		{all, `SELECT 1 AS limit FROM a x, salaries`, []string{"a", "salaries"}, nil},
		{[]lexer.Dialect{lexer.Oracle}, `SELECT * FROM a limit, salaries`, []string{"a", "salaries"}, nil},
		{[]lexer.Dialect{lexer.Oracle}, `SELECT * FROM a limit JOIN salaries ON true`, []string{"a", "salaries"}, nil},
		// DISTINCT as a member is not IS DISTINCT FROM; Spark allows a
		// table called as.
		{all, `SELECT s.distinct FROM a s, salaries`, []string{"a", "salaries"}, nil},
		{[]lexer.Dialect{lexer.Spark}, `SELECT * FROM as x, salaries`, []string{"as", "salaries"}, nil},
		{all, `SELECT * FROM t1 UNION SELECT * FROM t2 x, salaries`, []string{"t1", "t2", "salaries"}, nil},
		{all, `CREATE VIEW v AS SELECT * FROM a x, salaries`, []string{"a", "salaries"}, []string{"v"}},
		{[]lexer.Dialect{lexer.Postgres}, `DELETE FROM t x USING s y, salaries WHERE x.id = y.id`,
			[]string{"s", "salaries"}, []string{"t"}},
		{[]lexer.Dialect{lexer.Postgres, lexer.MSSQL}, `UPDATE t SET a = 1 FROM s x, salaries WHERE t.id = x.id`,
			[]string{"s", "salaries"}, []string{"t"}},
		{[]lexer.Dialect{lexer.MySQL}, `UPDATE t1 a, salaries b SET a.x = b.y`, []string{"salaries"}, []string{"t1"}},
		{[]lexer.Dialect{lexer.MSSQL}, `SELECT * FROM a x WITH (NOLOCK), salaries`, []string{"a", "salaries"}, nil},
		// T-SQL's OUTPUT comes before UPDATE's FROM.
		{[]lexer.Dialect{lexer.MSSQL}, `UPDATE t SET x = s.amount OUTPUT inserted.x FROM a q, salaries s WHERE t.id = s.id`,
			[]string{"a", "salaries"}, []string{"t"}},
		{[]lexer.Dialect{lexer.MSSQL}, `SELECT * FROM a FOR SYSTEM_TIME AS OF '2020-01-01' x, salaries`,
			[]string{"a", "salaries"}, nil},
		{[]lexer.Dialect{lexer.Oracle}, `SELECT * FROM a x, b@link y, salaries`, []string{"a", "b", "salaries"}, nil},
		{[]lexer.Dialect{lexer.GoogleSQL}, `SELECT * FROM t x, UNNEST(x.arr) AS e WITH OFFSET AS o, salaries`,
			[]string{"t", "salaries"}, nil},
		{[]lexer.Dialect{lexer.Spark}, `FROM a x, salaries INSERT INTO t SELECT c, d`,
			[]string{"a", "salaries"}, []string{"t"}},
		// Spark does not reserve TABLE: here it aliases a.
		{[]lexer.Dialect{lexer.Spark}, `SELECT * FROM a table JOIN salaries ON true`,
			[]string{"a", "join", "salaries"}, nil},
	} {
		for _, d := range tc.ds {
			a := lexer.Analyze(tc.sql, d)
			if !slices.Equal(reads(a), tc.reads) || !slices.Equal(writes(a), tc.writes) {
				t.Errorf("%s: reads = %v, writes = %v, want %v and %v: %s", d, reads(a), writes(a), tc.reads, tc.writes, tc.sql)
			}
		}
	}
}

// The clause after a FROM list ends it. Its commas separate columns, sort
// keys or settings, and read as list items they would name relations that a
// rule acts on.
func TestFromListEndsAtTheNextClause(t *testing.T) {
	all := []lexer.Dialect{lexer.Postgres, lexer.MSSQL, lexer.MySQL, lexer.ClickHouse,
		lexer.GoogleSQL, lexer.Oracle, lexer.Spark}
	for _, tc := range []struct {
		ds            []lexer.Dialect
		sql           string
		reads, writes []string
	}{
		{all, `SELECT a, b FROM t x GROUP BY a, b ORDER BY a, b`, []string{"t"}, nil},
		{all, `SELECT * FROM t x WHERE a IN (1, 2) AND b = 1`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.Postgres}, `SELECT * FROM t x WINDOW w AS (ORDER BY a), v AS (ORDER BY b)`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.Postgres}, `DELETE FROM t USING s x WHERE t.id = x.id RETURNING t.a, t.b`,
			[]string{"s"}, []string{"t"}},
		{[]lexer.Dialect{lexer.Postgres}, `UPDATE t SET a = 1, b = 2 FROM s x WHERE t.id = x.id`,
			[]string{"s"}, []string{"t"}},
		{[]lexer.Dialect{lexer.Postgres}, `INSERT INTO t SELECT * FROM s x ON CONFLICT (k) DO UPDATE SET c = 1, d = 2`,
			[]string{"s"}, []string{"t"}},
		{[]lexer.Dialect{lexer.MySQL}, `SELECT * FROM t x LIMIT 1, 2`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.MSSQL}, `DELETE FROM t OUTPUT deleted.a, deleted.b WHERE id = 1`, nil, []string{"t"}},
		{[]lexer.Dialect{lexer.MSSQL}, `SELECT * FROM t x OPTION (MAXDOP 1), a`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.ClickHouse}, `SELECT * FROM t x SETTINGS max_threads = 1, max_memory_usage = 2`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.ClickHouse}, `SELECT * FROM t x LIMIT 1 BY a, b`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.Spark}, `SELECT * FROM t x DISTRIBUTE BY a, b SORT BY a, b`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.Spark, lexer.GoogleSQL}, `SELECT * FROM t x QUALIFY row_number() OVER (PARTITION BY a) = 1, b`,
			[]string{"t"}, nil},
		// ClickHouse and Spark do not reserve FROM: before a comma it is a
		// column, and no item can follow it.
		{[]lexer.Dialect{lexer.ClickHouse, lexer.Spark}, `SELECT * FROM t ORDER BY from, salaries`, []string{"t"}, nil},
		{[]lexer.Dialect{lexer.ClickHouse, lexer.Spark}, `SELECT * FROM t ORDER BY a, from`, []string{"t"}, nil},
	} {
		for _, d := range tc.ds {
			a := lexer.Analyze(tc.sql, d)
			if !slices.Equal(reads(a), tc.reads) || !slices.Equal(writes(a), tc.writes) {
				t.Errorf("%s: reads = %v, writes = %v, want %v and %v: %s", d, reads(a), writes(a), tc.reads, tc.writes, tc.sql)
			}
		}
	}
}

// IS [NOT] DISTINCT FROM compares; its FROM begins no list.
func TestIsDistinctFromBeginsNoList(t *testing.T) {
	for _, d := range []lexer.Dialect{lexer.Postgres, lexer.MSSQL, lexer.MySQL, lexer.ClickHouse,
		lexer.GoogleSQL, lexer.Oracle, lexer.Spark} {
		for _, sql := range []string{
			`SELECT a IS DISTINCT FROM 1, salaries FROM t`,
			`SELECT a IS NOT DISTINCT FROM 1, salaries FROM t`,
		} {
			if got := reads(lexer.Analyze(sql, d)); !slices.Equal(got, []string{"t"}) {
				t.Errorf("%s: reads = %v, want [t]: %s", d, got, sql)
			}
		}
	}
}

// ClickHouse's ARRAY JOIN lists arrays, after a plain or an aliased item. Its
// commas name no relation.
func TestClickHouseArrayJoinEndsTheList(t *testing.T) {
	for _, sql := range []string{
		`SELECT * FROM salaries ARRAY JOIN tags AS tag, names AS name`,
		`SELECT * FROM salaries s ARRAY JOIN tags AS tag, names AS name`,
		`SELECT * FROM salaries s LEFT ARRAY JOIN tags AS tag, names AS name`,
	} {
		got := reads(lexer.Analyze(sql, lexer.ClickHouse))
		if !slices.Contains(got, "salaries") || slices.Contains(got, "names") {
			t.Errorf("reads = %v, want salaries and not names: %s", got, sql)
		}
	}
}

// The fix above must not cost a table its name. Oracle does not reserve JOIN,
// and ClickHouse, and Spark unless ANSI enforcement is on, reserve neither
// keyword, so a table called join or from is legal there and a rule naming it
// must match. Spark does not reserve AS either: `SELECT as` selects a column.
func TestKeywordNamesKeepTheirRelations(t *testing.T) {
	for _, tc := range []struct {
		d             lexer.Dialect
		sql           string
		reads, writes []string
	}{
		{lexer.Oracle, `SELECT * FROM join`, []string{"join"}, nil},
		{lexer.Oracle, `SELECT * FROM a JOIN join ON a.id = join.id`, []string{"a", "join"}, nil},
		{lexer.Oracle, `DELETE FROM join`, nil, []string{"join"}},
		{lexer.Oracle, `DROP VIEW join`, nil, []string{"join"}},
		{lexer.Spark, `SELECT * FROM from`, []string{"from"}, nil},
		{lexer.Spark, `INSERT INTO from VALUES (1)`, nil, []string{"from"}},
		{lexer.Spark, `UPDATE from SET a = 1`, nil, []string{"from"}},
		{lexer.Spark, `INSERT INTO TABLE from VALUES (1)`, nil, []string{"from"}},
		{lexer.Spark, `SELECT as FROM salaries`, []string{"salaries"}, nil},
		{lexer.Spark, `SELECT as FROM from`, []string{"from"}, nil},
		{lexer.Spark, `SELECT id, as FROM join.salaries`, []string{"join.salaries"}, nil},
		{lexer.Spark, `SELECT s.as FROM join.salaries s`, []string{"join.salaries"}, nil},
		{lexer.Spark, `SELECT 1 + as FROM from`, []string{"from"}, nil},
		{lexer.ClickHouse, `SELECT s.as FROM join.salaries s`, []string{"join.salaries"}, nil},
		{lexer.Spark, `SELECT amount AS select FROM join.salaries`, []string{"join.salaries"}, nil},
		{lexer.Spark, `SELECT s.select FROM join.salaries s`, []string{"join.salaries"}, nil},
		{lexer.Spark, `SELECT DISTINCT distinct FROM from`, []string{"from"}, nil},
		{lexer.Spark, `MERGE INTO audit AS as USING join.salaries s ON as.id = s.id WHEN MATCHED THEN DELETE`,
			[]string{"join.salaries"}, []string{"audit"}},
		{lexer.ClickHouse, `SELECT * FROM from`, []string{"from"}, nil},
	} {
		a := lexer.Analyze(tc.sql, tc.d)
		if !slices.Equal(reads(a), tc.reads) || !slices.Equal(writes(a), tc.writes) {
			t.Errorf("%s: reads = %v, writes = %v, want %v and %v: %s", tc.d, reads(a), writes(a), tc.reads, tc.writes, tc.sql)
		}
	}
}

// ClickHouse and Spark reserve none of the introducers, so any of them may
// name a column, and ClickHouse's catalog has one called table. Read as an
// introducer it took FROM for the relation and lost the table, as UPDATE did
// in TestKeywordColumnIntroducesNoRelation. Those dialects cannot rule the
// introducer reading out, so it is kept beside the column reading: the table
// is read, and the relation the old reading named is still recorded.
func TestPermissiveDialectKeywordColumnsKeepTheTable(t *testing.T) {
	for _, d := range []lexer.Dialect{lexer.ClickHouse, lexer.Spark} {
		for _, tc := range []struct{ sql, read string }{
			{`SELECT database, table FROM system.parts`, "system.parts"},
			{`SELECT table FROM audit`, "audit"},
			{`SELECT DISTINCT table FROM audit`, "audit"},
			{`SELECT into FROM audit`, "audit"},
			{`SELECT id, using FROM audit`, "audit"},
			{`SELECT 1 FROM audit a JOIN (SELECT table FROM audit) b ON true`, "audit"},
		} {
			a := lexer.Analyze(tc.sql, d)
			if got := reads(a); !slices.Contains(got, tc.read) {
				t.Errorf("%s: reads = %v, want %s read: %s", d, got, tc.read, tc.sql)
			}
		}
	}
}

// Other spellings of "create a table from a query".
func TestObjectCreatingForms(t *testing.T) {
	for _, tc := range []struct{ sql, write, read string }{
		{`CREATE VIEW v AS SELECT * FROM t`, "v", "t"},
		{`SELECT * INTO snap FROM customers`, "snap", "customers"},
		{`REFRESH MATERIALIZED VIEW mv`, "mv", ""},
	} {
		a := lexer.Analyze(tc.sql, lexer.Postgres)
		if got := writes(a); !slices.Equal(got, []string{tc.write}) {
			t.Errorf("writes = %v, want [%s]: %s", got, tc.write, tc.sql)
		}
		if tc.read == "" {
			continue
		}
		if got := reads(a); !slices.Equal(got, []string{tc.read}) {
			t.Errorf("reads = %v, want [%s]: %s", got, tc.read, tc.sql)
		}
	}

	// SELECT INTO creates its table, so it is a create to a rule that reads
	// the effects, whatever the persistence keyword before the name.
	for _, sql := range []string{
		`SELECT * INTO snap FROM customers`,
		`SELECT * INTO TEMP snap FROM customers`,
		`SELECT * INTO TEMPORARY TABLE snap FROM customers`,
		`SELECT * INTO UNLOGGED snap FROM customers`,
		`WITH c AS (SELECT 1) SELECT * INTO snap FROM c`,
	} {
		if a := lexer.Analyze(sql, lexer.Postgres); a.Severity() != lexer.Create {
			t.Errorf("Severity = %v, want create: effects %v: %s", a.Severity(), a.Effects, sql)
		}
	}
}

// The backtick is MySQL's only out-of-the-box identifier quote, so a scanner
// without it loses every relation a client bothered to quote — and clients
// quote exactly the names that collide with keywords, which is the set most
// worth naming in a rule.
func TestMySQLBacktickIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want []string
	}{
		// A relation spelled as a reserved word. The quoting must not
		// fold into a bare word, or the keyword filter drops it and the
		// delete is reported with no target at all.
		{"DELETE FROM `select`", []string{"select"}},
		// The doubled-close escape, which delimitedIdent already
		// implements for "" and ]]. Truncating at the inner pair names
		// a different table than the one being emptied.
		{"DELETE FROM `cust``omers`", []string{"cust`omers"}},
		// Schema qualification survives the quoting on both halves.
		{"DELETE FROM `app`.`orders`", []string{"app.orders"}},
	} {
		a := lexer.Analyze(tc.sql, lexer.MySQL)
		if got := writes(a); !slices.Equal(got, tc.want) {
			t.Errorf("writes = %v, want %v: %s", got, tc.want, tc.sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, tc.sql)
		}
	}
}

// '#' comments to end of line in MySQL. Reading the tail as live SQL puts a
// table nobody touched into the relation list of a plain select, and a rule
// naming that table then fires on a statement that never went near it.
func TestMySQLHashComment(t *testing.T) {
	a := lexer.Analyze("SELECT 1 # DROP TABLE t", lexer.MySQL)
	if a.Writes() {
		t.Errorf("commented-out SQL was executed as live: effects=%v rels=%v", a.Effects, a.Relations)
	}
	if got := a.Severity(); got != lexer.Select {
		t.Errorf("Severity() = %q, want select", got)
	}
	if len(a.Relations) != 0 {
		t.Errorf("relations = %v, want none; the commented tail named a phantom table", a.Relations)
	}

	// A semicolon inside the comment is not a separator, so the whole
	// line is one statement and the DROP after it stays commented out.
	// Splitting there hands the codec a fragment it would analyze as a
	// live drop.
	if got := lexer.Split("SELECT 1 # ; DROP TABLE t", lexer.MySQL); len(got) != 1 {
		t.Errorf("Split = %q, want one statement; a commented ';' was read as a separator", got)
	}

	// Only to end of LINE. A '#' comment running to end of INPUT would
	// hide every statement after it in a multi-statement query, which is
	// the delete this package exists to see.
	multi := lexer.Analyze("SELECT 1 # note\n; DELETE FROM customers", lexer.MySQL)
	if got := writes(multi); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("writes = %v, want [customers]; the comment ate the next line", got)
	}
}

// The load-bearing one. MySQL honours backslash escapes in an ordinary '...'
// literal unless NO_BACKSLASH_ESCAPES is set, so the quote after the
// backslash does not close the string and the semicolon after it is a real
// separator. A scanner using the Postgres rule runs the literal to end of
// input, splits nothing, and the DELETE is never analyzed.
func TestMySQLBackslashEscapeDoesNotSwallowTheNextStatement(t *testing.T) {
	const sql = `SELECT 'O\'Brien'; DELETE FROM t`

	got := lexer.Split(sql, lexer.MySQL)
	if len(got) != 2 {
		t.Fatalf("Split = %q, want two statements; the literal swallowed the separator", got)
	}
	if !strings.Contains(got[1], "DELETE") {
		t.Errorf("Split[1] = %q, want the DELETE", got[1])
	}

	a := lexer.Analyze(sql, lexer.MySQL)
	if !slices.Contains(a.Effects, lexer.Delete) {
		t.Errorf("the DELETE was swallowed by the literal: effects=%v", a.Effects)
	}
	if w := writes(a); !slices.Equal(w, []string{"t"}) {
		t.Errorf("writes = %v, want [t]", w)
	}
	if !a.Complete {
		t.Errorf("Complete = false (%s); MySQL's default reading is unambiguous", a.Reason)
	}

	// The same bytes under Postgres are genuinely ambiguous — it depends
	// on standard_conforming_strings, which this package cannot see — so
	// the honest answer there stays Complete=false, not a confident split.
	if pg := lexer.Analyze(sql, lexer.Postgres); pg.Complete {
		t.Errorf("postgres Complete = true on an ambiguous backslash literal: %+v", pg)
	}
}

// MySQL follows the standard and does NOT nest block comments, so the first
// close ends it and the tail is live SQL. Postgres nests, where the same
// bytes are entirely comment. Both readings are correct for their engine and
// each is a misread for the other, which is the whole reason nestedBlockComment
// is a per-dialect row rather than a constant.
func TestBlockCommentNestingIsPerDialect(t *testing.T) {
	const sql = `/* a /* b */ DELETE FROM t */`

	my := lexer.Analyze(sql, lexer.MySQL)
	if got := writes(my); !slices.Equal(got, []string{"t"}) {
		t.Errorf("mysql writes = %v, want [t]; the first */ closes and the DELETE runs", got)
	}

	pg := lexer.Analyze(sql, lexer.Postgres)
	if pg.Writes() {
		t.Errorf("postgres executed commented-out SQL: effects=%v rels=%v", pg.Effects, pg.Relations)
	}
}

// Bytes that delimit something in another dialect must stay inert here.
// Borrowing T-SQL's brackets would invent a relation out of `SELECT [a]`, and
// borrowing PostgreSQL's dollar quote would hide a real statement inside what
// MySQL reads as two user-variable references.
func TestMySQLDoesNotBorrowOtherDialectsQuoting(t *testing.T) {
	br := lexer.Analyze(`SELECT * FROM [dbo].[customers]`, lexer.MySQL)
	if len(br.Relations) != 0 {
		t.Errorf("relations = %v, want none; '[' is not an identifier quote in MySQL", br.Relations)
	}

	dq := lexer.Analyze(`SELECT $$ DELETE FROM t $$`, lexer.MySQL)
	if got := reads(dq); !slices.Equal(got, []string{"t"}) {
		t.Errorf("reads = %v, want [t]; $$ is not a dollar quote in MySQL, so the FROM is live", got)
	}
	if dq.Writes() {
		t.Errorf("a bare DELETE keyword became a write: effects=%v", dq.Effects)
	}
}

// MySQL needs whitespace after `--`; glued to a token it is two minus signs.
// Applying the Postgres rule comments out the rest of the line, and any
// statement after the semicolon on it is never analyzed.
func TestMySQLDashCommentNeedsWhitespace(t *testing.T) {
	const glued = `SELECT 1--2; DELETE FROM t`

	if got := lexer.Split(glued, lexer.MySQL); len(got) != 2 {
		t.Fatalf("Split = %q, want two statements; `--2` was read as a comment", got)
	}
	if got := writes(lexer.Analyze(glued, lexer.MySQL)); !slices.Equal(got, []string{"t"}) {
		t.Errorf("writes = %v, want [t]", got)
	}

	// Spaced, it is a comment in MySQL too, and must still hide its tail.
	spaced := lexer.Analyze("SELECT 1 -- DROP TABLE t", lexer.MySQL)
	if spaced.Writes() {
		t.Errorf("a spaced -- comment was read as live SQL: %v", spaced.Effects)
	}

	// Postgres has no such requirement: `--2` is a comment there, and this
	// must not have been made a global rule.
	if got := lexer.Split(glued, lexer.Postgres); len(got) != 1 {
		t.Errorf("postgres Split = %q, want one statement; `--` comments unconditionally there", got)
	}
}

func TestClickHouseLexicalRules(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1 # DROP TABLE t",
		"SELECT 1--2; DELETE FROM t",
		`SELECT 'a\'; DELETE FROM t; --'`,
		"SELECT 1 /*! DROP TABLE t */",
	} {
		a := lexer.Analyze(sql, lexer.ClickHouse)
		if a.Writes() {
			t.Errorf("ClickHouse commentary or string content executed as live SQL: effects=%v rels=%v: %s",
				a.Effects, a.Relations, sql)
		}
	}

	for _, sql := range []string{
		"DELETE FROM `select`",
		`DELETE FROM "select"`,
	} {
		a := lexer.Analyze(sql, lexer.ClickHouse)
		if got := writes(a); !slices.Equal(got, []string{"select"}) {
			t.Errorf("writes = %v, want [select]: %s", got, sql)
		}
	}
}

// The negative controls. MySQL's bytes must mean in the other dialects
// exactly what they meant before this dialect existed.
func TestMySQLQuotingDoesNotLeakIntoOtherDialects(t *testing.T) {
	for _, d := range []lexer.Dialect{lexer.Postgres, lexer.MSSQL} {
		// A backtick is not an identifier delimiter in either engine, so
		// the name must NOT be recovered. Recovering it would mean the
		// scanner is inventing relations out of invalid syntax.
		bt := lexer.Analyze("DELETE FROM `select`", d)
		if len(bt.Relations) != 0 {
			t.Errorf("%s: relations = %v, want none; a backtick named a relation", d, bt.Relations)
		}

		// '#' is a live operator in PostgreSQL, not a comment. Treating
		// it as one would swallow the rest of the line — here, the FROM
		// clause that names the table being read.
		h := lexer.Analyze("SELECT 1 # DROP TABLE t", d)
		if got := reads(h); !slices.Equal(got, []string{"t"}) {
			t.Errorf("%s: reads = %v, want [t]; '#' swallowed the rest of the line", d, got)
		}
	}
}

// MySQL EXECUTES the body of `/*! ... */`, so the classifier must read it.
//
// Found against a live MySQL 8.4 relay, not by unit test: `/*! DROP TABLE
// orders */` was forwarded by a lane configured to refuse `drop`, and the
// table was gone. A scanner discarding the body as a comment reports no verb
// at all, so the rule matches nothing and the statement passes.
func TestMySQLExecutableCommentIsLiveSQL(t *testing.T) {
	for _, sql := range []string{
		"/*! DROP TABLE customers */",
		"/*!50000 DROP TABLE customers */",
		"SELECT 1 /*! ; DROP TABLE customers */",
	} {
		t.Run(sql, func(t *testing.T) {
			a := lexer.Analyze(sql, lexer.MySQL)
			if !slices.Contains(a.Effects, lexer.Drop) {
				t.Fatalf("the DROP was read as a comment: effects=%v", a.Effects)
			}
			if w := writes(a); !slices.Contains(w, "customers") {
				t.Errorf("writes = %v, want customers", w)
			}
		})
	}
}

// The other dialects have no executable comment, so the same bytes there are
// ordinary commentary and must not invent a verb.
func TestExecutableCommentIsInertOutsideMySQL(t *testing.T) {
	const sql = "SELECT 1 /*! DROP TABLE customers */"
	for _, d := range []lexer.Dialect{lexer.Postgres, lexer.MSSQL} {
		t.Run(d.String(), func(t *testing.T) {
			a := lexer.Analyze(sql, d)
			if slices.Contains(a.Effects, lexer.Drop) {
				t.Errorf("%s invented a DROP out of a comment: effects=%v",
					d, a.Effects)
			}
		})
	}
}

// An executable comment left open truncates the statement, so the scan must
// fail closed rather than report a clean read of half of it.
func TestMySQLUnterminatedExecutableCommentFailsClosed(t *testing.T) {
	a := lexer.Analyze("SELECT 1 /*! DROP TABLE customers", lexer.MySQL)
	if a.Complete {
		t.Error("Complete = true on an unterminated executable comment")
	}
}

// NO_BACKSLASH_ESCAPES changes where a literal ends, and the classifier
// cannot see the session mode.
//
// Verified server-side on MySQL 8.4: under that mode the literal ends at the
// first quote and the DELETE runs — the row count dropped. Under the default
// the same bytes are one SELECT. Reading only the default hides a live
// DELETE, so both readings are scanned and their effects unioned.
func TestMySQLDeleteHiddenByBackslashModeIsStillFound(t *testing.T) {
	const sql = `SELECT 'a\'; DELETE FROM orders; -- '`

	a := lexer.Analyze(sql, lexer.MySQL)
	if !slices.Contains(a.Effects, lexer.Delete) {
		t.Fatalf("the DELETE is invisible under the default reading and was "+
			"not recovered: effects=%v", a.Effects)
	}
	if w := writes(a); !slices.Contains(w, "orders") {
		t.Errorf("writes = %v, want orders", w)
	}

	// Split must surface it too, or the statement never reaches policy.
	parts := lexer.Split(sql, lexer.MySQL)
	if len(parts) < 2 {
		t.Fatalf("Split returned %d statement(s): %q", len(parts), parts)
	}
}

// The dual reading must not invent effects on ordinary statements: a
// backslash in a literal is common and must stay literal.
func TestMySQLBackslashLiteralStaysLiteral(t *testing.T) {
	a := lexer.Analyze(`SELECT 'C:\path\to\file' FROM customers`, lexer.MySQL)
	if len(a.Effects) != 1 || a.Effects[0] != lexer.Select {
		t.Errorf("effects = %v, want [select]", a.Effects)
	}
	if !a.Complete {
		t.Errorf("Complete = false (%s) on an ordinary backslash literal", a.Reason)
	}
}

// GoogleSQL makes '"' a STRING delimiter, interchangeable with the single
// quote, and spells its identifiers with backticks. Reading "..." under the
// PostgreSQL rule closes the "identifier" at an escaped quote and scans the
// literal's TAIL as live SQL — a delete nobody wrote, refused by a lane that
// should have forwarded a select.
func TestGoogleSQLDoubleQuoteIsAString(t *testing.T) {
	for _, sql := range []string{
		`SELECT "a\";DELETE FROM customers;--"`,
		`SELECT ";DELETE FROM customers;"`,
	} {
		a := lexer.Analyze(sql, lexer.GoogleSQL)
		if a.Writes() {
			t.Errorf("string content executed as live SQL: effects=%v rels=%v: %s", a.Effects, a.Relations, sql)
		}
		if got := a.Severity(); got != lexer.Select {
			t.Errorf("Severity() = %q, want select: %s", got, sql)
		}
		if len(a.Relations) != 0 {
			t.Errorf("relations = %v, want none; the literal leaked a name: %s", a.Relations, sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, sql)
		}
	}

	// The negative control: Postgres must keep reading "..." as a quoted
	// identifier, or `DELETE FROM "select"` loses its relation.
	pg := lexer.Analyze(`DELETE FROM "customers"`, lexer.Postgres)
	if got := writes(pg); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("postgres writes = %v, want [customers]", got)
	}
}

// GoogleSQL escapes a backtick inside an identifier with a BACKSLASH, not
// MySQL's doubling. Under the MySQL rule `a\`b` ends at the escaped backtick
// and the stray tail re-opens quoting over whatever follows.
func TestGoogleSQLBacktickBackslashEscape(t *testing.T) {
	a := lexer.Analyze("DELETE FROM `a\\`b`", lexer.GoogleSQL)
	if got := writes(a); !slices.Equal(got, []string{"a`b"}) {
		t.Errorf("writes = %v, want [a`b]; the backslash escape was not honoured", got)
	}
	if !a.Complete {
		t.Errorf("Complete = false (%s)", a.Reason)
	}

	// MySQL's doubling must survive this flag existing: the two escape
	// styles are per-dialect rows, not a global change of mind.
	my := lexer.Analyze("DELETE FROM `cust``omers`", lexer.MySQL)
	if got := writes(my); !slices.Equal(got, []string{"cust`omers"}) {
		t.Errorf("mysql writes = %v, want [cust`omers]", got)
	}
}

// GoogleSQL quoted identifiers share the string-literal escape table, so
// every spelling below names the relation a table policy was written
// against. A scanner that strips the backslash and keeps the next byte
// verbatim turns `\x63ustomers` into "x63ustomers" — a two-byte bypass of
// a rule protecting customers. Each row pins the DECODED relation name.
func TestGoogleSQLIdentifierEscapesDecode(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want string
	}{
		{"DELETE FROM `a\\\\b`", `a\b`},
		{"DELETE FROM `a\\nb`", "a\nb"},
		{"DELETE FROM `a\\tb`", "a\tb"},
		{"DELETE FROM `a\\'b`", "a'b"},
		{"DELETE FROM `a\\?b`", "a?b"},
		{"DELETE FROM `\\x63ustomers`", "customers"},
		{"DELETE FROM `\\X63ustomers`", "customers"},
		{"DELETE FROM `\\143ustomers`", "customers"},
		{"DELETE FROM `\\u0063ustomers`", "customers"},
		{"DELETE FROM `\\U00000063ustomers`", "customers"},
		{"DELETE FROM `caf\\u00e9`", "café"},
	} {
		a := lexer.Analyze(tc.sql, lexer.GoogleSQL)
		if got := writes(a); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("writes = %q, want [%q]: %s", got, tc.want, tc.sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, tc.sql)
		}
	}
}

// An escape GoogleSQL does not define never yields a guessed byte: the
// relation it would name is one nobody could have written a rule against,
// so the only honest answer is Complete=false and a caller failing closed.
func TestGoogleSQLInvalidIdentifierEscapesFailClosed(t *testing.T) {
	for _, sql := range []string{
		"SELECT x FROM `a\\qb`",         // no such named escape
		"SELECT x FROM `a\\x6z`",        // hex escape cut short
		"SELECT x FROM `a\\x6`",         // hex escape ends at the close
		"SELECT x FROM `a\\400b`",       // octal beyond one byte
		"SELECT x FROM `a\\18b`",        // octal with a non-octal digit
		"SELECT x FROM `a\\uD800b`",     // a lone surrogate is not a rune
		"SELECT x FROM `a\\U00110000b`", // beyond the last code point
	} {
		if a := lexer.Analyze(sql, lexer.GoogleSQL); a.Complete {
			t.Errorf("Complete = true on an invalid identifier escape: %s", sql)
		}
	}
}

// The load-bearing GoogleSQL case. In r'...' a backslash is an ordinary
// byte, so r'\' is a complete literal holding one backslash. A scanner that
// honours the escape swallows the terminator, the literal runs to the next
// quote, and the DELETE between them is never analyzed — a statement
// executing unseen, the misread class this dialect exists to prevent.
func TestGoogleSQLRawStringDoesNotSwallowTheNextStatement(t *testing.T) {
	for _, sql := range []string{
		`SELECT r'\'; DELETE FROM customers; --'`,
		// The byte-raw spellings share the raw rule, in either order.
		`SELECT rb'\'; DELETE FROM customers; --'`,
		`SELECT br"\"; DELETE FROM customers; --"`,
	} {
		if got := lexer.Split(sql, lexer.GoogleSQL); len(got) < 2 {
			t.Errorf("Split = %q, want the DELETE as its own statement: %s", got, sql)
		}
		a := lexer.Analyze(sql, lexer.GoogleSQL)
		if got := writes(a); !slices.Equal(got, []string{"customers"}) {
			t.Errorf("writes = %v, want [customers]; the raw literal ate the DELETE: %s", got, sql)
		}
	}

	// The same bytes WITHOUT the prefix honour the escape, so the literal
	// really does run on and the statement is one harmless select. The
	// prefix alone decides where the literal ends.
	plain := lexer.Analyze(`SELECT 'a\'; DELETE FROM customers; --'`, lexer.GoogleSQL)
	if plain.Writes() {
		t.Errorf("escaped literal executed its content: effects=%v rels=%v", plain.Effects, plain.Relations)
	}
	if !plain.Complete {
		t.Errorf("Complete = false (%s); the default escape reading is unambiguous", plain.Reason)
	}
}

// An unterminated raw string is a truncated statement: what follows the
// missing quote is unknown, and the only honest answer is Complete=false.
func TestGoogleSQLUnterminatedRawStringFailsClosed(t *testing.T) {
	for _, sql := range []string{
		`SELECT r'abc`,
		`SELECT rb"abc`,
		`SELECT r'''abc`,
	} {
		if a := lexer.Analyze(sql, lexer.GoogleSQL); a.Complete {
			t.Errorf("Complete = true on an unterminated raw string: %s", sql)
		}
	}
}

// A triple-quoted body is data. It may contain bare quotes, so a scanner
// without the form closes the literal two quotes early and classifies its
// BODY: a DELETE inside a triple-quoted literal must not reach policy as a
// delete.
func TestGoogleSQLTripleQuotedBodiesAreData(t *testing.T) {
	for _, sql := range []string{
		`SELECT '''; DELETE FROM customers; '''`,
		`SELECT """; DELETE FROM customers; """`,
	} {
		a := lexer.Analyze(sql, lexer.GoogleSQL)
		if a.Writes() {
			t.Errorf("triple-quoted content executed as live SQL: effects=%v rels=%v: %s", a.Effects, a.Relations, sql)
		}
		if got := a.Severity(); got != lexer.Select {
			t.Errorf("Severity() = %q, want select: %s", got, sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, sql)
		}
	}

	// Raw-ness composes with the triple form: in r'''...''' the backslash
	// before the closing quotes is literal, so the literal CLOSES and the
	// DELETE after it is live. The escaping read runs past the close and
	// loses it.
	raw := lexer.Analyze(`SELECT r'''a\'''; DELETE FROM customers`, lexer.GoogleSQL)
	if got := writes(raw); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("writes = %v, want [customers]; the raw triple swallowed the DELETE", got)
	}

	// And unterminated fails closed, same as every other literal form.
	if a := lexer.Analyze(`SELECT '''abc`, lexer.GoogleSQL); a.Complete {
		t.Errorf("Complete = true on an unterminated triple-quoted string")
	}
}

// ZetaSQL comments: '--' and '#' to end of line, '/* */' NOT nested. Each
// reading has a misread on the other side — '--' needing whitespace would
// hide nothing here but a nested read of /* */ files a live DELETE as
// commentary.
func TestGoogleSQLComments(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1 # DROP TABLE t",
		"SELECT 1 -- DROP TABLE t",
		// Unlike MySQL, '--' opens a comment even glued to a token, so
		// the tail is commentary and this is ONE harmless select.
		"SELECT 1--2; DELETE FROM t",
	} {
		a := lexer.Analyze(sql, lexer.GoogleSQL)
		if a.Writes() {
			t.Errorf("commented-out SQL executed: effects=%v rels=%v: %s", a.Effects, a.Relations, sql)
		}
		if got := a.Severity(); got != lexer.Select {
			t.Errorf("Severity() = %q, want select: %s", got, sql)
		}
	}

	// The standard-conforming close: the FIRST */ ends the comment and the
	// DELETE after it is live SQL, exactly as in MySQL.
	flat := lexer.Analyze(`/* a /* b */ DELETE FROM t */`, lexer.GoogleSQL)
	if got := writes(flat); !slices.Equal(got, []string{"t"}) {
		t.Errorf("writes = %v, want [t]; the block comment was read as nested", got)
	}
}

// Bytes that quote something in another dialect are inert in ZetaSQL.
// Honouring PostgreSQL's dollar quote here would hide the statement after
// the semicolon inside a phantom literal, and honouring MySQL's executable
// comment would invent a verb out of commentary.
func TestGoogleSQLDoesNotBorrowOtherDialectsQuoting(t *testing.T) {
	a := lexer.Analyze(`SELECT $$x$$; DELETE FROM customers`, lexer.GoogleSQL)
	if got := writes(a); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("writes = %v, want [customers]; a dollar quote swallowed the DELETE", got)
	}

	exec := lexer.Analyze(`/*! DROP TABLE t */ SELECT 1`, lexer.GoogleSQL)
	if exec.Writes() {
		t.Errorf("executable-comment body classified: effects=%v rels=%v", exec.Effects, exec.Relations)
	}
	if got := exec.Severity(); got != lexer.Select {
		t.Errorf("Severity() = %q, want select", got)
	}
}

// '@' is punctuation in GoogleSQL: @param binds a parameter and @{...} is
// an optimizer hint. Neither is an effect, and neither may cost the
// statement its classification — a hint glued to a relation must not eat
// the name, and a statement-level hint must not leave the SELECT after it
// headless, which would classify the statement as nothing and refuse it on
// a lane that forwards selects.
func TestGoogleSQLHintsAndParametersAreInert(t *testing.T) {
	glued := lexer.Analyze("SELECT x FROM t@{FORCE_INDEX=idx}", lexer.GoogleSQL)
	if got := reads(glued); !slices.Equal(got, []string{"t"}) {
		t.Errorf("reads = %v, want [t]; the hint ate the relation", got)
	}
	if got := glued.Severity(); got != lexer.Select {
		t.Errorf("Severity() = %q, want select", got)
	}

	stmt := lexer.Analyze("@{USE_ADDITIONAL_PARALLELISM=TRUE} SELECT x FROM t", lexer.GoogleSQL)
	if got := stmt.Severity(); got != lexer.Select {
		t.Errorf("Severity() = %q, want select; the hint cost the SELECT its head position", got)
	}
	if got := reads(stmt); !slices.Equal(got, []string{"t"}) {
		t.Errorf("reads = %v, want [t]", got)
	}
	if !stmt.Complete {
		t.Errorf("Complete = false (%s)", stmt.Reason)
	}

	param := lexer.Analyze("SELECT x FROM t WHERE id = @id", lexer.GoogleSQL)
	if got := reads(param); !slices.Equal(got, []string{"t"}) || param.Severity() != lexer.Select {
		t.Errorf("parameter binding disturbed the analysis: reads=%v severity=%q", got, param.Severity())
	}

	// Half a hint is half a statement: the missing brace would have
	// preceded SQL this scan never saw.
	if a := lexer.Analyze("@{FORCE_INDEX=idx SELECT 1", lexer.GoogleSQL); a.Complete {
		t.Errorf("Complete = true on an unterminated hint")
	}
}

// The GoogleSQL DML spellings policy must see through: the OR UPDATE/OR
// IGNORE insert modifiers and the THEN RETURN clause. Each row pins the verb
// AND the written relation, because losing the target is how a `tables:`
// rule stops firing.
func TestGoogleSQLDMLShapes(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		verb lexer.Verb
		want []string
	}{
		{"INSERT OR UPDATE INTO t (a) VALUES (1)", lexer.Insert, []string{"t"}},
		{"INSERT OR IGNORE INTO t (a) VALUES (1)", lexer.Insert, []string{"t"}},
		{"INSERT INTO t (a) VALUES (1) THEN RETURN id", lexer.Insert, []string{"t"}},
		{"UPDATE t SET a = 1 WHERE b = 2 THEN RETURN *", lexer.Update, []string{"t"}},
		{"DELETE FROM t WHERE a = 1 THEN RETURN *", lexer.Delete, []string{"t"}},
	} {
		a := lexer.Analyze(tc.sql, lexer.GoogleSQL)
		if got := a.Severity(); got != tc.verb {
			t.Errorf("Severity() = %q, want %q: %s", got, tc.verb, tc.sql)
		}
		if got := writes(a); !slices.Equal(got, tc.want) {
			t.Errorf("writes = %v, want %v: %s", got, tc.want, tc.sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, tc.sql)
		}
	}

	// GoogleSQL has no data-modifying CTE, so plain WITH ... SELECT is the
	// only WITH shape and it stays a select.
	with := lexer.Analyze("WITH x AS (SELECT 1 FROM t) SELECT * FROM x", lexer.GoogleSQL)
	if got := with.Severity(); got != lexer.Select {
		t.Errorf("Severity() = %q, want select", got)
	}
	if got := reads(with); !slices.Equal(got, []string{"t"}) {
		t.Errorf("reads = %v, want [t]; the CTE alias leaked or the base table was lost", got)
	}
}

// Oracle's alternative quoting runs a literal past any single quote, up to
// the closing delimiter followed by a quote. Read as plain '...', the quote
// INSIDE q'[']' closes a literal, the next one opens another, and the
// DELETE between them is swallowed as string data: a statement executing
// unseen. Each delimiter form is pinned, because the four brackets close
// with their mirror and everything else with itself.
func TestOracleAlternativeQuotingDoesNotSwallowTheNextStatement(t *testing.T) {
	for _, sql := range []string{
		`SELECT q'[']' FROM dual; DELETE FROM customers; --'`,
		`SELECT q'{'}' FROM dual; DELETE FROM customers; --'`,
		`SELECT q'(')' FROM dual; DELETE FROM customers; --'`,
		`SELECT q'<'>' FROM dual; DELETE FROM customers; --'`,
		`SELECT q'!'!' FROM dual; DELETE FROM customers; --'`,
		`SELECT Nq'#'#' FROM dual; DELETE FROM customers; --'`,
	} {
		a := lexer.Analyze(sql, lexer.Oracle)
		if got := writes(a); !slices.Equal(got, []string{"customers"}) {
			t.Errorf("writes = %v, want [customers]; the q-quote ate the DELETE: %s", got, sql)
		}
	}
}

// The inverse: SQL inside a q-quoted literal is data. A bracket delimiter
// closes only before a quote, so q'(a(b)c)' is a(b)c, verified on 23ai, and
// a multibyte delimiter closes on the whole character.
func TestOracleAlternativeQuotedBodiesAreData(t *testing.T) {
	for _, sql := range []string{
		`SELECT q'[it's; DELETE FROM customers; --]' FROM dual`,
		`SELECT q'(a(b); DELETE FROM customers; c)' FROM dual`,
		`SELECT q'!DELETE FROM customers; '!' FROM dual`,
		`SELECT nq'{'; DROP TABLE customers}' FROM dual`,
		"SELECT q'Ä'; DELETE FROM customers; Ä' FROM dual",
	} {
		a := lexer.Analyze(sql, lexer.Oracle)
		if a.Writes() || !a.Complete {
			t.Errorf("q-quoted content executed or misread: effects=%v rels=%v complete=%v (%s): %s",
				a.Effects, a.Relations, a.Complete, a.Reason, sql)
		}
		if got := reads(a); !slices.Equal(got, []string{"dual"}) {
			t.Errorf("reads = %v, want [dual]; literal content leaked a relation: %s", got, sql)
		}
	}
}

// A q-quote Oracle refuses has no close to look for: whitespace and the quote
// itself are rejected by the server (ORA-00911, ORA-01756 on 23ai), and an
// unterminated one hides what the missing close would have ended. Both are
// Complete=false rather than a guessed boundary.
func TestOracleUnreadableAlternativeQuotesFailClosed(t *testing.T) {
	for _, sql := range []string{
		`SELECT q'[abc FROM dual`,
		`SELECT q'[abc]' ' FROM dual`,
		`SELECT q' a ' FROM dual`,
		`SELECT q'''a''' FROM dual`,
		`SELECT q'`,
	} {
		if a := lexer.Analyze(sql, lexer.Oracle); a.Complete {
			t.Errorf("Complete = true on an unreadable q-quote: %s", sql)
		}
	}
}

// A q-quote is Oracle's alone. In PostgreSQL q'...' is an identifier
// followed by a string, and reading it Oracle's way there would move a
// statement boundary.
func TestOracleQuotingDoesNotLeakIntoOtherDialects(t *testing.T) {
	a := lexer.Analyze(`SELECT q'[x' AS a; DELETE FROM customers; --]'`, lexer.Postgres)
	if got := writes(a); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("postgres writes = %v, want [customers]; q'[ opened a literal outside Oracle", got)
	}
	if got := lexer.Analyze("BEGIN", lexer.Postgres).Verb; got != lexer.Begin {
		t.Errorf("postgres BEGIN = %q, want begin; the PL/SQL reading leaked", got)
	}
}

// Oracle's lexical differences from PostgreSQL, each of which moves a
// statement boundary or a relation name. Block comments close at the FIRST
// `*/` (verified on 23ai), so the DELETE after an inner close is live; a
// backslash is an ordinary byte in '...', so 'a\' is a whole literal; and
// `--` comments even when glued to a token.
func TestOracleCommentsAndStrings(t *testing.T) {
	for _, sql := range []string{
		`SELECT 1 FROM dual /* a /* b */; DELETE FROM customers; --*/`,
		`SELECT 'a\' FROM dual; DELETE FROM customers; --'`,
	} {
		if got := writes(lexer.Analyze(sql, lexer.Oracle)); !slices.Equal(got, []string{"customers"}) {
			t.Errorf("writes = %v, want [customers]: %s", got, sql)
		}
	}
	for _, sql := range []string{
		"SELECT 1--2; DELETE FROM customers\nFROM dual",
		`SELECT /*+ INDEX(c ix) */ * FROM dual /*; DELETE FROM customers */`,
		"SELECT 1 FROM dual --+ ; DELETE FROM customers",
	} {
		if a := lexer.Analyze(sql, lexer.Oracle); a.Writes() {
			t.Errorf("commentary executed: effects=%v rels=%v: %s", a.Effects, a.Relations, sql)
		}
	}
	hint := lexer.Analyze(`DELETE /*+ FULL(c) */ FROM customers c WHERE id = :1`, lexer.Oracle)
	if got := writes(hint); !slices.Equal(got, []string{"customers"}) || hint.Severity() != lexer.Delete {
		t.Errorf("hinted delete: writes = %v, severity = %q", got, hint.Severity())
	}
}

// Oracle's catalog spells relations with `$` and `#`, and quoted parts keep
// their case. A relation reported as "v" instead of v$session is one no rule
// names.
func TestOracleRelationNames(t *testing.T) {
	for _, tc := range []struct {
		sql          string
		reads, write []string
	}{
		{`SELECT sid FROM v$session`, []string{"v$session"}, nil},
		{`DELETE FROM app.audit#log WHERE id = :1`, nil, []string{"app.audit#log"}},
		{`SELECT * FROM "HR"."Emp" e`, []string{"HR.Emp"}, nil},
		{`SELECT sysdate FROM dual`, []string{"dual"}, nil},
		// A bind may spell a non-reserved keyword (:using binds on 23ai);
		// read as a word it opened a relation list mid-predicate.
		{`SELECT a FROM t WHERE x = :using AND y = :join`, []string{"t"}, nil},
	} {
		a := lexer.Analyze(tc.sql, lexer.Oracle)
		if got := reads(a); !slices.Equal(got, tc.reads) {
			t.Errorf("reads = %v, want %v: %s", got, tc.reads, tc.sql)
		}
		if got := writes(a); !slices.Equal(got, tc.write) {
			t.Errorf("writes = %v, want %v: %s", got, tc.write, tc.sql)
		}
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, tc.sql)
		}
	}
}

// A database link names the remote OBJECT, and the object is what a rule is
// written against: a loopback link reaches the same table. The link must
// neither hide the write nor end the relation list.
func TestOracleDatabaseLinks(t *testing.T) {
	for _, tc := range []struct {
		sql          string
		verb         lexer.Verb
		reads, write []string
	}{
		{`DELETE FROM hr.emp@loop WHERE id = :1`, lexer.Delete, nil, []string{"hr.emp"}},
		{`INSERT INTO emp@hq.example.com SELECT * FROM staging`, lexer.Insert, []string{"staging"}, []string{"emp"}},
		{`UPDATE emp@hq@hr SET sal = 1`, lexer.Update, nil, []string{"emp"}},
		{`SELECT * FROM emp@remote, dept WHERE 1 = 1`, lexer.Select, []string{"emp", "dept"}, nil},
	} {
		a := lexer.Analyze(tc.sql, lexer.Oracle)
		if got := a.Severity(); got != tc.verb {
			t.Errorf("Severity() = %q, want %q: %s", got, tc.verb, tc.sql)
		}
		if got := reads(a); !slices.Equal(got, tc.reads) {
			t.Errorf("reads = %v, want %v: %s", got, tc.reads, tc.sql)
		}
		if got := writes(a); !slices.Equal(got, tc.write) {
			t.Errorf("writes = %v, want %v: %s", got, tc.write, tc.sql)
		}
	}
}

// A PL/SQL anonymous block is DO $$...$$ without the quotes: procedural code
// whose effects the scanner cannot run. It must never classify as a read or
// as a transaction BEGIN, and the DML the scanner does see — including DML
// PL/SQL puts where SQL never does, after FORALL or LOOP — is reported with
// its target written.
func TestOraclePLSQLBlocksAreOpaque(t *testing.T) {
	for _, tc := range []struct {
		sql   string
		write []string
	}{
		{`BEGIN DELETE FROM customers; END;`, []string{"customers"}},
		{`BEGIN EXECUTE IMMEDIATE 'DELETE FROM customers'; END;`, nil},
		{`DECLARE n NUMBER; BEGIN SELECT count(*) INTO n FROM t; END;`, nil},
		{`DECLARE TYPE ids IS TABLE OF NUMBER; v ids; BEGIN FORALL i IN 1 .. v.count DELETE FROM customers WHERE id = v(i); END;`, []string{"customers"}},
		{`BEGIN FOR r IN (SELECT id FROM t) LOOP UPDATE customers SET x = 1 WHERE id = r.id; END LOOP; END;`, []string{"customers"}},
		{`BEGIN UPDATE customers SET x = 1 RETURNING x INTO v; END;`, []string{"customers"}},
		{`BEGIN pkg.purge; END;`, nil},
	} {
		a := lexer.Analyze(tc.sql, lexer.Oracle)
		if a.Complete {
			t.Errorf("Complete = true on a PL/SQL block: %s", tc.sql)
		}
		if a.Verb != lexer.Call {
			t.Errorf("Verb = %q, want call: %s", a.Verb, tc.sql)
		}
		if slices.Contains(a.Effects, lexer.Begin) || slices.Contains(a.Effects, lexer.Commit) {
			t.Errorf("effects = %v; BEGIN/END read as a transaction: %s", a.Effects, tc.sql)
		}
		// Variables after INTO are not relations: a spurious write of n
		// or v is a rule acting on nothing.
		if got := writes(a); !slices.Equal(got, tc.write) {
			t.Errorf("writes = %v, want %v: %s", got, tc.write, tc.sql)
		}
	}
}

// CREATE of a stored unit carries its PL/SQL body inline, and the body is
// source Oracle compiles, not runs — verified on 23ai: a trailing DELETE
// after END; is stored as a compilation error and deletes nothing. It is one
// create, complete, and a trigger records the table it fires on.
func TestOracleStoredUnitBodiesAreData(t *testing.T) {
	for _, tc := range []struct {
		sql   string
		write []string
	}{
		{`CREATE OR REPLACE PROCEDURE purge_all AS BEGIN DELETE FROM customers; END;`, nil},
		{`CREATE OR REPLACE EDITIONABLE FUNCTION f (x NUMBER) RETURN NUMBER IS BEGIN RETURN x; END; DELETE FROM customers`, nil},
		{`CREATE OR REPLACE PACKAGE BODY pkg AS PROCEDURE p IS BEGIN DELETE FROM customers; END; END pkg;`, nil},
		{`CREATE TYPE point AS OBJECT (x NUMBER, y NUMBER)`, nil},
		{`CREATE OR REPLACE TRIGGER trg BEFORE INSERT OR UPDATE OF sal ON hr.emp FOR EACH ROW WHEN (new.sal > 0) BEGIN DELETE FROM customers; END;`, []string{"hr.emp"}},
		{`CREATE TRIGGER trg INSTEAD OF INSERT ON NESTED TABLE lines OF orders_v FOR EACH ROW BEGIN NULL; END;`, []string{"orders_v"}},
		{`CREATE TRIGGER trg AFTER LOGON ON DATABASE BEGIN DELETE FROM customers; END;`, nil},
	} {
		a := lexer.Analyze(tc.sql, lexer.Oracle)
		if !a.Complete {
			t.Errorf("Complete = false (%s): %s", a.Reason, tc.sql)
		}
		if !slices.Equal(a.Effects, []lexer.Verb{lexer.Create}) {
			t.Errorf("effects = %v, want [create]; the body was read as SQL: %s", a.Effects, tc.sql)
		}
		if got := writes(a); !slices.Equal(got, tc.write) {
			t.Errorf("writes = %v, want %v: %s", got, tc.write, tc.sql)
		}
	}
}

// Oracle statement forms and the verb each must report. Every write form
// pins its written relation, because losing the target is how a `tables:`
// rule stops firing; the read forms pin that nothing is written.
func TestOracleStatementForms(t *testing.T) {
	for _, tc := range []struct {
		sql      string
		verb     lexer.Verb
		write    []string
		complete bool
	}{
		{`SELECT * FROM t FOR UPDATE OF c NOWAIT`, lexer.Select, nil, true},
		{`MERGE INTO t USING s ON (t.id = s.id) WHEN MATCHED THEN UPDATE SET t.a = s.a WHEN NOT MATCHED THEN INSERT (id) VALUES (s.id)`, lexer.Merge, []string{"t"}, true},
		// Multi-table insert names a target after EVERY INTO.
		{`INSERT ALL INTO a VALUES (1) INTO b VALUES (2) SELECT * FROM dual`, lexer.Insert, []string{"a", "b"}, true},
		{`INSERT ALL WHEN returning > 1 THEN INTO b VALUES (1) SELECT 1 returning FROM dual`, lexer.Insert, []string{"b"}, true},
		{`INSERT INTO t (a) VALUES (:1) RETURNING id INTO :2`, lexer.Insert, []string{"t"}, true},
		{`TRUNCATE TABLE t`, lexer.Truncate, []string{"t"}, true},
		{`FLASHBACK TABLE t TO BEFORE DROP`, lexer.Alter, []string{"t"}, true},
		{`FLASHBACK TABLE t TO TIMESTAMP SYSTIMESTAMP - INTERVAL '1' HOUR`, lexer.Alter, []string{"t"}, true},
		{`PURGE TABLE t`, lexer.Drop, []string{"t"}, true},
		{`PURGE RECYCLEBIN`, lexer.Drop, nil, true},
		{`ALTER SYSTEM KILL SESSION '12,34'`, lexer.Alter, nil, true},
		{`ALTER TABLE t ADD (c NUMBER)`, lexer.Alter, []string{"t"}, true},
		{`GRANT SELECT ON hr.emp TO app`, lexer.Grant, []string{"hr.emp"}, true},
		{`REVOKE SELECT ON hr.emp FROM app`, lexer.Revoke, []string{"hr.emp"}, true},
		{`CREATE OR REPLACE VIEW v AS SELECT * FROM t`, lexer.Create, []string{"v"}, true},
		{`CALL pkg.purge()`, lexer.Call, nil, false},
		{`EXEC pkg.purge`, lexer.Call, nil, false},
	} {
		a := lexer.Analyze(tc.sql, lexer.Oracle)
		if got := a.Severity(); got != tc.verb {
			t.Errorf("Severity() = %q, want %q: %s", got, tc.verb, tc.sql)
		}
		if got := writes(a); !slices.Equal(got, tc.write) {
			t.Errorf("writes = %v, want %v: %s", got, tc.write, tc.sql)
		}
		if a.Complete != tc.complete {
			t.Errorf("Complete = %v (%s), want %v: %s", a.Complete, a.Reason, tc.complete, tc.sql)
		}
	}
}

// ALTER SESSION is Oracle's SET — NLS formats, CURRENT_SCHEMA, time zone —
// and drivers may send it on connect. Filed under alter, a lane refusing DDL
// refuses the login. ALTER SYSTEM changes the instance and stays alter.
func TestOracleAlterSessionIsASet(t *testing.T) {
	for _, sql := range []string{
		`ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD'`,
		`ALTER SESSION SET CURRENT_SCHEMA = hr`,
	} {
		a := lexer.Analyze(sql, lexer.Oracle)
		if !slices.Equal(a.Effects, []lexer.Verb{lexer.Set}) || a.Writes() {
			t.Errorf("effects = %v, want [set]: %s", a.Effects, sql)
		}
	}
	if got := lexer.Analyze(`ALTER SESSION SET x = 1`, lexer.Postgres).Verb; got != lexer.Alter {
		t.Errorf("postgres ALTER SESSION = %q, want alter; the Oracle reading leaked", got)
	}
}

// Oracle runs one statement per call, so Split never cuts its text: the
// only inner semicolons are PL/SQL's, and a fragment of a block loses the
// context that shows its DML. Each call below runs a write that its
// fragments would hide; read whole, the write is an effect.
func TestOracleSplitKeepsTheWholeCall(t *testing.T) {
	for _, sql := range []string{
		`BEGIN DELETE FROM customers; END;`,
		`<<x>> BEGIN DELETE FROM customers; END;`,
		`<<outer>> DECLARE n NUMBER; BEGIN UPDATE customers SET ssn = NULL; END;`,
		`DECLARE TYPE ids IS TABLE OF NUMBER; v ids; BEGIN FORALL i IN 1 .. v.count DELETE FROM customers WHERE id = v(i); END;`,
		`UPDATE /*+ WITH_PLSQL */ audit_t SET n = (WITH FUNCTION f RETURN NUMBER IS BEGIN NULL; FORALL i IN 1 .. 3 DELETE FROM customers WHERE id = i; RETURN 1; END; SELECT f() FROM dual)`,
	} {
		parts := lexer.Split("  "+sql+"\n", lexer.Oracle)
		if !slices.Equal(parts, []string{sql}) {
			t.Fatalf("Split = %q, want the whole call", parts)
		}
		a := lexer.Analyze(parts[0], lexer.Oracle)
		if a.Complete || !slices.Contains(writes(a), "customers") {
			t.Errorf("complete = %v, writes = %v, effects = %v: %s", a.Complete, writes(a), a.Effects, sql)
		}
	}
	if got := lexer.Split(" ; ", lexer.Oracle); !slices.Equal(got, []string{";"}) {
		t.Errorf("Split = %q", got)
	}
	if got := lexer.Split("   ", lexer.Oracle); len(got) != 0 {
		t.Errorf("Split of blank text = %q", got)
	}
}

// WITH FUNCTION / WITH PROCEDURE declares PL/SQL the query runs, and an
// autonomous function can call a procedure that writes. The statement is
// opaque, and the DELETE in the procedure is one of its effects. A CTE that
// is merely named function stays a plain, complete query.
func TestOracleInlinePLSQLInWithIsOpaque(t *testing.T) {
	inline := `WITH PROCEDURE p IS BEGIN DELETE FROM customers; END; FUNCTION f RETURN NUMBER IS PRAGMA AUTONOMOUS_TRANSACTION; BEGIN p; COMMIT; RETURN 1; END; SELECT f FROM dual`
	a := lexer.Analyze(inline, lexer.Oracle)
	if a.Complete || !slices.Contains(a.Effects, lexer.Delete) {
		t.Errorf("inline PL/SQL: complete = %v, effects = %v", a.Complete, a.Effects)
	}
	if got := writes(a); !slices.Equal(got, []string{"customers"}) {
		t.Errorf("writes = %v, want [customers]", got)
	}
	for _, sql := range []string{
		`WITH function AS (SELECT 1 x FROM dual) SELECT x FROM function`,
		`WITH function (a) AS (SELECT 1 FROM dual) SELECT a FROM function`,
	} {
		cte := lexer.Analyze(sql, lexer.Oracle)
		if !cte.Complete || cte.Verb != lexer.Select {
			t.Errorf("CTE named function: verb = %v, complete = %v (%s): %s", cte.Verb, cte.Complete, cte.Reason, sql)
		}
	}
}

// Inside PL/SQL a DML word is a statement only where one can start. A member
// call (`v.DELETE`) and the row lock of `FOR UPDATE` write nothing, and
// reading them as DML lets a rule naming delete or update refuse the block.
// Real DML around them is still an effect.
func TestOraclePLSQLMemberCallsAndRowLocksAreNotDML(t *testing.T) {
	for _, tc := range []struct {
		sql     string
		effect  lexer.Verb
		present bool
		write   []string
	}{
		{`BEGIN v.DELETE; END;`, lexer.Delete, false, nil},
		{`BEGIN v_tab.DELETE(1); r.update; END;`, lexer.Delete, false, nil},
		{`DECLARE CURSOR c IS SELECT * FROM t FOR UPDATE OF x; BEGIN NULL; END;`, lexer.Update, false, nil},
		{`BEGIN SELECT a INTO v FROM t WHERE id = 1 FOR UPDATE; END;`, lexer.Update, false, nil},
		{`BEGIN v.DELETE; DELETE FROM customers; END;`, lexer.Delete, true, []string{"customers"}},
		{`BEGIN FOR r IN (SELECT id FROM t FOR UPDATE) LOOP UPDATE customers SET x = 1 WHERE id = r.id; END LOOP; END;`, lexer.Update, true, []string{"customers"}},
	} {
		a := lexer.Analyze(tc.sql, lexer.Oracle)
		if slices.Contains(a.Effects, tc.effect) != tc.present {
			t.Errorf("effects = %v, %s present = %v: %s", a.Effects, tc.effect, !tc.present, tc.sql)
		}
		if got := writes(a); !slices.Equal(got, tc.write) {
			t.Errorf("writes = %v, want %v: %s", got, tc.write, tc.sql)
		}
		for _, r := range a.Relations {
			if r.Name == "of" {
				t.Errorf("relation %q read from FOR UPDATE OF: %s", r.Name, tc.sql)
			}
		}
	}
}

// RENAME old TO new removes a table from under its name. It is an alter
// that writes the renamed table, so drop/alter and table rules see it.
func TestOracleRenameIsAnAlterOfItsTable(t *testing.T) {
	for _, sql := range []string{`RENAME customers TO c_old`, `rename CUSTOMERS to c_old`} {
		a := lexer.Analyze(sql, lexer.Oracle)
		if a.Verb != lexer.Alter || !slices.Equal(writes(a), []string{"customers"}) || !a.Complete {
			t.Errorf("verb = %v, writes = %v, complete = %v: %s", a.Verb, writes(a), a.Complete, sql)
		}
	}
}

// Spark carries a `--` comment over a backslash-newline, so the quote on the
// next line is commentary and the DROP after it runs. Verified on a
// Databricks SQL warehouse: this dropped the table. Only LF continues: before
// CR LF the backslash is an ordinary byte.
func TestSparkDashCommentContinuesPastBackslashNewline(t *testing.T) {
	const bypass = "-- x \\\nSELECT '\nDROP TABLE t -- '"
	a := lexer.Analyze(bypass, lexer.Spark)
	if a.Severity() != lexer.Drop || !slices.Equal(writes(a), []string{"t"}) || !a.Complete {
		t.Errorf("severity = %q, writes = %v, complete = %v; the DROP hid in a literal", a.Severity(), writes(a), a.Complete)
	}
	if a := lexer.Analyze(bypass, lexer.Postgres); a.Severity() != lexer.Select {
		t.Errorf("postgres severity = %q; the continuation leaked out of Spark", a.Severity())
	}

	for _, tc := range []struct {
		sql  string
		want lexer.Verb
	}{
		{"SELECT 1 AS x -- note \\\n; DELETE FROM t", lexer.Select},
		{"SELECT 1 AS x -- a \\\n b \\\n; DELETE FROM t", lexer.Select},
		{"SELECT 1 AS x -- note \\\\\n; DELETE FROM t", lexer.Select},
		{"SELECT 1 AS x -- note \\\r\n; DELETE FROM t", lexer.Delete},
		{"SELECT 1 AS x -- note \\ \n; DELETE FROM t", lexer.Delete},
		// A lone CR ends the comment too, as LF does (SIMPLE_COMMENT).
		{"SELECT 1 AS x -- note\r; DELETE FROM t", lexer.Delete},
		{"-- x\rDROP TABLE t", lexer.Drop},
		{"-- x\rVACUUM t", lexer.Delete},
	} {
		if got := lexer.Analyze(tc.sql, lexer.Spark).Severity(); got != tc.want {
			t.Errorf("severity = %q, want %q: %q", got, tc.want, tc.sql)
		}
	}
}

// Spark's whitespace includes the Unicode spaces of its WS rule, so a
// no-break space between DROP and TABLE is two words to Spark and one to
// every other dialect here. Verified on a Databricks SQL warehouse: the table
// was dropped. A zero-width space and U+2029 are not in the rule; splitting
// on them would invent verbs out of identifiers.
func TestSparkUnicodeSpaceSeparatesWords(t *testing.T) {
	for _, sp := range []string{"\u00a0", "\u1680", "\u2000", "\u2005", "\u200a", "\u2028", "\u202f", "\u205f", "\u3000"} {
		sql := "DROP" + sp + "TABLE" + sp + "t"
		a := lexer.Analyze(sql, lexer.Spark)
		if a.Severity() != lexer.Drop || !slices.Equal(writes(a), []string{"t"}) {
			t.Errorf("U+%04X: severity = %q, writes = %v", []rune(sp)[0], a.Severity(), writes(a))
		}
		if got := lexer.Analyze(sql, lexer.Postgres).Severity(); got == lexer.Drop {
			t.Errorf("U+%04X: postgres reads a drop; Unicode spaces leaked out of Spark", []rune(sp)[0])
		}
	}
	for _, sp := range []string{"\u200b", "\u2029", "\u0085", "\ufeff"} {
		if got := lexer.Analyze("DROP"+sp+"TABLE t", lexer.Spark).Severity(); got == lexer.Drop {
			t.Errorf("U+%04X is not Spark whitespace, but split the word into a drop", []rune(sp)[0])
		}
	}
	if a := lexer.Analyze("SELECT a\u3000FROM t", lexer.Spark); !slices.Equal(reads(a), []string{"t"}) {
		t.Errorf("reads = %v, want [t]", reads(a))
	}
}

// A `/*+ ... */` hint is live tokens in Spark: a string inside it may hold
// `*/`, and a scanner that reads the hint as a comment ends it there and
// opens a literal on the quote after it. Verified on a Databricks SQL
// warehouse: the script below dropped the table.
func TestSparkHintBodyIsLive(t *testing.T) {
	a := lexer.Analyze("BEGIN SELECT /*+ X('*/') */ 1; DROP TABLE t; END; --'", lexer.Spark)
	if !slices.Contains(a.Effects, lexer.Drop) || !slices.Equal(writes(a), []string{"t"}) {
		t.Errorf("effects = %v, writes = %v; the DROP hid behind the hint", a.Effects, writes(a))
	}
	if a := lexer.Analyze("SELECT /*+ X('*/') */ * FROM t --'", lexer.Spark); !slices.Equal(reads(a), []string{"t"}) || !a.Complete {
		t.Errorf("reads = %v, complete = %v; the hint swallowed the FROM", reads(a), a.Complete)
	}
	for _, sql := range []string{
		"SELECT /*+ BROADCAST(s) */ * FROM t JOIN s ON t.id = s.id",
		"SELECT /*+ MERGE(t, s) */ * FROM t JOIN s ON t.id = s.id",
		"SELECT /*+ REPARTITION(3) /* why */ */ * FROM t",
		"INSERT /*+ REBALANCE */ INTO t SELECT * FROM s",
	} {
		a := lexer.Analyze(sql, lexer.Spark)
		if !a.Complete || slices.Contains(a.Effects, lexer.Merge) || slices.Contains(a.Effects, lexer.Delete) {
			t.Errorf("complete = %v (%s), effects = %v: %s", a.Complete, a.Reason, a.Effects, sql)
		}
	}
	if a := lexer.Analyze("SELECT /*+ REPARTITION(3) FROM t", lexer.Spark); a.Complete {
		t.Error("Complete = true with the hint never closed")
	}
	// Elsewhere `/*+` is an ordinary comment.
	if a := lexer.Analyze("SELECT 1 /*+ DROP TABLE t */", lexer.Postgres); a.Severity() != lexer.Select {
		t.Errorf("postgres severity = %q; the live hint leaked out of Spark", a.Severity())
	}
}

// Spark replaces ${var} in the raw text before it parses, inside literals
// too, so the statement it runs is one this scanner never saw.
func TestSparkVariableSubstitutionFailsClosed(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM ${tbl}",
		"SELECT '${x}' AS y",
		"SELECT 1 -- ${x}",
		"/* ${x} */ SELECT 1",
	} {
		if a := lexer.Analyze(sql, lexer.Spark); a.Complete {
			t.Errorf("Complete = true with a substitution: %s", sql)
		}
	}
	for _, sql := range []string{"SELECT '$x' AS y", "SELECT '{x}' AS y", "SELECT $$it's$$ AS y"} {
		if a := lexer.Analyze(sql, lexer.Spark); !a.Complete {
			t.Errorf("Complete = false (%s) without a substitution: %s", a.Reason, sql)
		}
	}
	if a := lexer.Analyze("SELECT '${x}' AS y", lexer.Postgres); !a.Complete {
		t.Error("postgres conceded on ${; the substitution leaked out of Spark")
	}
}

// Spark nests block comments. In the first statement the DROP sits after
// the comment closes, and Spark ran it on a Databricks SQL warehouse; a flat
// reader ends the comment early and the quote hides the DROP. In the second
// the DELETE is inside the comment.
func TestSparkBlockCommentsNest(t *testing.T) {
	a := lexer.Analyze("/* /* */ SELECT 'x */ DROP TABLE t; -- '", lexer.Spark)
	if a.Severity() != lexer.Drop || !slices.Equal(writes(a), []string{"t"}) {
		t.Errorf("severity = %q, writes = %v; a flat comment hid the DROP", a.Severity(), writes(a))
	}
	if a := lexer.Analyze("/* a /* b */ DELETE FROM t */ SELECT 1", lexer.Spark); a.Severity() != lexer.Select || len(a.Relations) != 0 {
		t.Errorf("severity = %q, relations = %v; a nested comment leaked a DELETE", a.Severity(), a.Relations)
	}
}

// Spark's quoting: backslash escapes in both quote styles, '"' a string,
// backticks doubled, R the only raw prefix, $tag$ quotes, and no '#' comment.
// Each row is a statement whose literal boundary decides whether a DELETE is
// live.
func TestSparkQuoting(t *testing.T) {
	for _, tc := range []struct {
		why, sql string
		want     lexer.Verb
		writes   []string
	}{
		{"backslash escapes a quote", `SELECT 'a\'; DELETE FROM t; --'`, lexer.Select, nil},
		{"'\"' is a string", `SELECT "a\"; DELETE FROM t; --"`, lexer.Select, nil},
		{"a string is no relation", `DELETE FROM "t"`, lexer.Delete, nil},
		{"r'' is raw", `SELECT r'\'; DELETE FROM t; --'`, lexer.Delete, []string{"t"}},
		{"R\"\" is raw", `SELECT R"\"; DELETE FROM t; --"`, lexer.Delete, []string{"t"}},
		{"b'' is no prefix", `SELECT b'\'; DELETE FROM t; --'`, lexer.Select, nil},
		{"rb'' is no prefix", `SELECT rb'\'; DELETE FROM t; --'`, lexer.Select, nil},
		{"no triple quotes", `SELECT '''; DELETE FROM t; --'`, lexer.Select, nil},
		{"doubled backtick", "DELETE FROM `a``b`", lexer.Delete, []string{"a`b"}},
		{"backslash in a backtick is a byte", "DELETE FROM `a\\`", lexer.Delete, []string{`a\`}},
		{"dollar quote", `SELECT $q$; DELETE FROM t; $q$`, lexer.Select, nil},
		{"'#' is no comment", "SELECT 1 # ;DELETE FROM t", lexer.Delete, []string{"t"}},
	} {
		a := lexer.Analyze(tc.sql, lexer.Spark)
		if a.Severity() != tc.want || !slices.Equal(writes(a), tc.writes) || !a.Complete {
			t.Errorf("%s: severity = %q, writes = %v, complete = %v (%s), want %q %v: %s",
				tc.why, a.Severity(), writes(a), a.Complete, a.Reason, tc.want, tc.writes, tc.sql)
		}
	}
}

// `:name` parameters and JSON paths may spell a keyword; `?` is punctuation.
// Read as words, `:from` or `raw:from` would put an introducer mid-predicate
// and invent a relation from the next word.
func TestSparkParametersInventNoRelations(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM t WHERE a = :from AND b = ?",
		"SELECT raw:from, raw:into FROM t",
		"SELECT x::int FROM t",
	} {
		a := lexer.Analyze(sql, lexer.Spark)
		if !slices.Equal(reads(a), []string{"t"}) || len(writes(a)) != 0 || a.Severity() != lexer.Select {
			t.Errorf("reads = %v, writes = %v, severity = %q: %s", reads(a), writes(a), a.Severity(), sql)
		}
	}
}

// The Delta Lake commands and Spark's own maintenance verbs. VACUUM deletes
// the files older versions need; the rest rewrite, roll back or re-register
// the table. Each names its target, written. DESCRIBE reads metadata, and
// CACHE and UNCACHE read the table into memory or drop it from there.
func TestSparkMaintenanceCommands(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		verb   lexer.Verb
		writes []string
		reads  []string
	}{
		{"VACUUM t RETAIN 0 HOURS", lexer.Delete, []string{"t"}, nil},
		{"VACUUM c.s.t DRY RUN", lexer.Delete, []string{"c.s.t"}, nil},
		{"VACUUM '/mnt/t'", lexer.Delete, nil, nil},
		// Any word names a relation here: Databricks SQL parses these.
		{"VACUUM over RETAIN 0 HOURS", lexer.Delete, []string{"over"}, nil},
		{"OPTIMIZE between ZORDER BY (a)", lexer.Alter, []string{"between"}, nil},
		{"RESTORE like TO VERSION AS OF 1", lexer.Alter, []string{"like"}, nil},
		{"OPTIMIZE t WHERE d > '2024' ZORDER BY (a)", lexer.Alter, []string{"t"}, nil},
		{"RESTORE TABLE t TO VERSION AS OF 1", lexer.Alter, []string{"t"}, nil},
		{"RESTORE t TO TIMESTAMP AS OF '2024-01-01'", lexer.Alter, []string{"t"}, nil},
		{"REORG TABLE t APPLY (PURGE)", lexer.Alter, []string{"t"}, nil},
		{"CONVERT TO DELTA parquet.`/mnt/t` PARTITIONED BY (d DATE)", lexer.Alter, []string{"parquet./mnt/t"}, nil},
		{"MSCK REPAIR TABLE t SYNC PARTITIONS", lexer.Alter, []string{"t"}, nil},
		{"REPAIR TABLE t", lexer.Alter, []string{"t"}, nil},
		{"REPLACE TABLE t AS SELECT * FROM s", lexer.Create, []string{"t"}, []string{"s"}},
		{"LOAD DATA INPATH '/p' OVERWRITE INTO TABLE t", lexer.Insert, []string{"t"}, nil},
		{"DESCRIBE TABLE t", lexer.Show, nil, []string{"t"}},
		{"DESC HISTORY t", lexer.Show, nil, []string{"t"}},
		{"DESCRIBE t", lexer.Show, nil, []string{"t"}},
		{"DESCRIBE TABLE EXTENDED t", lexer.Show, nil, []string{"t"}},
		{"DESC FORMATTED c.s.t", lexer.Show, nil, []string{"c.s.t"}},
		{"DESCRIBE DETAIL t", lexer.Show, nil, []string{"t"}},
		// A table named like a DESCRIBE keyword: no name follows it.
		{"DESC history", lexer.Show, nil, []string{"history"}},
		{"DESCRIBE extended", lexer.Show, nil, []string{"extended"}},
		{"DESC history.x", lexer.Show, nil, []string{"history.x"}},
		{"DESCRIBE TABLE EXTENDED history", lexer.Show, nil, []string{"history"}},
		{"DESCRIBE extended PARTITION (p = 1)", lexer.Show, nil, []string{"extended"}},
		// Delta's path form names no table, and the keyword is no name.
		{"DESCRIBE DETAIL '/data/events'", lexer.Show, nil, nil},
		{"DESCRIBE HISTORY '/data/events'", lexer.Show, nil, nil},
		{"DESCRIBE FUNCTION f", lexer.Show, nil, nil},
		{"DESCRIBE SCHEMA s", lexer.Show, nil, nil},
		{"DESCRIBE QUERY SELECT * FROM t", lexer.Show, nil, []string{"t"}},
		{"GENERATE symlink_format_manifest FOR TABLE t", lexer.Alter, []string{"t"}, nil},
		{"CACHE TABLE t", lexer.Other, nil, []string{"t"}},
		{"CACHE LAZY TABLE t AS SELECT * FROM s", lexer.Other, nil, []string{"t", "s"}},
		{"UNCACHE TABLE IF EXISTS t", lexer.Other, nil, []string{"t"}},
		{"CACHE SELECT * FROM t", lexer.Other, nil, []string{"t"}},
	} {
		a := lexer.Analyze(tc.sql, lexer.Spark)
		if a.Verb != tc.verb || !slices.Equal(writes(a), tc.writes) || !slices.Equal(reads(a), tc.reads) || !a.Complete {
			t.Errorf("verb = %q, writes = %v, reads = %v, complete = %v, want %q %v %v: %s",
				a.Verb, writes(a), reads(a), a.Complete, tc.verb, tc.writes, tc.reads, tc.sql)
		}
	}
}

// SHOW TBLPROPERTIES, SHOW COLUMNS and SHOW PARTITIONS read a table's
// metadata, and table_changes reads its rows. Each names its table read, so
// a rule fencing reads of the table matches; before, none named a relation.
func TestSparkMetadataAndChangeFeedReadTheirTable(t *testing.T) {
	for _, tc := range []struct {
		sql   string
		reads []string
	}{
		{"SHOW TBLPROPERTIES salaries", []string{"salaries"}},
		{"SHOW TBLPROPERTIES c.s.salaries ('delta.appendOnly')", []string{"c.s.salaries"}},
		{"SHOW COLUMNS IN salaries", []string{"salaries"}},
		{"SHOW COLUMNS FROM salaries", []string{"salaries"}},
		// The schema after the table qualifies it; it is no relation.
		{"SHOW COLUMNS IN salaries IN hr", []string{"hr.salaries"}},
		{"SHOW COLUMNS FROM salaries FROM hr", []string{"hr.salaries"}},
		{"SHOW COLUMNS IN hr.salaries IN hr", []string{"hr.salaries"}},
		{"SHOW PARTITIONS salaries", []string{"salaries"}},
		{"SHOW PARTITIONS salaries PARTITION (year = 2024)", []string{"salaries"}},
		{"SHOW TABLES IN hr", nil},
		// SHOW puts no modifier before its table, so a table may be called only.
		{"SHOW TBLPROPERTIES only", []string{"only"}},
		{"SHOW PARTITIONS if", []string{"if"}},
		{"SHOW COLUMNS IN t IN only", []string{"only.t"}},
		{"SELECT * FROM table_changes('salaries', 1)", []string{"salaries"}},
		{"SELECT * FROM table_changes('c.hr.Salaries', 1, 5)", []string{"c.hr.salaries"}},
		{"SELECT * FROM table_changes(\"`Pay Roll`.salaries\", '2024-01-01')", []string{"Pay Roll.salaries"}},
		{"SELECT * FROM table_changes(r'hr.salaries', 1)", []string{"hr.salaries"}},
		{"SELECT * FROM TABLE_CHANGES ( /* feed */ 'salaries' , 1)", []string{"salaries"}},
		{"SELECT * FROM a JOIN table_changes('salaries', 1) c ON a.id = c.id", []string{"a", "salaries"}},
		{"SELECT * FROM table_changes('salaries', (SELECT max(v) FROM versions))", []string{"salaries", "versions"}},
		// The qualified spellings of the same built-in.
		{"SELECT * FROM builtin.table_changes('salaries', 1)", []string{"salaries"}},
		{"SELECT * FROM system.builtin.table_changes('salaries', 1)", []string{"salaries"}},
		{"SELECT * FROM `system`.`builtin`.`table_changes`('salaries', 1)", []string{"salaries"}},
	} {
		a := lexer.Analyze(tc.sql, lexer.Spark)
		if !slices.Equal(reads(a), tc.reads) || len(writes(a)) != 0 || !a.Complete {
			t.Errorf("reads = %v, writes = %v, complete = %v (%s), want reads %v: %s",
				reads(a), writes(a), a.Complete, a.Reason, tc.reads, tc.sql)
		}
	}
	// A MERGE reads the change feed it applies.
	a := lexer.Analyze("MERGE INTO t USING table_changes('salaries', 1) c ON t.id = c.id WHEN MATCHED THEN DELETE", lexer.Spark)
	if !slices.Equal(writes(a), []string{"t"}) || !slices.Equal(reads(a), []string{"salaries"}) {
		t.Errorf("writes = %v, reads = %v, want [t] [salaries]", writes(a), reads(a))
	}
}

// A table_changes whose first argument is not one plain string names a table
// this scanner cannot see. Complete, it passed a rule fencing reads of that
// table with no relation to match.
func TestSparkTableChangesOverAnExpressionIsUnreadable(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM table_changes(concat('sal', 'aries'), 1)",
		"SELECT * FROM table_changes(:tbl, 1)",
		"SELECT * FROM table_changes(?, 1)",
		"SELECT * FROM table_changes('sal' 'aries', 1)",
		`SELECT * FROM table_changes('sal\aries', 1)`,
		"SELECT * FROM table_changes('hr salaries', 1)",
		"SELECT * FROM table_changes('', 1)",
	} {
		if a := lexer.Analyze(sql, lexer.Spark); a.Complete {
			t.Errorf("complete, relations = %v: %s", a.Relations, sql)
		}
	}
	// Elsewhere table_changes is any function, and names nothing.
	if a := lexer.Analyze("SELECT * FROM table_changes(concat('a', 'b'), 1)", lexer.Postgres); !a.Complete || len(a.Relations) != 0 {
		t.Errorf("postgres: complete = %v, relations = %v", a.Complete, a.Relations)
	}
}

// Statements whose target no INTO-after-INSERT introduces, all of which ran
// on a Databricks SQL warehouse: COPY INTO, INSERT OVERWRITE with and
// without TABLE, and the FROM-first multi-insert. Read as PostgreSQL, the
// first was a select and the targets of the others were reads or missing.
func TestSparkWriteTargets(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		verb   lexer.Verb
		writes []string
		reads  []string
	}{
		{"COPY INTO t FROM 's3://b/p' FILEFORMAT = CSV", lexer.Copy, []string{"t"}, nil},
		{"COPY INTO t FROM (SELECT a FROM 's3://b/p') FILEFORMAT = PARQUET", lexer.Copy, []string{"t"}, nil},
		{"INSERT OVERWRITE t SELECT * FROM s", lexer.Insert, []string{"t"}, []string{"s"}},
		{"INSERT OVERWRITE TABLE t PARTITION (p = 1) SELECT * FROM s", lexer.Insert, []string{"t"}, []string{"s"}},
		{"INSERT OVERWRITE LOCAL DIRECTORY '/tmp/x' SELECT * FROM s", lexer.Insert, nil, []string{"s"}},
		{"FROM s INSERT INTO t SELECT a INSERT OVERWRITE TABLE u SELECT b", lexer.Insert, []string{"t", "u"}, []string{"s"}},
		{"INSERT WITH SCHEMA EVOLUTION INTO t SELECT * FROM s", lexer.Insert, []string{"t"}, []string{"s"}},
		{"INSERT WITH SCHEMA EVOLUTION OVERWRITE t SELECT * FROM s", lexer.Insert, []string{"t"}, []string{"s"}},
		{"INSERT WITH SCHEMA EVOLUTION OVERWRITE TABLE t SELECT * FROM s", lexer.Insert, []string{"t"}, []string{"s"}},
		{"FROM s INSERT INTO t SELECT a INSERT WITH SCHEMA EVOLUTION OVERWRITE TABLE u SELECT b", lexer.Insert, []string{"t", "u"}, []string{"s"}},
		{"FROM s SELECT a", lexer.Select, nil, []string{"s"}},
		{"MERGE WITH SCHEMA EVOLUTION INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET *", lexer.Merge, []string{"t"}, []string{"s"}},
		{"CREATE TABLE t (a INT) USING DELTA", lexer.Create, []string{"t"}, nil},
		{"CREATE TABLE t USING DELTA AS SELECT * FROM s", lexer.Create, []string{"t"}, []string{"s"}},
		// OVERWRITE names a target only after INSERT. Elsewhere it is a
		// name, here an alias, and the FROM after it still names s.
		{"SELECT id overwrite FROM s", lexer.Select, nil, []string{"s"}},
		{"CREATE TABLE t AS SELECT x overwrite FROM s", lexer.Create, []string{"t"}, []string{"s"}},
		{"INSERT INTO t SELECT x overwrite FROM s", lexer.Insert, []string{"t"}, []string{"s"}},
		{"INSERT OVERWRITE t SELECT x overwrite FROM s", lexer.Insert, []string{"t"}, []string{"s"}},
		// EXPLAIN plans the INSERT and writes nothing; the plan shows t.
		{"EXPLAIN INSERT OVERWRITE t SELECT 1", lexer.Explain, nil, []string{"t"}},
		{"EXPLAIN FORMATTED INSERT OVERWRITE t SELECT 1", lexer.Explain, nil, []string{"t"}},
		{"EXPLAIN INSERT WITH SCHEMA EVOLUTION OVERWRITE t SELECT 1", lexer.Explain, nil, []string{"t"}},
		{"EXPLAIN SELECT insert overwrite FROM s", lexer.Explain, nil, []string{"s"}},
		{"LOAD DATA INPATH '/p' OVERWRITE INTO TABLE t", lexer.Insert, []string{"t"}, nil},
	} {
		a := lexer.Analyze(tc.sql, lexer.Spark)
		if a.Verb != tc.verb || !slices.Equal(writes(a), tc.writes) || !slices.Equal(reads(a), tc.reads) || !a.Complete {
			t.Errorf("verb = %q, writes = %v, reads = %v, complete = %v, want %q %v %v: %s",
				a.Verb, writes(a), reads(a), a.Complete, tc.verb, tc.writes, tc.reads, tc.sql)
		}
	}
}

// A Spark SQL script runs its statements under control flow: it is
// incomplete, and every write in it is an effect, wherever the grammar puts
// it — after BEGIN, DO, LOOP, REPEAT, a label, or a handler's condition list.
// BEGIN TRANSACTION and BEGIN WORK are a transaction start, and END is never
// a commit.
func TestSparkScripts(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		effect lexer.Verb
		writes []string
	}{
		{"BEGIN DROP TABLE t; END", lexer.Drop, []string{"t"}},
		{"BEGIN NOT ATOMIC DELETE FROM t; END", lexer.Delete, []string{"t"}},
		{"BEGIN ATOMIC UPDATE t SET a = 1; END", lexer.Update, []string{"t"}},
		{"BEGIN DECLARE c INT DEFAULT 0; WHILE c < 3 DO DELETE FROM t; SET c = c + 1; END WHILE; END", lexer.Delete, []string{"t"}},
		{"BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION DROP TABLE t; SELECT 1; END", lexer.Drop, []string{"t"}},
		{"BEGIN DECLARE CONTINUE HANDLER FOR SQLSTATE '22012' VACUUM t; SELECT 1; END", lexer.Delete, []string{"t"}},
		{"BEGIN lbl: LOOP UPDATE t SET a = 1; LEAVE lbl; END LOOP lbl; END", lexer.Update, []string{"t"}},
		{"BEGIN lbl:BEGIN DROP TABLE t; END lbl; END", lexer.Drop, []string{"t"}},
		{"BEGIN REPEAT INSERT INTO t VALUES (1); UNTIL true END REPEAT; END", lexer.Insert, []string{"t"}},
		{"BEGIN IF x THEN SELECT 1; ELSE TRUNCATE TABLE t; END IF; END", lexer.Truncate, []string{"t"}},
		{"BEGIN FOR r AS SELECT a FROM s DO MERGE INTO t USING s ON t.a = s.a WHEN MATCHED THEN DELETE; END FOR; END", lexer.Delete, []string{"t"}},
		{"BEGIN SELECT /*+ MERGE(a, b) */ * FROM a JOIN b ON a.id = b.id; END", lexer.Select, nil},
		// REPLACE is no verb anywhere in a script, since replace() is a
		// function, so these depend on the head position each keyword
		// restores.
		{"BEGIN NOT ATOMIC REPLACE TABLE t AS SELECT 1; END", lexer.Create, []string{"t"}},
		{"BEGIN WHILE c DO REPLACE TABLE t AS SELECT 1; END WHILE; END", lexer.Create, []string{"t"}},
		{"BEGIN LOOP REPLACE TABLE t AS SELECT 1; END LOOP; END", lexer.Create, []string{"t"}},
		{"BEGIN REPEAT REPLACE TABLE t AS SELECT 1; UNTIL true END REPEAT; END", lexer.Create, []string{"t"}},
		{"BEGIN lbl: BEGIN REPLACE TABLE t AS SELECT 1; END lbl; END", lexer.Create, []string{"t"}},
		// A label may spell TRANSACTION or WORK; Databricks runs these as
		// scripts, so they are not a transaction start.
		{"BEGIN work: BEGIN DROP TABLE t; END work; END", lexer.Drop, []string{"t"}},
		{"BEGIN transaction: BEGIN DELETE FROM t; END transaction; END", lexer.Delete, []string{"t"}},
		{"BEGIN work:BEGIN DROP TABLE t; END work; END", lexer.Drop, []string{"t"}},
		{"BEGIN lbl:LOOP DELETE FROM t; LEAVE lbl; END LOOP lbl; END", lexer.Delete, []string{"t"}},
		// A handler's statement follows its whole condition list.
		{"BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION REPLACE TABLE t AS SELECT 1; SELECT 1/0; END", lexer.Create, []string{"t"}},
		{"BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION GENERATE symlink_format_manifest FOR TABLE t; SELECT 1/0; END", lexer.Alter, []string{"t"}},
		{"BEGIN DECLARE EXIT HANDLER FOR NOT FOUND, SQLSTATE VALUE '22012' DELETE FROM t; SELECT 1; END", lexer.Delete, []string{"t"}},
		{"BEGIN DECLARE EXIT HANDLER FOR c.d DROP TABLE t; SELECT 1; END", lexer.Drop, []string{"t"}},
		{"BEGIN DECLARE EXIT HANDLER FOR drop DROP TABLE t; SELECT 1; END", lexer.Drop, []string{"t"}},
		{"BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN DELETE FROM t; END; SELECT 1; END", lexer.Delete, []string{"t"}},
	} {
		a := lexer.Analyze(tc.sql, lexer.Spark)
		if a.Complete {
			t.Errorf("Complete = true on a script: %s", tc.sql)
		}
		if !slices.Contains(a.Effects, tc.effect) || !slices.Equal(writes(a), tc.writes) {
			t.Errorf("effects = %v, writes = %v, want %q %v: %s", a.Effects, writes(a), tc.effect, tc.writes, tc.sql)
		}
		if slices.Contains(a.Effects, lexer.Begin) || slices.Contains(a.Effects, lexer.Commit) || slices.Contains(a.Effects, lexer.Merge) && tc.effect == lexer.Select {
			t.Errorf("effects = %v; a block keyword or a hint read as a statement: %s", a.Effects, tc.sql)
		}
	}
	for _, sql := range []string{"BEGIN TRANSACTION", "BEGIN WORK", "begin transaction", "BEGIN TRANSACTION;"} {
		a := lexer.Analyze(sql, lexer.Spark)
		if a.Verb != lexer.Begin || !a.Complete {
			t.Errorf("verb = %q, complete = %v, want a complete begin: %s", a.Verb, a.Complete, sql)
		}
	}
	// Databricks rejects any clause after TRANSACTION; read as a script, the
	// statement is incomplete rather than a begin.
	if a := lexer.Analyze("BEGIN TRANSACTION ISOLATION LEVEL SERIALIZABLE", lexer.Spark); a.Complete || a.Verb == lexer.Begin {
		t.Errorf("verb = %q, complete = %v, want an incomplete script", a.Verb, a.Complete)
	}
	if a := lexer.Analyze("END", lexer.Spark); slices.Contains(a.Effects, lexer.Commit) {
		t.Errorf("effects = %v; END read as a commit", a.Effects)
	}
}

// Inside a script, statements begin only where the grammar puts them, so a
// verb elsewhere is no write: SHOW CREATE TABLE reads, a column may be called
// load or generate, and MERGE is also a join hint. Read as writes, each
// refused a read.
func TestSparkScriptsInventNoWrites(t *testing.T) {
	for _, sql := range []string{
		"BEGIN SHOW CREATE TABLE t; END",
		"BEGIN SELECT load FROM metrics; END",
		"BEGIN SELECT generate FROM t; END",
		"BEGIN SELECT /*+ MERGE */ * FROM a JOIN b ON a.id = b.id; END",
		"BEGIN SELECT /*+ MERGE(a, b) */ * FROM a JOIN b ON a.id = b.id; END",
		"BEGIN SELECT a.update, b.delete FROM a JOIN b ON a.id = b.id; END",
		"BEGIN WHILE x < 3 DO SELECT replace(s, 'a', 'b') FROM t; END WHILE; END",
	} {
		a := lexer.Analyze(sql, lexer.Spark)
		if a.Writes() || len(writes(a)) != 0 {
			t.Errorf("effects = %v, writes = %v; a read in a script read as a write: %s", a.Effects, writes(a), sql)
		}
	}
}

// THEN and ELSE head a statement in every dialect, for MERGE clauses and
// IF and CASE statements. In a CASE expression, `THEN replace(...)` is a
// function call BI tools emit and `THEN load` a column; neither carries the
// keyword its statement would start with, so neither reads as a write.
func TestSparkCaseExpressionsAreNotStatements(t *testing.T) {
	for _, sql := range []string{
		"SELECT CASE WHEN a = 1 THEN replace(s, 'x', 'y') ELSE s END AS r FROM t",
		"SELECT CASE WHEN a = 1 THEN s ELSE replace(s, 'x', 'y') END AS r FROM t",
		"SELECT CASE WHEN a = 1 THEN load ELSE 0 END FROM t",
		"SELECT CASE WHEN a THEN repair ELSE convert END FROM t",
		"SELECT CASE WHEN a THEN msck WHEN b THEN reorg END FROM t",
		"WITH c AS (SELECT CASE WHEN a THEN replace(s, 'x', 'y') END AS r FROM t) SELECT * FROM c",
		"BEGIN SELECT CASE WHEN a THEN replace(s, 'x', 'y') END FROM t; END",
		"BEGIN IF x THEN SELECT CASE WHEN a THEN load END FROM t; END IF; END",
		"(SELECT CASE WHEN a = 1 THEN replace(s, 'x', 'y') END FROM t)",
		"(SELECT a FROM t) UNION ALL (SELECT CASE WHEN a = 1 THEN replace(s, 'x', 'y') END FROM t)",
		"BEGIN IF CASE WHEN a THEN replace(s, 'x', 'y') ELSE s END = 'z' THEN SELECT 1; END IF; END",
		"BEGIN WHILE CASE x WHEN 1 THEN load ELSE 0 END > 0 DO SELECT 1; END WHILE; END",
	} {
		a := lexer.Analyze(sql, lexer.Spark)
		if a.Writes() || len(writes(a)) != 0 {
			t.Errorf("effects = %v, writes = %v; a CASE expression read as a write: %s", a.Effects, writes(a), sql)
		}
	}
	for _, tc := range []struct {
		sql    string
		effect lexer.Verb
	}{
		{"MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET a = s.a WHEN NOT MATCHED THEN INSERT *", lexer.Insert},
		{"MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET * WHEN NOT MATCHED BY SOURCE THEN DELETE", lexer.Delete},
		{"BEGIN IF x THEN SELECT 1; ELSEIF y THEN SELECT 2; ELSE DROP TABLE t; END IF; END", lexer.Drop},
		{"BEGIN IF exists(SELECT 1 FROM s) THEN DELETE FROM t; END IF; END", lexer.Delete},
		{"BEGIN CASE x WHEN 1 THEN SELECT 1; WHEN 2 THEN TRUNCATE TABLE t; END CASE; END", lexer.Truncate},
		{"BEGIN CASE WHEN x THEN REPLACE TABLE t AS SELECT 1; ELSE SELECT 1; END CASE; END", lexer.Create},
		// A handler's body block keeps its IF and CASE statements: DECLARE's
		// own verb must not decide whether their THEN heads.
		{"BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN IF true THEN DROP TABLE t; END IF; END; SELECT 1/0; END", lexer.Drop},
		{"BEGIN DECLARE EXIT HANDLER FOR SQLEXCEPTION BEGIN CASE WHEN true THEN DELETE FROM t; END CASE; END; SELECT 1/0; END", lexer.Delete},
		// A CASE expression closes before the IF's THEN.
		{"BEGIN IF CASE x WHEN 1 THEN true END THEN DROP TABLE t; END IF; END", lexer.Drop},
		{"BEGIN IF CASE WHEN a THEN CASE WHEN b THEN 1 END END = 1 THEN DROP TABLE t; END IF; END", lexer.Drop},
		// CASE is a column outside ANSI mode; it opens no expression here.
		{"BEGIN IF case = 1 THEN DROP TABLE t; END IF; END", lexer.Drop},
		{"BEGIN SELECT case FROM s; IF x THEN DELETE FROM t; END IF; END", lexer.Delete},
		// A column called case before a WHEN reads as an open CASE that no
		// END closes; the next statement must start with none open.
		{"BEGIN MERGE INTO t USING s ON t.id = s.case WHEN MATCHED THEN UPDATE SET a = 1; IF x THEN DROP TABLE u; END IF; END", lexer.Drop},
		// A column or variable called case comes before a CASE statement;
		// Databricks accepts the name even in ANSI mode.
		{"BEGIN FOR r AS SELECT case FROM s DO CASE WHEN r.case = 1 THEN DELETE FROM t; END CASE; END FOR; END", lexer.Delete},
		{"BEGIN DECLARE case INT DEFAULT 0; WHILE case < 1 DO CASE WHEN true THEN DELETE FROM t; END CASE; SET case = case + 1; END WHILE; END", lexer.Delete},
		{"BEGIN DECLARE case INT DEFAULT 1; CASE case WHEN 1 THEN DROP TABLE t; END CASE; END", lexer.Drop},
		{"BEGIN DECLARE case INT DEFAULT 1; CASE case WHEN 1 THEN REPLACE TABLE t AS SELECT 1; END CASE; END", lexer.Create},
	} {
		if a := lexer.Analyze(tc.sql, lexer.Spark); !slices.Contains(a.Effects, tc.effect) {
			t.Errorf("effects = %v, want %q: %s", a.Effects, tc.effect, tc.sql)
		}
	}
}

// Released Spark reads a `/*+` inside a block comment as text, so the comment
// ends at the first `*/` and what follows runs; Spark master and Databricks
// SQL nest on it. Text holding `/*+` is read both ways.
func TestSparkHintOpenerInsideACommentIsReadBothWays(t *testing.T) {
	a := lexer.Analyze("/* /*+ */ DROP TABLE t -- */", lexer.Spark)
	if a.Severity() != lexer.Drop || !slices.Equal(writes(a), []string{"t"}) {
		t.Errorf("severity = %q, writes = %v; released Spark runs this DROP", a.Severity(), writes(a))
	}
	// Only released Spark sees this statement, and it is dynamic SQL: the
	// merged reading must stay incomplete, or a rule naming unknown misses
	// it.
	if a := lexer.Analyze("/* /*+ */ EXECUTE IMMEDIATE 'DROP TABLE t' -- */", lexer.Spark); a.Complete {
		t.Errorf("Complete = true; dynamic SQL that only released Spark reads was reported readable, effects %v", a.Effects)
	}
	for _, sql := range []string{
		"/* a /*+ b */ c */ SELECT 1",
		"SELECT /*+ REPARTITION(3) */ * FROM t",
		"/* a /* b */ c */ SELECT 1",
	} {
		if a := lexer.Analyze(sql, lexer.Spark); a.Severity() != lexer.Select || len(writes(a)) != 0 {
			t.Errorf("severity = %q, writes = %v, want a select: %s", a.Severity(), writes(a), sql)
		}
	}
}

// Delta Lake's parser reads its commands with flat comments, so text that is
// all comment to Spark can be a VACUUM to Delta. The second reading counts
// only behind a Delta command: a DELETE after a continued comment is
// commentary to Spark, and Delta hands it to Spark.
func TestSparkDeltaReading(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		want   lexer.Verb
		writes []string
	}{
		{"/* /* */ VACUUM t RETAIN 0 HOURS -- */", lexer.Delete, []string{"t"}},
		{"/* /* */ OPTIMIZE t -- */", lexer.Alter, []string{"t"}},
		{"/* /* */ RESTORE TABLE t TO VERSION AS OF 1 -- */", lexer.Alter, []string{"t"}},
		{"/* /* */ ALTER TABLE t DROP FEATURE deletionVectors -- */", lexer.Alter, []string{"t"}},
		{"-- x \\\nVACUUM t", lexer.Delete, []string{"t"}},
		{"/*+ x */ VACUUM t", lexer.Delete, []string{"t"}},
		// Only Delta reads a VACUUM here, and only once a lone CR ends its
		// dash comment.
		{"/* /* */ -- x\rVACUUM t -- */", lexer.Delete, []string{"t"}},
		{"-- x \\\nDELETE FROM t", lexer.Unknown, nil},
		{"/* /* */ DELETE FROM t -- */", lexer.Unknown, nil},
	} {
		a := lexer.Analyze(tc.sql, lexer.Spark)
		if a.Severity() != tc.want || !slices.Equal(writes(a), tc.writes) {
			t.Errorf("severity = %q, writes = %v, want %q %v: %q", a.Severity(), writes(a), tc.want, tc.writes, tc.sql)
		}
	}
}

// Spark runs one statement or one script per call, and a script fragment
// loses the BEGIN that makes its statements heads. Split keeps the text
// whole, as it does for Oracle.
func TestSparkSplitKeepsTheWholeCall(t *testing.T) {
	for _, sql := range []string{
		"SELECT 'a;b' FROM t",
		"BEGIN WHILE c DO DELETE FROM t; END WHILE; END",
	} {
		if parts := lexer.Split("  "+sql+"\n", lexer.Spark); !slices.Equal(parts, []string{sql}) {
			t.Errorf("Split = %q, want the whole call", parts)
		}
	}
	if got := lexer.Split("   ", lexer.Spark); len(got) != 0 {
		t.Errorf("Split of blank text = %q", got)
	}
}

// The Spark rows are off everywhere else. Each statement below reads one way
// under Spark and the other way under every other dialect.
func TestSparkRulesDoNotLeakIntoOtherDialects(t *testing.T) {
	others := []lexer.Dialect{lexer.Postgres, lexer.MSSQL, lexer.MySQL, lexer.ClickHouse, lexer.GoogleSQL, lexer.Oracle}
	for _, sql := range []string{
		"SELECT 1 -- x \\\n; DELETE FROM t",
		"DELETE\u00a0FROM t",
		"VACUUM t",
		"BEGIN SELECT /*+ X('*/') */ 1; DELETE FROM t; END; --'",
		"SELECT '${x}' AS y",
		"FROM s INSERT INTO t SELECT a",
	} {
		spark := lexer.Analyze(sql, lexer.Spark)
		for _, d := range others {
			a := lexer.Analyze(sql, d)
			if a.Severity() == spark.Severity() && a.Complete == spark.Complete && slices.Equal(writes(a), writes(spark)) {
				t.Errorf("%s reads %q as Spark does (%q, complete = %v)", d, sql, a.Severity(), a.Complete)
			}
		}
	}
}
