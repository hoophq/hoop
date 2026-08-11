package hoopinspect_test

import (
	"testing"

	"github.com/hoophq/hoopinspect"
	_ "github.com/hoophq/hoopinspect/codec/all"
)

// Every case here defeated the previous classifier through the REAL wire
// path, not through ClassifySQL in isolation. The codec splits multi-statement
// query strings itself, so a bypass that survives here survived in production.
//
// This file is a permanent regression suite. A statement that lands in it
// stays in it: each row is a way somebody got a write past a policy that named
// the write.

// inspectOne runs sql through the postgres codec and returns the statements.
func inspectOne(t *testing.T, sql string) []hoopinspect.Statement {
	t.Helper()
	insp, err := hoopinspect.New(hoopinspect.Postgres)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stmts, err := insp.Inspect(hoopinspect.FromClient, pgQuery(sql))
	if err != nil {
		t.Fatalf("Inspect(%q): %v", sql, err)
	}
	return stmts
}

// writesTo reports whether any statement in the batch writes to name.
func writesTo(stmts []hoopinspect.Statement, name string) bool {
	for _, s := range stmts {
		for _, r := range s.Relations {
			if r.Name == name && r.Access == hoopinspect.AccessWrite {
				return true
			}
		}
	}
	return false
}

func hasEffect(stmts []hoopinspect.Statement, op hoopinspect.Operation) bool {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			stmts := inspectOne(t, tc.sql)
			if !hasEffect(stmts, hoopinspect.OpDelete) {
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
					if r.Name == tc.wantRead && r.Access == hoopinspect.AccessRead {
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
			if s.Operation != hoopinspect.OpUnknown {
				t.Errorf("Operation = %q, want unknown: %s", s.Operation, sql)
			}
			if s.Metadata[hoopinspect.MetadataSQLIncomplete] == "" {
				t.Errorf("no %s metadata: %s", hoopinspect.MetadataSQLIncomplete, sql)
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
