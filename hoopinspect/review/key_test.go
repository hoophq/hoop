package review

import (
	"strings"
	"testing"

	"github.com/hoophq/hoopinspect"
)

// The round trip the whole design rests on: the bytes the CREATE path hashed
// and the bytes the CLAIM path hashes on the retry are the same bytes, so the
// approval a human gave is findable. Drift between the two callers is the
// failure that makes approvals silently unmatchable, which is why there is one
// function and this test.
func TestCreateAndClaimAgreeOnTheKey(t *testing.T) {
	// What the agent sends first: marked, so a review can be deduped.
	first := "-- hoopdev:correlation_id=task-42\nDELETE FROM users WHERE id = 7;"
	// What it re-sends after approval. Same statement, and it may or may not
	// still carry the marker: the marker is stripped either way.
	retries := []string{
		"-- hoopdev:correlation_id=task-42\nDELETE FROM users WHERE id = 7;",
		"-- hoopdev:correlation_id=task-99\nDELETE FROM users WHERE id = 7;",
		"DELETE FROM users WHERE id = 7;",
		"  DELETE FROM users WHERE id = 7;  ",
	}

	created, marker := canonicalize(first)
	if marker != "task-42" {
		t.Fatalf("marker = %q, want task-42", marker)
	}
	want := execKey(created)

	for _, retry := range retries {
		got, _ := canonicalize(retry)
		if execKey(got) != want {
			t.Errorf("retry %q hashed to a different key\n  created: %q\n  retried: %q",
				retry, created, got)
		}
	}
}

// Over-normalizing is a silent bypass and under-normalizing is a visible
// failure, so every rule that is not "remove what hoop injected" is left out.
// Each case here is a pair a normalizer would wrongly merge.
func TestDifferentStatementsNeverShareAKey(t *testing.T) {
	pairs := [][2]string{
		// The literal. This is the whole threat: approve one row, and a
		// shape-keyed lookup would authorize every row.
		{"DELETE FROM users WHERE id = 1", "DELETE FROM users WHERE id = 999"},
		// Case, in a string literal: different rows.
		{"SELECT * FROM t WHERE name = 'Alice'", "SELECT * FROM t WHERE name = 'alice'"},
		// Case, in an identifier: different relations in PostgreSQL.
		{`SELECT * FROM "Customers"`, `SELECT * FROM customers`},
		// Interior whitespace inside a literal is DATA.
		{"UPDATE t SET note = 'a  b'", "UPDATE t SET note = 'a b'"},
		// Comments are not inert: optimizer hints and MySQL's executable
		// comments both change behavior.
		{"SELECT /*+ IndexScan(t) */ * FROM t", "SELECT * FROM t"},
		{"SELECT /*! SQL_NO_CACHE */ * FROM t", "SELECT * FROM t"},
		// A second marker-looking comment is content, not metadata: only a
		// leading one is stripped.
		{"SELECT 1\n-- hoopdev:correlation_id=x", "SELECT 1"},
	}

	for _, p := range pairs {
		a, _ := canonicalize(p[0])
		b, _ := canonicalize(p[1])
		if execKey(a) == execKey(b) {
			t.Errorf("collided:\n  %q\n  %q", p[0], p[1])
		}
	}
}

// A marker that is not in the one accepted form is CONTENT. Stripping it would
// make "strip the marker" ambiguous, and the same logical statement would then
// leave different residue depending on which almost-right spelling it used.
func TestNonConformingMarkersAreLeftInPlace(t *testing.T) {
	cases := []string{
		"--hoopdev:correlation_id=x\nSELECT 1",                                            // no space
		"-- Hoop:corr=x\nSELECT 1",                                                        // wrong case
		"-- hoopdev:correlation_id =x\nSELECT 1",                                          // space before =
		" -- hoopdev:correlation_id=x\nSELECT 1",                                          // not anchored at byte 0
		"/* hoopdev:correlation_id=x */\nSELECT 1",                                        // block comment
		"-- hoopdev:correlation_id=\nSELECT 1",                                            // empty value
		"-- hoopdev:correlation_id=a b\nSELECT 1",                                         // space in the value
		"-- hoopdev:correlation_id=a\tb\nSELECT 1",                                        // tab in the value
		"-- hoopdev:correlation_id=" + strings.Repeat("a", MaxMarkerLen+1) + "\nSELECT 1", // too long
	}

	for _, in := range cases {
		canonical, marker := canonicalize(in)
		if marker != "" {
			t.Errorf("%q yielded marker %q; nothing non-conforming should be recognized", in, marker)
		}
		if canonical != strings.TrimSpace(in) {
			t.Errorf("%q was rewritten to %q; a non-conforming marker must survive into the hash",
				in, canonical)
		}
	}
}

func TestConformingMarkerShapes(t *testing.T) {
	cases := map[string]struct{ marker, canonical string }{
		"-- hoopdev:correlation_id=task-42\nSELECT 1":     {"task-42", "SELECT 1"},
		"-- hoopdev:correlation_id=a/b:c@d+e._-1\nDROP t": {"a/b:c@d+e._-1", "DROP t"},
		"-- hoopdev:correlation_id=x   \nSELECT 1":        {"x", "SELECT 1"}, // trailing blanks trimmed
		"-- hoopdev:correlation_id=x\r\nSELECT 1":         {"x", "SELECT 1"}, // CRLF
		"-- hoopdev:correlation_id=x":                     {"x", ""},         // marker and nothing else
		"-- hoopdev:correlation_id=x\n\n\n  SELECT 1  \n": {"x", "SELECT 1"}, // surrounding blanks trimmed
	}

	for in, want := range cases {
		canonical, marker := canonicalize(in)
		if marker != want.marker || canonical != want.canonical {
			t.Errorf("canonicalize(%q) = (%q, %q), want (%q, %q)",
				in, canonical, marker, want.canonical, want.marker)
		}
	}
}

// Observability is an ALLOWLIST of message kinds, so anything a codec grows
// later falls through to a refusal instead of being gated on a shape.
//
// The parameterized paths are the ones this exists for: neither codec reads
// the values that will be bound, so the gate sees `WHERE id = $1` and one
// approval would cover every subsequent binding, indefinitely.
func TestOnlyLiteralStatementsAreObservable(t *testing.T) {
	stmt := func(proto hoopinspect.Protocol, text string, md map[string]string) hoopinspect.Statement {
		return hoopinspect.Statement{
			Protocol: proto, Direction: hoopinspect.FromClient, Text: text, Metadata: md,
		}
	}

	refused := map[string]hoopinspect.Statement{
		"postgres extended protocol": stmt(hoopinspect.Postgres,
			"DELETE FROM users WHERE id = $1", map[string]string{"pg.message": "Parse"}),
		"mssql sp_executesql": stmt(hoopinspect.MSSQL,
			"DELETE FROM users WHERE id = @p1",
			map[string]string{"mssql.message": "RPCRequest", "mssql.proc": "sp_executesql"}),
		"a message kind the gate does not know": stmt(hoopinspect.Postgres,
			"DELETE FROM users", map[string]string{"pg.message": "SomethingNew"}),
		"no message kind at all": stmt(hoopinspect.Postgres, "DELETE FROM users", nil),
	}
	for name, s := range refused {
		id := identify(s, "")
		if id.Observable {
			t.Errorf("%s was treated as observable", name)
		}
		if id.Hash != "" {
			t.Errorf("%s produced a key; an unobservable statement must have none", name)
		}
		if id.Why == "" {
			t.Errorf("%s was refused without saying why", name)
		}
	}

	allowed := map[string]hoopinspect.Statement{
		"postgres simple query": stmt(hoopinspect.Postgres,
			"DELETE FROM users WHERE id = 7", map[string]string{"pg.message": "Query"}),
		"mssql batch": stmt(hoopinspect.MSSQL,
			"DELETE FROM users WHERE id = 7", map[string]string{"mssql.message": "SQLBatch"}),
	}
	for name, s := range allowed {
		if id := identify(s, ""); !id.Observable || id.Hash == "" {
			t.Errorf("%s was refused: %+v", name, id)
		}
	}
}

// An HTTP request is keyed on the body too, or an approval for one POST
// authorizes every later POST to the same path — the same bypass a shape hash
// would be on SQL.
func TestHTTPKeyCoversTheBody(t *testing.T) {
	req := func(body string) hoopinspect.Statement {
		return hoopinspect.Statement{
			Protocol:  hoopinspect.HTTP,
			Direction: hoopinspect.FromClient,
			Text:      "POST /transfers",
			HTTP:      &hoopinspect.HTTPDetail{Method: "POST", Path: "/transfers", Body: body},
		}
	}
	a := identify(req(`{"amount":1}`), "")
	b := identify(req(`{"amount":1000000}`), "")

	if !a.Observable || !b.Observable {
		t.Fatal("an HTTP request with a captured body was refused")
	}
	if a.Hash == b.Hash {
		t.Error("two different bodies share a key; an approval for one would authorize the other")
	}
}

// A body the codec cut short is a statement the gate did not fully see.
func TestTruncatedHTTPBodyIsNotObservable(t *testing.T) {
	id := identify(hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Direction: hoopinspect.FromClient,
		Text:      "POST /transfers",
		HTTP: &hoopinspect.HTTPDetail{
			Method: "POST", Body: `{"amount":1`, BodyTruncated: true,
		},
	}, "")

	if id.Observable {
		t.Fatal("a truncated body was treated as observable")
	}
}

// A protocol with nowhere to put a comment still needs a request identity, and
// the session's correlation id is the handle that already exists for it.
func TestSessionCorrelationIDIsTheFallbackMarker(t *testing.T) {
	stmt := hoopinspect.Statement{
		Protocol:  hoopinspect.Postgres,
		Direction: hoopinspect.FromClient,
		Text:      "DELETE FROM t",
		Metadata:  map[string]string{"pg.message": "Query"},
	}
	if got := identify(stmt, "ci-run-9").Marker; got != "ci-run-9" {
		t.Errorf("marker = %q, want the session correlation id", got)
	}

	// The statement's own marker is per-statement and wins: an agent whose
	// task 3 and task 9 run byte-identical SQL has to be able to say so.
	stmt.Text = "-- hoopdev:correlation_id=task-9\nDELETE FROM t"
	if got := identify(stmt, "ci-run-9").Marker; got != "task-9" {
		t.Errorf("marker = %q, want the statement's own", got)
	}
}
