//go:build integration

package e2e_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/sijms/go-ora/v2/network"
)

// Exercise a real thin Oracle driver through the shipped relay: a filtered
// SELECT, enough rows to require multiple fetches, NULLs, a native denial,
// and continued use of the same database after that denial.
func TestOracleThinClientE2E(t *testing.T) {
	upstream := startOracle(t)
	relay := startSidecar(t, upstream.addr, oracleMaskingConfig)
	client := openOracle(t, relay.addr, 5)
	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()

	var name, email, code, nick, note string
	if err := client.QueryRowContext(ctx, `SELECT name, email, code, nick, note FROM people WHERE id = :1`, 1).
		Scan(&name, &email, &code, &nick, &note); err != nil {
		t.Fatalf("filtered Oracle SELECT: %v", err)
	}
	if name != "Ada Lovelace" || code != "AL-001" || nick != "Ada" || note != "first row" {
		t.Errorf("filtered row changed: name=%q code=%q nick=%q note=%q", name, code, nick, note)
	}
	if email == "ada@example.com" || !strings.Contains(email, "[REDACTED:") {
		t.Errorf("email was not redacted: %q", email)
	}
	relay.waitForAudit(t, "Oracle filtered SELECT", func(ev auditEvent) bool {
		return ev.Kind == "statement" && ev.Operation == "select" &&
			strings.Contains(strings.ToUpper(ev.Statement), "WHERE ID")
	})

	rows, err := client.QueryContext(ctx, `SELECT id, email FROM people ORDER BY id`)
	if err != nil {
		t.Fatalf("Oracle multi-batch SELECT: %v", err)
	}
	count := 0
	for rows.Next() {
		var id int
		var value sql.NullString
		if err := rows.Scan(&id, &value); err != nil {
			rows.Close()
			t.Fatalf("scan Oracle row %d: %v", count+1, err)
		}
		count++
		if id != count {
			rows.Close()
			t.Fatalf("fetch order: row %d has id %d", count, id)
		}
		if id == 3 {
			if value.Valid {
				rows.Close()
				t.Fatalf("NULL email became %q", value.String)
			}
		} else if !value.Valid || !strings.Contains(value.String, "[REDACTED:") || strings.Contains(value.String, "@example.com") {
			rows.Close()
			t.Fatalf("row %d email not redacted: %+v", id, value)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("fetch Oracle rows: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close Oracle rows: %v", err)
	}
	if count != 40 {
		t.Fatalf("fetched %d rows, want 40 across multiple batches", count)
	}
	relay.waitForAudit(t, "masked Oracle result", func(ev auditEvent) bool {
		return ev.Kind == "masked" && ev.Count > 0
	})

	// Both directions of a long CLR: SQL text and a selected value over 252
	// bytes. Sessions negotiate BIG_CHUNK_CLR, so their chunk lengths are
	// compressed UB4 rather than single bytes.
	long := strings.Repeat("x", 300)
	var filler, mailed string
	if err := client.QueryRowContext(ctx, `SELECT '`+long+`' AS filler, email FROM people WHERE id = :1`, 2).
		Scan(&filler, &mailed); err != nil {
		t.Fatalf("Oracle long SQL and value: %v", err)
	}
	if filler != long || !strings.Contains(mailed, "[REDACTED:") {
		t.Errorf("long value changed or email unmasked: %d bytes, email %q", len(filler), mailed)
	}

	var nullable sql.NullString
	if err := client.QueryRowContext(ctx, `SELECT note FROM people WHERE id = :1`, 3).Scan(&nullable); err != nil {
		t.Fatalf("Oracle nullable note: %v", err)
	}
	if nullable.Valid {
		t.Errorf("NULL note became %q", nullable.String)
	}

	_, err = client.ExecContext(ctx, `DELETE FROM people WHERE id = :1`, 1)
	var oracleErr *network.OracleError
	if !errors.As(err, &oracleErr) || oracleErr.ErrCode != 1031 {
		t.Fatalf("denied DELETE error = %v, want native ORA-01031", err)
	}
	violation := relay.waitForAudit(t, "denied Oracle DELETE", func(ev auditEvent) bool {
		return ev.Kind == "violation" && ev.Operation == "delete"
	})
	if violation.Allowed || violation.Rule != "no-destructive-oracle" {
		t.Errorf("DELETE violation = %+v", violation)
	}

	// Check upstream independently: a policy event alone cannot prove that
	// the relay kept the DELETE away from Oracle.
	direct := openOracle(t, upstream.addr, 10)
	var remaining int
	if err := direct.QueryRowContext(ctx, `SELECT COUNT(*) FROM people WHERE id = :1`, 1).Scan(&remaining); err != nil {
		t.Fatalf("direct Oracle verification: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("denied DELETE reached Oracle: row 1 count = %d", remaining)
	}

	// A DELETE inside a PL/SQL block is the same delete. FORALL puts it
	// after an expression, where only a reading of the whole block finds
	// it. The relay closed the denied session, so use a fresh pool.
	client = openOracle(t, relay.addr, 5)
	_, err = client.ExecContext(ctx, `DECLARE TYPE ids IS TABLE OF NUMBER; v ids := ids(1); BEGIN FORALL i IN 1 .. v.count DELETE FROM people WHERE id = v(i); END;`)
	if !errors.As(err, &oracleErr) || oracleErr.ErrCode != 1031 {
		t.Fatalf("denied PL/SQL DELETE error = %v, want native ORA-01031", err)
	}
	violation = relay.waitForAudit(t, "denied PL/SQL DELETE", func(ev auditEvent) bool {
		return ev.Kind == "violation" && strings.Contains(ev.Statement, "FORALL")
	})
	if violation.Allowed || violation.Rule != "no-destructive-oracle" {
		t.Errorf("PL/SQL DELETE violation = %+v", violation)
	}
	if err := direct.QueryRowContext(ctx, `SELECT COUNT(*) FROM people WHERE id = :1`, 1).Scan(&remaining); err != nil {
		t.Fatalf("direct Oracle verification: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("denied PL/SQL DELETE reached Oracle: row 1 count = %d", remaining)
	}

	// The relay ends a denied session. database/sql can still hand out that
	// pooled connection before go-ora sees the close, and a transaction
	// cannot retry onto another one, so continue on a fresh pool: the claim
	// under test is that the lane keeps serving new sessions.
	client = openOracle(t, relay.addr, 5)
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin Oracle transaction after denial: %v", err)
	}
	var got string
	if err := tx.QueryRowContext(ctx, `SELECT code FROM people WHERE id = :1`, 1).Scan(&got); err != nil {
		_ = tx.Rollback()
		t.Fatalf("SELECT after denial: %v", err)
	}
	if got != "AL-001" {
		_ = tx.Rollback()
		t.Errorf("code after denial = %q, want AL-001", got)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO people (id, name, email, code, nick, note)
		VALUES (:1, :2, :3, :4, :5, :6)`,
		41, "New Person", "new@example.com", "NEW-041", "New", "inserted row"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("INSERT after denial: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Oracle COMMIT after denial: %v", err)
	}
	if err := direct.QueryRowContext(ctx, `SELECT COUNT(*) FROM people WHERE id = :1`, 41).Scan(&remaining); err != nil {
		t.Fatalf("verify committed INSERT upstream: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("INSERT did not reach Oracle after COMMIT: row 41 count = %d", remaining)
	}
	if err := client.QueryRowContext(ctx, `SELECT name, email FROM people WHERE id = :1`, 41).
		Scan(&name, &email); err != nil {
		t.Fatalf("read inserted Oracle row: %v", err)
	}
	if name != "New Person" || !strings.Contains(email, "[REDACTED:") || strings.Contains(email, "new@example.com") {
		t.Errorf("inserted row was changed or unmasked: name=%q email=%q", name, email)
	}
	relay.waitForAudit(t, "allowed Oracle INSERT", func(ev auditEvent) bool {
		return ev.Kind == "statement" && ev.Operation == "insert" && ev.Allowed
	})
	if err := client.Close(); err != nil {
		t.Fatalf("close thin Oracle driver: %v", err)
	}
}
