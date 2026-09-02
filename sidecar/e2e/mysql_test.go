//go:build integration

package e2e_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// maskingConfig is a lane that masks the email column and refuses
// destructive statements. Column masking rather than entity detection: the
// column rule is exact, so a failure here is a relay bug and never a
// detector's judgement call about whether a string looks like an address.
const maskingConfig = `
log_level: info

audit:
  file: "-"
  memory_buffer: 64

listeners:
  - name: appdb
    protocol: mysql
    listen: {{listen}}
    upstream: {{upstream}}
    guardrails:
      mode: enforce
      rules:
        - name: no-destructive-sql
          type: operation
          operations: [drop, delete, truncate]
          message: destructive statements are not permitted on appdb
    mask:
      rules:
        - {name: email-column, columns: [email], strategy: redact}
`

// Masking must apply to a text-protocol result set.
//
// This is the regression test for the bug that shipped past a green unit
// suite: the gate built one codec per direction, the server half never saw
// the client-side state that names columns, and the relay returned every
// address in the clear while logging "masking: true". Nothing in the client's
// view or the config said anything was wrong.
func TestTextProtocolMasksResultSet(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	rows, err := db.Query("SELECT id, name, email FROM customers ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	emails := scanEmails(t, rows)
	assertMasked(t, emails)

	s.waitForAudit(t, "a masked response", func(ev auditEvent) bool {
		return ev.Kind == "masked" && ev.Count > 0
	})
}

// The same, over the binary protocol.
//
// A prepared statement returns rows in an entirely different encoding: a NULL
// bitmap and type-driven values rather than length-encoded strings. The text
// path passing says nothing about this one, and every ORM uses it.
func TestBinaryProtocolMasksResultSet(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	// A placeholder forces the driver onto COM_STMT_PREPARE/EXECUTE.
	rows, err := db.Query("SELECT id, name, email FROM customers WHERE id > ? ORDER BY id", 0)
	if err != nil {
		t.Fatalf("prepared query: %v", err)
	}
	defer rows.Close()

	assertMasked(t, scanEmails(t, rows))
}

// A NULL must survive masking as a NULL.
//
// Re-encoding it as an empty string turns "no value" into "the empty string",
// which changes what the client computes downstream. It is the kind of
// corruption that shows up as a wrong report weeks later, so it is pinned
// here rather than left to the codec's unit tests alone.
func TestMaskingPreservesNull(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	var email sql.NullString
	if err := db.QueryRow("SELECT email FROM customers WHERE id = 3").Scan(&email); err != nil {
		t.Fatalf("query: %v", err)
	}
	if email.Valid {
		t.Fatalf("NULL came back as %q; a re-encoded NULL is a silent data change",
			email.String)
	}
}

// A denied statement must come back as a native ERR_Packet, and the session
// must stay usable afterwards.
//
// Dropping the socket instead would give the developer "Lost connection to
// MySQL server during query" — an outage message for a policy decision, which
// is what sends them to support instead of to their own query.
func TestGuardrailDeniesWithNativeError(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	_, err := db.Exec("DELETE FROM customers WHERE id = 1")
	if err == nil {
		t.Fatal("the DELETE was allowed")
	}
	assertPolicyError(t, err)

	// The row is still there: the statement was refused, not merely reported.
	if got := countRows(t, db); got != 3 {
		t.Fatalf("row count = %d, want 3 -- the DELETE reached the server", got)
	}

	ev := s.waitForAudit(t, "the denied DELETE", func(ev auditEvent) bool {
		return ev.Kind == "violation" && ev.Operation == "delete"
	})
	if ev.Allowed {
		t.Error("the violation was recorded as allowed")
	}
	if ev.Rule != "no-destructive-sql" {
		t.Errorf("rule = %q, want no-destructive-sql", ev.Rule)
	}
}

// A DROP hidden behind a harmless leading statement must still be caught.
//
// CLIENT_MULTI_STATEMENTS is on by default in Connector/J and most ORMs set
// it, so this arrives as ONE COM_QUERY. A relay classifying the packet by its
// leading verb sees a SELECT and forwards the DROP.
func TestMultiStatementDropIsDenied(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "multiStatements=true")

	_, err := db.Exec("SELECT 1; DROP TABLE customers")
	if err == nil {
		t.Fatal("a DROP behind a leading SELECT was allowed")
	}
	assertPolicyError(t, err)

	if got := countRows(t, db); got != 3 {
		t.Fatalf("row count = %d; the table did not survive", got)
	}
}

// A prepared DELETE must be denied at PREPARE, where the SQL text is.
//
// COM_STMT_EXECUTE carries a numeric id and no statement, so a relay that
// only inspected execution would see an integer and allow it. Denying at
// prepare is what makes the parameterised path as safe as the literal one.
func TestPreparedDeleteIsDenied(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	_, err := db.Exec("DELETE FROM customers WHERE id = ?", 2)
	if err == nil {
		t.Fatal("a prepared DELETE was allowed")
	}
	assertPolicyError(t, err)

	if got := countRows(t, db); got != 3 {
		t.Fatalf("row count = %d; the prepared DELETE ran", got)
	}

	s.waitForAudit(t, "the denied prepare", func(ev auditEvent) bool {
		return ev.Kind == "violation" &&
			ev.Metadata["mysql.command"] == "COM_STMT_PREPARE"
	})
}

// Allowed traffic must pass through untouched, and the session must survive a
// mixed workload.
//
// The negative half of every test above: a relay that denied everything would
// pass them all. This is also the shape that caught the auth-phase hang —
// several statements over a pooled connection, where the relay has to stay in
// step across command boundaries.
func TestAllowedTrafficIsUnaffected(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	if _, err := db.Exec("UPDATE customers SET name = 'Ada L.' WHERE id = 1"); err != nil {
		t.Fatalf("allowed UPDATE refused: %v", err)
	}
	if _, err := db.Exec("INSERT INTO customers VALUES (4, 'Eve', 'eve@example.com', NULL)"); err != nil {
		t.Fatalf("allowed INSERT refused: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM customers WHERE id = 1").Scan(&name); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if name != "Ada L." {
		t.Errorf("name = %q, want the updated value", name)
	}
	if got := countRows(t, db); got != 4 {
		t.Errorf("row count = %d, want 4", got)
	}
}

// execAllowed runs a statement that policy permits, tolerating exactly one
// stale-connection failure.
//
// This is the relay's real contract, not a workaround. A denial ends with an
// ERR frame and a CLOSED socket (proxy.pump returns after writing the frame),
// which is deliberate and the same for every protocol: the relay will not go
// on relaying a session it just refused a statement on. database/sql does not
// know that, so it keeps the dead connection in the pool and hands it to the
// next caller, which fails once before the driver dials again.
//
// So a test asserting "the very next Exec succeeds" would be asserting
// something the sidecar has never promised, and would fail intermittently
// depending on whether the pool happened to reuse the closed connection. What
// IS promised is that the failure is transient: one retry reaches a working
// session. A second failure means the ERR frame desynchronized the client, or
// the relay stopped accepting, and that is a real bug.
func execAllowed(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	if err == nil {
		return
	}
	if !isStaleConn(err) {
		t.Fatalf("allowed statement refused: %v", err)
	}
	if _, err = db.Exec(query, args...); err != nil {
		t.Fatalf("allowed statement still failing after a fresh connection: %v", err)
	}
}

// isStaleConn reports whether err is the driver noticing the socket the relay
// closed under it, rather than a refusal or a protocol fault.
//
// Matched narrowly on purpose: "commands out of sync" is the symptom of a
// malformed ERR frame and MUST NOT be swallowed here, because that is exactly
// the bug this suite exists to catch.
func isStaleConn(err error) bool {
	return errors.Is(err, mysqldriver.ErrInvalidConn) ||
		errors.Is(err, driver.ErrBadConn) ||
		strings.Contains(err.Error(), "invalid connection") ||
		strings.Contains(err.Error(), "busy buffer")
}

// A pooled connection must recover after a denial, repeatedly.
//
// The relay answers a denial with an ERR frame and then CLOSES the session —
// deliberate, and the same for every protocol. The pool keeps that dead
// socket, so the next statement on it fails once and the driver redials.
// execAllowed encodes exactly that one-retry contract.
//
// What must never happen is the failure persisting. If the ERR frame were
// malformed or its sequence id wrong, the driver would report "commands out
// of sync" and keep handing the poisoned connection to the next caller — a
// policy denial turning into an application-wide outage. Looping proves the
// relay returns to a working state every time rather than degrading.
func TestSessionRecoversAfterDenial(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	for i := range 3 {
		if _, err := db.Exec("DELETE FROM customers WHERE id = 1"); err == nil {
			t.Fatalf("round %d: the DELETE was allowed", i)
		}
		// A write, not a read: QueryRow is retried by database/sql itself, so
		// a read would hide a session the relay never recovered.
		execAllowed(t, db, "UPDATE customers SET name = 'Ada L.' WHERE id = 1")

		if got := countRows(t, db); got != 3 {
			t.Fatalf("round %d: row count = %d, want 3", i, got)
		}
	}
}

// The relay must not hang, on any of its paths.
//
// This is the regression test for the intermittent hang: under
// caching_sha2_password the fast-auth reply and the auth OK arrive in one
// read, and the rewriter mistook an auth byte for a column count and buffered
// the connection forever. It reproduced in about two runs of three, so the
// loop runs the full mixed workload repeatedly on fresh connections — a
// single pass would have passed on the broken build often enough to look
// green.
//
// There is no watchdog goroutine here. The connections carry a per-statement
// read deadline (see sidecar.dial), so a stalled relay surfaces as a timeout
// on the exact query that hung, in seconds, in whichever test hit it —
// including the ones below that were never written with hangs in mind.
func TestNoHangAcrossRepeatedSessions(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)

	for i := range 8 {
		db := s.dial(t, "multiStatements=true")

		rows, err := db.Query("SELECT id, name, email FROM customers ORDER BY id")
		if err != nil {
			t.Fatalf("round %d: text query: %v", i, err)
		}
		assertMasked(t, scanEmails(t, rows))
		rows.Close()

		prep, err := db.Query("SELECT email FROM customers WHERE id > ? ORDER BY id", 0)
		if err != nil {
			t.Fatalf("round %d: prepared query: %v", i, err)
		}
		prep.Close()

		if _, err := db.Exec("SELECT 1; DROP TABLE customers"); err == nil {
			t.Fatalf("round %d: the DROP was allowed", i)
		}
		// Follows a denial, so the pool may still hold the socket the relay
		// closed. One retry is the contract; a hang is not.
		execAllowed(t, db, "UPDATE customers SET name = 'Ada L.' WHERE id = 1")
		db.Close()
	}
}

// A lane with no mask rules must forward responses unchanged.
//
// The rewriter still runs on this path — it has to track the handshake so it
// is ready if masking is ever configured — so "no rules" is a distinct code
// path from "rules that match nothing", and it is the one most deployments
// use.
func TestLaneWithoutMaskingForwardsCleanly(t *testing.T) {
	const noMask = `
log_level: info

audit:
  file: "-"
  memory_buffer: 64

listeners:
  - name: appdb
    protocol: mysql
    listen: {{listen}}
    upstream: {{upstream}}
    guardrails:
      mode: enforce
      rules:
        - name: no-destructive-sql
          type: operation
          operations: [drop]
          message: destructive statements are not permitted on appdb
`
	up := startMySQL(t)
	s := startSidecar(t, up, noMask)
	db := s.dial(t, "")

	rows, err := db.Query("SELECT email FROM customers WHERE email IS NOT NULL ORDER BY id")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	emails := scanEmails(t, rows)
	if len(emails) != 2 {
		t.Fatalf("got %d rows, want 2", len(emails))
	}
	for _, e := range emails {
		if !strings.Contains(e, "@example.com") {
			t.Errorf("email = %q, want the original value on an unmasked lane", e)
		}
	}
}

// The audit trail must name the statement that ran.
//
// It is the product surface an operator reads to answer "did this happen",
// and it is written by the relay rather than observed by the client, so a
// client-side assertion cannot cover it.
func TestAuditTrailRecordsStatements(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "")

	if _, err := db.Exec("UPDATE customers SET name = 'Grace' WHERE id = 2"); err != nil {
		t.Fatalf("update: %v", err)
	}

	ev := s.waitForAudit(t, "the UPDATE", func(ev auditEvent) bool {
		return ev.Kind == "statement" &&
			ev.Direction == "client" &&
			strings.HasPrefix(ev.Statement, "UPDATE customers")
	})
	if ev.Operation != "update" {
		t.Errorf("operation = %q, want update", ev.Operation)
	}
	if !ev.Allowed {
		t.Error("an allowed statement was recorded as denied")
	}
	if got := ev.Metadata["mysql.command"]; got != "COM_QUERY" {
		t.Errorf("mysql.command = %q, want COM_QUERY", got)
	}
}

// The official mysql CLI must be policed exactly like the Go driver.
//
// This suite drives go-sql-driver, and that driver does not negotiate
// CLIENT_QUERY_ATTRIBUTES. The CLI does, by default, since 8.0.23 — and the
// capability prefixes every COM_QUERY with an attribute block. A decoder that
// misses it hands the classifier "\x00\x01DROP TABLE customers", finds no
// verb, reports OpUnknown, and a lane refusing `drop` FORWARDS the statement.
//
// That is exactly what happened: every Go-driver test above passed while the
// CLI dropped the table. Running the real client is the only way this suite
// covers the capability set a human at a terminal actually uses.
func TestOfficialCLIIsPoliced(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)

	out, err := runCLI(t, s.addr, "SELECT 1; DROP TABLE customers")
	if err == nil {
		t.Fatalf("the CLI's DROP was allowed:\n%s", out)
	}
	if !strings.Contains(out, "destructive statements are not permitted") {
		t.Fatalf("the CLI did not receive the operator's message:\n%s", out)
	}

	// The table is the ground truth: a denial that did not stop the statement
	// is not a denial.
	db := s.dial(t, "")
	if got := countRows(t, db); got != 3 {
		t.Fatalf("row count = %d, want 3 -- the CLI's DROP reached the server", got)
	}

	// Masking must work on the CLI's framing too, not only the driver's.
	masked, err := runCLI(t, s.addr, "SELECT email FROM customers WHERE id = 1")
	if err != nil {
		t.Fatalf("CLI select failed: %v\n%s", err, masked)
	}
	if strings.Contains(masked, "ada@example.com") {
		t.Fatalf("the CLI saw an unmasked address:\n%s", masked)
	}
}

// runCLI runs one statement through the official client, in a container.
//
// The client is not installed on developer machines or CI runners, and the
// point is its wire behaviour rather than the binary itself, so it comes from
// the same image as the server. --ssl-mode=DISABLED because the relay refuses
// a client-initiated TLS upgrade by design; a real deployment terminates TLS
// in front of it.
func runCLI(t *testing.T, relayAddr, sql string) (string, error) {
	t.Helper()
	return runCLIArgs(t, relayAddr, nil, sql)
}

// runCLIArgs is runCLI with extra client flags, for the capability
// negotiations that only the official client can request.
func runCLIArgs(t *testing.T, relayAddr string, extra []string, sql string) (string, error) {
	t.Helper()

	_, port, err := net.SplitHostPort(relayAddr)
	if err != nil {
		t.Fatalf("relay address %q: %v", relayAddr, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()

	args := []string{"run", "--rm",
		"--add-host=host.docker.internal:host-gateway",
		mysqlImage,
		"mysql",
		"-h", "host.docker.internal",
		"-P", port,
		"-u", dbUser,
		"-p" + dbPass,
		"--ssl-mode=DISABLED",
	}
	args = append(args, extra...)
	args = append(args, dbName, "-e", sql)

	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}

func scanEmails(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	var out []string
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	for rows.Next() {
		cells := make([]any, len(cols))
		vals := make([]sql.NullString, len(cols))
		for i := range cells {
			cells[i] = &vals[i]
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for i, c := range cols {
			if c == "email" && vals[i].Valid {
				out = append(out, vals[i].String)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// assertMasked fails if any address survived. It checks for the ORIGINAL
// value rather than for the replacement's spelling, so the assertion does not
// break when the redaction format changes — and cannot pass because masking
// produced some other wrong string.
func assertMasked(t *testing.T, emails []string) {
	t.Helper()
	if len(emails) == 0 {
		t.Fatal("no email values came back; the query returned nothing to mask")
	}
	for _, e := range emails {
		if strings.Contains(e, "@example.com") {
			t.Fatalf("email %q reached the client unmasked", e)
		}
	}
}

// assertPolicyError fails unless err carries the operator's message.
//
// The point of a native error frame is that the developer reads the reason.
// An error that merely exists — a reset connection, a driver-level protocol
// complaint — means the denial worked and the explanation did not.
func assertPolicyError(t *testing.T, err error) {
	t.Helper()
	const want = "destructive statements are not permitted on appdb"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not carry the operator's message; the developer "+
			"sees a failure with no reason", err)
	}
}

// countRows reads the fixture row count, tolerating one stale connection.
//
// Every caller runs it straight after a denial, and a denial ends with the
// relay closing the socket (see execAllowed). The pool may hand back that
// dead connection: database/sql retries internally, but only while it has
// another pooled connection to try, so on a warm pool this surfaces as
// `invalid connection` instead. Observed as a 1-in-6 flake before this.
//
// A SECOND failure is a real one and still fails the test.
func countRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM customers").Scan(&n)
	if err != nil && isStaleConn(err) {
		err = db.QueryRow("SELECT COUNT(*) FROM customers").Scan(&n)
	}
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// MySQL executes the body of `/*! ... */`, and the guardrail must see it.
//
// Found by hand against this playground, not by any test: the relay forwarded
// `/*! DROP TABLE orders */` and the table was gone, while the lane reported
// itself as enforcing a rule refusing `drop`. Worse than no guardrail, which
// at least does not claim to protect anything.
func TestExecutableCommentIsDenied(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)

	for _, stmt := range []string{
		"/*! DROP TABLE customers */",
		"/*!50000 DROP TABLE customers */",
	} {
		out, err := runCLI(t, s.addr, stmt)
		if err == nil {
			t.Fatalf("%s was allowed:\n%s", stmt, out)
		}
		if !strings.Contains(out, "destructive statements are not permitted") {
			t.Fatalf("%s: no policy message:\n%s", stmt, out)
		}
	}

	db := s.dial(t, "")
	if got := countRows(t, db); got != 3 {
		t.Fatalf("row count = %d, want 3 -- an executable comment reached the server", got)
	}
}

// A DELETE hidden by NO_BACKSLASH_ESCAPES must still be classified.
//
// The mode is per-session and the classifier never sees it, so the statement
// is read under BOTH backslash conventions and their effects unioned.
// Verified server-side before the fix: under this mode the literal ends at
// the first quote and the DELETE ran, dropping the row count.
//
// The payload goes through the Go driver with multiStatements, not the CLI.
// That is not a convenience: the mysql CLI splits input into statements
// client-side and sends them one COM_QUERY at a time, so it never produces
// the single ambiguous packet this defends against. Only a driver that
// forwards the string whole does.
func TestBackslashModeHiddenDeleteIsDenied(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)
	db := s.dial(t, "multiStatements=true")

	// Under the default reading the literal swallows the DELETE and this is
	// one harmless SELECT. Under NO_BACKSLASH_ESCAPES the literal ends at
	// the first quote and the DELETE is real.
	_, err := db.Exec(`SELECT 'a\'; DELETE FROM customers; -- '`)
	if err == nil {
		t.Fatal("the hidden DELETE was allowed")
	}
	assertPolicyError(t, err)

	if got := countRows(t, db); got != 3 {
		t.Fatalf("row count = %d, want 3 -- the hidden DELETE ran", got)
	}
}

// A compressed session is refused rather than followed blindly.
//
// zstd is a separate capability from zlib, and checking only zlib let a
// zstd session through: the codec read 7-byte compressed frames as 4-byte
// classic packets and the client HUNG for the full test timeout instead of
// receiving a refusal.
func TestCompressedSessionsAreRefused(t *testing.T) {
	up := startMySQL(t)
	s := startSidecar(t, up, maskingConfig)

	for _, algo := range []string{"zstd", "zlib"} {
		t.Run(algo, func(t *testing.T) {
			start := time.Now()
			out, err := runCLIArgs(t, s.addr,
				[]string{"--compression-algorithms=" + algo}, "SELECT 1")
			if err == nil {
				t.Fatalf("a %s session was allowed; every later statement would "+
					"be unreadable:\n%s", algo, out)
			}

			// An error alone is not the property under test. A relay that
			// MISSES the capability also errors — it reads compressed frames
			// as classic packets, desynchronizes, and the client stalls until
			// something times out. That is what zstd did before the fix: 300
			// seconds, not a refusal. The refusal is fast because the codec
			// recognizes the capability at the first command and stops.
			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Fatalf("a %s session took %s to fail: the relay is following "+
					"the stream and stalling, not refusing it", algo, elapsed)
			}
		})
	}
}
