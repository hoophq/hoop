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
	first := "-- x-hoop-correlation-id=task-42\nDELETE FROM users WHERE id = 7;"
	// What it re-sends after approval. Same statement, and it may or may not
	// still carry the marker: the marker is stripped either way.
	retries := []string{
		"-- x-hoop-correlation-id=task-42\nDELETE FROM users WHERE id = 7;",
		"-- x-hoop-correlation-id=task-99\nDELETE FROM users WHERE id = 7;",
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
		{"SELECT 1\n-- x-hoop-correlation-id=x", "SELECT 1"},
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
		"--x-hoop-correlation-id=x\nSELECT 1",                                            // no space
		"-- X-Hoop-Correlation-Id=x\nSELECT 1",                                           // wrong case: the SQL form is lowercase
		"-- hoopdev:correlation_id=x\nSELECT 1",                                          // the superseded spelling
		"-- x-hoop-correlation-id =x\nSELECT 1",                                          // space before =
		" -- x-hoop-correlation-id=x\nSELECT 1",                                          // not anchored at byte 0
		"/* x-hoop-correlation-id=x */\nSELECT 1",                                        // block comment
		"-- x-hoop-correlation-id=\nSELECT 1",                                            // empty value
		"-- x-hoop-correlation-id=a b\nSELECT 1",                                         // space in the value
		"-- x-hoop-correlation-id=a\tb\nSELECT 1",                                        // tab in the value
		"-- x-hoop-correlation-id=" + strings.Repeat("a", MaxMarkerLen+1) + "\nSELECT 1", // too long
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
		"-- x-hoop-correlation-id=task-42\nSELECT 1":     {"task-42", "SELECT 1"},
		"-- x-hoop-correlation-id=a/b:c@d+e._-1\nDROP t": {"a/b:c@d+e._-1", "DROP t"},
		"-- x-hoop-correlation-id=x   \nSELECT 1":        {"x", "SELECT 1"}, // trailing blanks trimmed
		"-- x-hoop-correlation-id=x\r\nSELECT 1":         {"x", "SELECT 1"}, // CRLF
		"-- x-hoop-correlation-id=x":                     {"x", ""},         // marker and nothing else
		"-- x-hoop-correlation-id=x\n\n\n  SELECT 1  \n": {"x", "SELECT 1"}, // surrounding blanks trimmed
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
	stmt.Text = "-- x-hoop-correlation-id=task-9\nDELETE FROM t"
	if got := identify(stmt, "ci-run-9").Marker; got != "task-9" {
		t.Errorf("marker = %q, want the statement's own", got)
	}
}

// httpStmt builds a decoded HTTP request statement carrying a correlation id.
func httpStmt(text, body, corr string) hoopinspect.Statement {
	return hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Direction: hoopinspect.FromClient,
		Text:      text,
		HTTP:      &hoopinspect.HTTPDetail{Body: body, CorrelationID: corr},
	}
}

// The header names the unit of work. It must NOT reach the authorization key:
// if it did, a retry under a new correlation id would hash differently and an
// existing approval would stop covering the identical request.
func TestHTTPCorrelationHeaderIsAMarkerAndNotPartOfTheHash(t *testing.T) {
	const text, body = "POST /anything/users/12345/orders", `{"action":"purge"}`

	a := identify(httpStmt(text, body, "task-3"), "")
	b := identify(httpStmt(text, body, "task-9"), "")
	bare := identify(httpStmt(text, body, ""), "")

	if a.Marker != "task-3" || b.Marker != "task-9" {
		t.Errorf("markers = %q, %q; want the header values", a.Marker, b.Marker)
	}
	if a.Hash != b.Hash || a.Hash != bare.Hash {
		t.Errorf("hash changed with the correlation header:\n  task-3 %s\n  task-9 %s\n  none   %s",
			a.Hash, b.Hash, bare.Hash)
	}
	if a.Canonical != bare.Canonical {
		t.Errorf("canonical text changed with the header:\n  %q\n  %q", a.Canonical, bare.Canonical)
	}
}

// Per-request beats per-connection: one keep-alive connection carries many
// requests, and each has to be able to name its own unit of work.
func TestHTTPHeaderBeatsTheSessionMarker(t *testing.T) {
	if got := identify(httpStmt("GET /x", "", "task-9"), "ci-run-9").Marker; got != "task-9" {
		t.Errorf("marker = %q, want the request's own header", got)
	}
	if got := identify(httpStmt("GET /x", "", ""), "ci-run-9").Marker; got != "ci-run-9" {
		t.Errorf("marker = %q, want the session fallback", got)
	}
}

// A header value that could not be used as a lookup key yields NO marker,
// rather than a truncated or sanitized one: a marker that quietly became a
// different string would join a retry to the wrong review request.
func TestUnusableHTTPCorrelationHeaderIsDropped(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"newline", "task\n9"},
		{"space", "task 9"},
		{"quote", `task"9`},
		{"nul", "task\x009"},
		{"too long", strings.Repeat("a", MaxMarkerLen+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := identify(httpStmt("GET /x", "", tc.value), "")
			if id.Marker != "" {
				t.Errorf("marker = %q, want none", id.Marker)
			}
			// It still falls back, and the statement stays observable:
			// an unusable header is a dedupe problem, never a refusal to gate.
			if got := identify(httpStmt("GET /x", "", tc.value), "ci-run-9").Marker; got != "ci-run-9" {
				t.Errorf("fallback marker = %q, want the session id", got)
			}
			if !id.Observable {
				t.Error("statement became unobservable; a bad header must not stop the gate")
			}
		})
	}

	// The boundary itself is accepted.
	max := strings.Repeat("a", MaxMarkerLen)
	if got := identify(httpStmt("GET /x", "", max), "").Marker; got != max {
		t.Errorf("a %d-byte marker was rejected", MaxMarkerLen)
	}
}

// Every protocol the gate can gate must be able to say how to supply a
// marker. A refusal telling an HTTP caller to "prefix it with a -- comment"
// describes something they cannot do, which reads as a broken gate rather
// than a configured one.
//
// This walks the protocols canonicalFor accepts. A new codec that becomes
// gateable without an entry in markerHowTo fails here.
func TestEveryGateableProtocolSaysHowToSupplyAMarker(t *testing.T) {
	gateable := []struct {
		protocol hoopinspect.Protocol
		stmt     hoopinspect.Statement
	}{
		{hoopinspect.HTTP, httpStmt("GET /x", "", "")},
		{hoopinspect.Postgres, hoopinspect.Statement{
			Protocol: hoopinspect.Postgres, Text: "DELETE FROM t",
			Metadata: map[string]string{"pg.message": "Query"}}},
		{hoopinspect.MSSQL, hoopinspect.Statement{
			Protocol: hoopinspect.MSSQL, Text: "DELETE FROM t",
			Metadata: map[string]string{"mssql.message": "SQLBatch"}}},
	}

	const generic = "this protocol has no supported way to carry one"
	for _, tc := range gateable {
		t.Run(string(tc.protocol), func(t *testing.T) {
			if !identify(tc.stmt, "").Observable {
				t.Fatalf("test is stale: canonicalFor no longer accepts %s", tc.protocol)
			}
			advice := markerHowTo(tc.protocol)
			if advice == generic {
				t.Errorf("%s can be gated but markerHowTo has no entry for it", tc.protocol)
			}
			// The advice must not send a caller after the wrong mechanism.
			switch tc.protocol {
			case hoopinspect.HTTP:
				if strings.Contains(advice, markerPrefix) {
					t.Errorf("HTTP advice tells the caller to use a SQL comment: %q", advice)
				}
				if !strings.Contains(advice, hoopinspect.CorrelationHeader) {
					t.Errorf("HTTP advice does not name the header: %q", advice)
				}
			default:
				if !strings.Contains(advice, markerPrefix) {
					t.Errorf("%s advice does not name the comment form: %q", tc.protocol, advice)
				}
			}
		})
	}

	// A protocol the gate refuses gets the generic line rather than advice
	// for a mechanism it does not have.
	if got := markerHowTo(hoopinspect.Protocol("mongodb")); got != generic {
		t.Errorf("unknown protocol advice = %q, want the generic line", got)
	}
}

// One name across protocols is the point of the rename: a header on HTTP, the
// same name as a comment on SQL. If either side moves without the other, a
// caller reading the HTTP docs writes a SQL marker that silently does not
// conform — it stays in the statement, lands in the hash, and the review never
// matches.
func TestSQLMarkerAndHTTPHeaderAreTheSameName(t *testing.T) {
	want := "-- " + strings.ToLower(hoopinspect.CorrelationHeader) + "="
	if markerPrefix != want {
		t.Errorf("markerPrefix = %q, want %q (lowercased %s)",
			markerPrefix, want, hoopinspect.CorrelationHeader)
	}
}
