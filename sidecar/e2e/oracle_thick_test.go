//go:build integration

package e2e_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// SQL*Plus uses OCI rather than the thin driver. Keep the whole workload on
// one seeded database, but use a new SQL*Plus process for each session: policy
// denial closes the relayed connection, not the listener.
func TestOracleThickSQLPlus(t *testing.T) {
	db := startOracle(t)
	s := startSidecar(t, db.addr, oracleMaskingConfig)

	t.Run("where projection and mask", func(t *testing.T) {
		out := oracleThickQuery(t, db, s.addr, `
SELECT id, name, email, code, nick, note FROM people WHERE id = 1;
EXIT;
`)
		rows := oracleThickRows(t, out, 1, 6)
		if got := rows[0]; got[0] != "1" || got[1] != "Ada Lovelace" || got[3] != "AL-001" || got[4] != "Ada" || got[5] != "first row" {
			t.Errorf("projected row = %q", got)
		}
		oracleAssertMaskedEmail(t, rows[0][2], out)
		s.waitForAudit(t, "OCI SELECT with a WHERE clause", func(ev auditEvent) bool {
			return ev.Kind == "statement" && ev.Operation == "select" &&
				strings.Contains(strings.ToLower(ev.Statement), "where id = 1") && ev.Allowed
		})
		s.waitForAudit(t, "masked OCI projection", func(ev auditEvent) bool {
			return ev.Kind == "masked" && ev.Count > 0
		})
	})

	t.Run("array fetches", func(t *testing.T) {
		out := oracleThickQuery(t, db, s.addr, `
SET ARRAYSIZE 2
SELECT id, email FROM people WHERE id BETWEEN 1 AND 9 ORDER BY id;
EXIT;
`)
		rows := oracleThickRows(t, out, 9, 2)
		for i, row := range rows {
			id := i + 1
			if row[0] != fmt.Sprint(id) {
				t.Errorf("row %d id = %q", i, row[0])
			}
			if id == 3 {
				if row[1] != "<NULL>" {
					t.Errorf("NULL email became %q", row[1])
				}
				continue
			}
			oracleAssertMaskedEmail(t, row[1], out)
		}
		if strings.Contains(out, "ada@example.com") || strings.Contains(out, "user09@example.com") {
			t.Errorf("unmasked address in OCI array fetch: %q", out)
		}
		s.waitForAudit(t, "OCI array fetch masking", func(ev auditEvent) bool {
			return ev.Kind == "masked" && ev.Count > 0
		})
	})

	t.Run("update commit and read back", func(t *testing.T) {
		out := oracleThickQuery(t, db, s.addr, `
SET FEEDBACK ON
UPDATE people SET name = 'Ada Updated' WHERE id = 1;
COMMIT;
SET FEEDBACK OFF
SELECT id, name, email FROM people WHERE id = 1;
EXIT;
`)
		if !strings.Contains(out, "1 row updated.") || !strings.Contains(out, "Commit complete.") {
			t.Errorf("UPDATE/COMMIT acknowledgment missing: %q", out)
		}
		rows := oracleThickRows(t, out, 1, 3)
		if rows[0][0] != "1" || rows[0][1] != "Ada Updated" {
			t.Errorf("read after COMMIT = %q", rows[0])
		}
		oracleAssertMaskedEmail(t, rows[0][2], out)

		// A different connection directly to Oracle proves COMMIT reached the
		// database; a SELECT in the same SQL*Plus session could see its own
		// uncommitted UPDATE even if the COMMIT never crossed the relay.
		direct := oracleThickQuery(t, db, db.addr, `
SELECT id, name FROM people WHERE id = 1;
EXIT;
`)
		committed := oracleThickRows(t, direct, 1, 2)
		if committed[0][0] != "1" || committed[0][1] != "Ada Updated" {
			t.Errorf("upstream row after COMMIT = %q", committed[0])
		}
		s.waitForAudit(t, "OCI UPDATE", func(ev auditEvent) bool {
			return ev.Kind == "statement" && ev.Operation == "update" && ev.Allowed
		})
	})

	t.Run("delete is denied", func(t *testing.T) {
		out, err := db.sqlplus(t, s.addr, oracleThickSettings+`
WHENEVER SQLERROR EXIT SQL.SQLCODE
DELETE FROM people WHERE id = 2;
EXIT;
`)
		// The relay closes a denied session after writing ORA-01031; SQL*Plus
		// can also report EOF while it processes the remaining EXIT command.
		if !strings.Contains(out, "ORA-01031:") {
			t.Fatalf("DELETE returned %q (process error %v), want ORA-01031", out, err)
		}
		if err == nil {
			t.Error("SQL*Plus exited successfully after denied DELETE")
		}

		direct := oracleThickQuery(t, db, db.addr, `
SELECT id, name FROM people WHERE id = 2;
EXIT;
`)
		rows := oracleThickRows(t, direct, 1, 2)
		if rows[0][0] != "2" || rows[0][1] != "Person 02" {
			t.Errorf("denied DELETE changed upstream row: %q", rows[0])
		}
		ev := s.waitForAudit(t, "denied OCI DELETE", func(ev auditEvent) bool {
			return ev.Kind == "violation" && ev.Operation == "delete"
		})
		if ev.Allowed || ev.Rule != "no-destructive-oracle" {
			t.Errorf("DELETE audit = %+v", ev)
		}

		// Denial ends only the offending connection; an independent OCI
		// session through the same listener must still work.
		recovered := oracleThickQuery(t, db, s.addr, `
SELECT id, name FROM people WHERE id = 2;
EXIT;
`)
		rows = oracleThickRows(t, recovered, 1, 2)
		if rows[0][0] != "2" || rows[0][1] != "Person 02" {
			t.Errorf("new session after denial = %q", rows[0])
		}
	})

	// SQL*Plus sends a PL/SQL block as one call, and its DELETE is the
	// same delete.
	t.Run("plsql delete is denied", func(t *testing.T) {
		out, err := db.sqlplus(t, s.addr, oracleThickSettings+`
WHENEVER SQLERROR EXIT SQL.SQLCODE
BEGIN DELETE FROM people WHERE id = 2; END;
/
EXIT;
`)
		if !strings.Contains(out, "ORA-01031:") {
			t.Fatalf("PL/SQL DELETE returned %q (process error %v), want ORA-01031", out, err)
		}
		direct := oracleThickQuery(t, db, db.addr, `
SELECT id, name FROM people WHERE id = 2;
EXIT;
`)
		if rows := oracleThickRows(t, direct, 1, 2); rows[0][0] != "2" {
			t.Errorf("denied PL/SQL DELETE changed upstream row: %q", rows[0])
		}
		ev := s.waitForAudit(t, "denied OCI PL/SQL DELETE", func(ev auditEvent) bool {
			return ev.Kind == "violation" && strings.HasPrefix(ev.Statement, "BEGIN DELETE")
		})
		if ev.Allowed || ev.Rule != "no-destructive-oracle" {
			t.Errorf("PL/SQL DELETE audit = %+v", ev)
		}
	})

	t.Run("normal exit", func(t *testing.T) {
		out := oracleThickQuery(t, db, s.addr, `
SELECT id, name FROM people WHERE id = 4;
EXIT;
`)
		rows := oracleThickRows(t, out, 1, 2)
		if rows[0][0] != "4" || rows[0][1] != "Person 04" {
			t.Errorf("normal session = %q", rows[0])
		}
	})
}

const oracleThickSettings = `SET ECHO OFF
SET VERIFY OFF
SET HEADING OFF
SET FEEDBACK OFF
SET PAGESIZE 0
SET LINESIZE 1000
SET TRIMOUT ON
SET WRAP OFF
SET RECSEP OFF
SET NULL '<NULL>'
SET COLSEP '|'
COLUMN id FORMAT 999
COLUMN name FORMAT A20
COLUMN email FORMAT A48
COLUMN code FORMAT A12
COLUMN nick FORMAT A12
COLUMN note FORMAT A20
`

func oracleThickQuery(t *testing.T, db *oracleDB, endpoint, statements string) string {
	t.Helper()
	out, err := db.sqlplus(t, endpoint, oracleThickSettings+"\nWHENEVER SQLERROR EXIT SQL.SQLCODE\n"+statements)
	if err != nil || strings.Contains(out, "ORA-") {
		t.Fatalf("SQL*Plus query failed: %v; output: %q", err, out)
	}
	return out
}

var oracleThickRowLine = regexp.MustCompile(`(?m)^[ \t]*[0-9]+[ \t]*\|[^\r\n]*$`)

func oracleThickRows(t *testing.T, output string, wantRows, wantColumns int) [][]string {
	t.Helper()
	lines := oracleThickRowLine.FindAllString(output, -1)
	if len(lines) != wantRows {
		t.Fatalf("SQL*Plus returned %d data rows, want %d; output: %q", len(lines), wantRows, output)
	}
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) != wantColumns {
			t.Fatalf("SQL*Plus row has %d columns, want %d: %q", len(parts), wantColumns, line)
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		rows = append(rows, parts)
	}
	return rows
}

func oracleAssertMaskedEmail(t *testing.T, email, output string) {
	t.Helper()
	if !strings.HasPrefix(email, "[REDACTED:") || !strings.HasSuffix(email, "]") {
		t.Errorf("OCI email was not redacted: %q; output: %q", email, output)
	}
}
