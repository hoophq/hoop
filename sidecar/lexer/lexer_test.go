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
		"with", "with as", "with x as", "select from", "delete from",
		strings.Repeat("(", 200), strings.Repeat(")", 200),
		strings.Repeat("with x as (", 50),
	} {
		for _, d := range []lexer.Dialect{lexer.Postgres, lexer.MSSQL, lexer.MySQL} {
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
