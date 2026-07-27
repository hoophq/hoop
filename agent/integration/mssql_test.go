//go:build integration

package integration

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"

	"github.com/hoophq/hoop/agent/integration/testutil"
)

const mssqlTestTimeout = 30 * time.Second

// dialMSSQL wires up the common per-test scaffolding: start the agent,
// open a MSSQL session, start the demux, and build the bridged client.
// Returns the client plus a teardown that shuts the agent down. The
// ordering (OpenMSSQLSession before StartRecvDemux before DialPipedMSSQL)
// matters — see the helper docs.
func dialMSSQL(t *testing.T, mc *testutil.MSSQLContainer) (*testutil.PipedMSSQLClient, func()) {
	t.Helper()
	agent, tr := startAgent(t)
	sessionID := testutil.OpenMSSQLSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	connID := "conn-1"
	client := testutil.DialPipedMSSQL(t, tr, demux, mc, sessionID, connID)
	return client, func() { shutdownAgent(t, agent, tr) }
}

// TestMSSQL_Ping is the end-to-end smoke test: a successful ping forces the
// full bridged TDS handshake (PRELOGIN → PRELOGIN reply → LOGIN7 →
// libhoop-driven upstream auth → login ack) through processMSSQLProtocol
// and libhoop's MSSQL proxy.
func TestMSSQL_Ping(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	if err := client.PingWithTimeout(mssqlTestTimeout); err != nil {
		t.Fatalf("mssql ping through agent failed: %v", err)
	}
}

// TestMSSQL_SimpleQuery runs a trivial SELECT and verifies the scalar
// result round-trips through the proxy.
func TestMSSQL_SimpleQuery(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	var n int
	if err := client.DB.QueryRow("SELECT 1 AS num").Scan(&n); err != nil {
		t.Fatalf("SELECT 1 failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

// TestMSSQL_CreateInsertSelectUpdateDelete exercises the full DML/DDL
// lifecycle over a single bridged connection.
func TestMSSQL_CreateInsertSelectUpdateDelete(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_crud_%d", time.Now().UnixNano())

	exec := func(t *testing.T, query string, args ...any) sql.Result {
		t.Helper()
		res, err := db.Exec(query, args...)
		if err != nil {
			t.Fatalf("exec %q failed: %v", query, err)
		}
		return res
	}

	t.Run("CreateTable", func(t *testing.T) {
		exec(t, fmt.Sprintf("CREATE TABLE %s (id INT IDENTITY(1,1) PRIMARY KEY, name NVARCHAR(64) NOT NULL)", table))
	})

	t.Run("Insert", func(t *testing.T) {
		res := exec(t, fmt.Sprintf("INSERT INTO %s (name) VALUES (@p1)", table), "alice")
		affected, err := res.RowsAffected()
		if err != nil {
			t.Fatalf("RowsAffected: %v", err)
		}
		if affected != 1 {
			t.Errorf("expected 1 row affected, got %d", affected)
		}
	})

	t.Run("Select", func(t *testing.T) {
		var name string
		if err := db.QueryRow(fmt.Sprintf("SELECT name FROM %s WHERE name = @p1", table), "alice").Scan(&name); err != nil {
			t.Fatalf("select failed: %v", err)
		}
		if name != "alice" {
			t.Errorf("expected 'alice', got %q", name)
		}
	})

	t.Run("Update", func(t *testing.T) {
		res := exec(t, fmt.Sprintf("UPDATE %s SET name = @p1 WHERE name = @p2", table), "bob", "alice")
		affected, _ := res.RowsAffected()
		if affected != 1 {
			t.Errorf("expected 1 row updated, got %d", affected)
		}
	})

	t.Run("SelectAfterUpdate", func(t *testing.T) {
		var name string
		if err := db.QueryRow(fmt.Sprintf("SELECT name FROM %s", table)).Scan(&name); err != nil {
			t.Fatalf("select failed: %v", err)
		}
		if name != "bob" {
			t.Errorf("expected 'bob', got %q", name)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		res := exec(t, fmt.Sprintf("DELETE FROM %s", table))
		affected, _ := res.RowsAffected()
		if affected != 1 {
			t.Errorf("expected 1 row deleted, got %d", affected)
		}
	})

	t.Run("SelectAfterDelete", func(t *testing.T) {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&count); err != nil {
			t.Fatalf("count failed: %v", err)
		}
		if count != 0 {
			t.Errorf("expected count 0, got %d", count)
		}
	})

	t.Run("DropTable", func(t *testing.T) {
		exec(t, fmt.Sprintf("DROP TABLE %s", table))
	})
}

// TestMSSQL_MultiRowResultSet verifies a result set spanning multiple rows
// decodes correctly end-to-end (column metadata + row token streams + DONE
// token all flow through the proxy).
func TestMSSQL_MultiRowResultSet(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_multirow_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY, val NVARCHAR(32))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP TABLE %s", table))

	for i := 1; i <= 5; i++ {
		if _, err := db.Exec(fmt.Sprintf("INSERT INTO %s (id, val) VALUES (@p1, @p2)", table), i, fmt.Sprintf("v%d", i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := db.Query(fmt.Sprintf("SELECT id, val FROM %s ORDER BY id", table))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	got := 0
	for rows.Next() {
		var id int
		var val string
		if err := rows.Scan(&id, &val); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got++
		if id != got || val != fmt.Sprintf("v%d", got) {
			t.Errorf("row %d: got (id=%d, val=%q)", got, id, val)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if got != 5 {
		t.Errorf("expected 5 rows, got %d", got)
	}
}

// TestMSSQL_LargePayloadFragmentation pushes a value larger than the
// default TDS packet size (4096 bytes) in both directions, forcing the
// request and the result row to span multiple TDS packets. This exercises
// the bridge's packet framing (readTDSPacket / DecodeFull) at the
// boundaries that motivated it — a regression in framing or in LOGIN7
// packet-size negotiation would corrupt a multi-packet payload while the
// small-payload tests stayed green.
func TestMSSQL_LargePayloadFragmentation(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_large_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY, payload NVARCHAR(MAX))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP TABLE %s", table))

	// 64 KiB of data: well beyond the 4096-byte default TDS packet, so both
	// the INSERT request and the SELECT reply must fragment.
	large := strings.Repeat("hoop-fragmentation-test-0123456789", 2000)
	if len(large) <= 4096 {
		t.Fatalf("test payload too small to force fragmentation: %d bytes", len(large))
	}

	if _, err := db.Exec(fmt.Sprintf("INSERT INTO %s (id, payload) VALUES (@p1, @p2)", table), 1, large); err != nil {
		t.Fatalf("insert large payload: %v", err)
	}

	var got string
	if err := db.QueryRow(fmt.Sprintf("SELECT payload FROM %s WHERE id = @p1", table), 1).Scan(&got); err != nil {
		t.Fatalf("select large payload: %v", err)
	}
	if got != large {
		t.Errorf("large payload round-trip mismatch: got %d bytes, want %d bytes", len(got), len(large))
	}
}

// TestMSSQL_TransactionCommit verifies a committed transaction persists
// across the proxy.
func TestMSSQL_TransactionCommit(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_tx_commit_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT IDENTITY(1,1) PRIMARY KEY, val NVARCHAR(32))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP TABLE %s", table))

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (val) VALUES (@p1)", table), "committed"); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE val = @p1", table), "committed").Scan(&count); err != nil {
		t.Fatalf("select: %v", err)
	}
	if count != 1 {
		t.Errorf("expected committed row to persist, count=%d", count)
	}
}

// TestMSSQL_TransactionRollback verifies a rolled-back transaction leaves
// no trace.
func TestMSSQL_TransactionRollback(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_tx_rollback_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT IDENTITY(1,1) PRIMARY KEY, val NVARCHAR(32))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP TABLE %s", table))

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (val) VALUES (@p1)", table), "rolled_back"); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE val = @p1", table), "rolled_back").Scan(&count); err != nil {
		t.Fatalf("select: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after rollback, got %d", count)
	}
}

// TestMSSQL_ErrorBadQuery verifies a server-side SQL error surfaces to the
// client as a mssql.Error (proving the error token round-trips) and that
// the connection survives for a follow-up query.
func TestMSSQL_ErrorBadQuery(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQL(t, mc)
	defer teardown()

	db := client.DB
	_, err := db.Exec("SELECT * FROM nonexistent_table_xyz")
	if err == nil {
		t.Fatal("expected error for nonexistent table, got nil")
	}
	var sqlErr mssql.Error
	if !asMSSQLError(err, &sqlErr) {
		t.Fatalf("expected mssql.Error, got %T: %v", err, err)
	}
	// 208 = "Invalid object name".
	if sqlErr.Number != 208 {
		t.Errorf("expected error 208 (invalid object name), got %d (%s)", sqlErr.Number, sqlErr.Message)
	}
	if !strings.Contains(strings.ToLower(sqlErr.Message), "nonexistent_table_xyz") {
		t.Errorf("error message should reference the table, got: %s", sqlErr.Message)
	}

	// Connection must survive the error.
	var n int
	if err := db.QueryRow("SELECT 1").Scan(&n); err != nil {
		t.Fatalf("connection did not survive query error: %v", err)
	}
}

// TestMSSQL_BadCredentials verifies that a session whose upstream password
// is wrong fails to establish — libhoop authenticates against the upstream
// itself, so the bad password manifests as a failed ping/connection.
func TestMSSQL_BadCredentials(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	// Open a session whose env vars carry the wrong password. libhoop
	// will fail the upstream handshake; the client never reaches a
	// usable state.
	badMC := *mc
	badMC.Password = "WrongPassword!2024"
	sessionID := testutil.OpenMSSQLSession(t, tr, &badMC)
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMSSQL(t, tr, demux, &badMC, sessionID, "conn-bad")

	if err := client.PingWithTimeout(15 * time.Second); err == nil {
		t.Fatal("expected ping to fail with bad upstream credentials, got nil")
	}
}

// mssqlGuardRailRules blocks any statement containing the substring
// "secret_table" via an input deny-word rule. This is the exact JSON shape the
// gateway ships to the agent (see gateway encodeGuardRailRules): a single object
// with top-level input_rules/output_rules, not an array of {id,name,...}.
const mssqlGuardRailRules = `{"input_rules":[{"rules":[{"type":"deny_words_list","words":["secret_table"],"pattern_regex":"","message":"blocked by hoop guardrail: secret_table is off limits"}]}],"output_rules":[]}`

// dialMSSQLWithGuardRails is dialMSSQL with guardrail rules attached to the
// session's connection params.
func dialMSSQLWithGuardRails(t *testing.T, mc *testutil.MSSQLContainer, rules string) (*testutil.PipedMSSQLClient, func()) {
	t.Helper()
	agent, tr := startAgent(t)
	sessionID := testutil.OpenMSSQLSessionWithGuardRails(t, tr, mc, []byte(rules))
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMSSQL(t, tr, demux, mc, sessionID, "conn-1")
	return client, func() { shutdownAgent(t, agent, tr) }
}

// TestMSSQL_Guardrails_BlocksSQLBatch verifies a no-argument statement (sent as
// a TDS SQLBatch) that matches an input guardrail rule is blocked before
// reaching the server, surfaces to the client as a server error, and leaves the
// connection usable.
func TestMSSQL_Guardrails_BlocksSQLBatch(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQLWithGuardRails(t, mc, mssqlGuardRailRules)
	defer teardown()
	db := client.DB

	// A no-arg Exec is sent as a SQLBatch.
	_, err := db.Exec("SELECT 1 FROM secret_table")
	if err == nil {
		t.Fatal("expected guardrail to block the SQLBatch query, got nil error")
	}
	var sqlErr mssql.Error
	if !asMSSQLError(err, &sqlErr) {
		t.Fatalf("expected mssql.Error, got %T: %v", err, err)
	}
	if sqlErr.Number != 50000 {
		t.Errorf("expected guardrail error number 50000, got %d (%q)", sqlErr.Number, sqlErr.Message)
	}
	if !strings.Contains(sqlErr.Message, "secret_table") {
		t.Errorf("guardrail error should reference the rule, got %q", sqlErr.Message)
	}

	// The connection must survive a blocked statement.
	var n int
	if err := db.QueryRow("SELECT 1 AS n").Scan(&n); err != nil {
		t.Fatalf("connection did not survive guardrail block: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 after block, got %d", n)
	}
}

// TestMSSQL_Guardrails_BlocksRPCExecuteSql verifies a parameterized statement
// (which the driver sends as an RPC sp_executesql, statement in parameter 0) is
// blocked when it matches a rule — the RPC path, not just SQLBatch.
func TestMSSQL_Guardrails_BlocksRPCExecuteSql(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQLWithGuardRails(t, mc, mssqlGuardRailRules)
	defer teardown()
	db := client.DB

	// A parameterized Exec is sent as RPC sp_executesql.
	_, err := db.Exec("SELECT id FROM secret_table WHERE id = @p1", 1)
	if err == nil {
		t.Fatal("expected guardrail to block the sp_executesql query, got nil error")
	}
	var sqlErr mssql.Error
	if !asMSSQLError(err, &sqlErr) {
		t.Fatalf("expected mssql.Error, got %T: %v", err, err)
	}
	if sqlErr.Number != 50000 {
		t.Errorf("expected guardrail error number 50000, got %d (%q)", sqlErr.Number, sqlErr.Message)
	}

	// The connection must survive a blocked RPC statement too.
	var n int
	if err := db.QueryRow("SELECT 1 AS n").Scan(&n); err != nil {
		t.Fatalf("connection did not survive guardrail block: %v", err)
	}
}

// TestMSSQL_Guardrails_BlocksDynamicExecParam verifies the dynamic-SQL bypass is
// closed: a harmless-looking statement (EXEC(@p1)) whose blocked SQL rides in a
// string parameter is still blocked, because all character parameter values are
// inspected — not just the statement text.
func TestMSSQL_Guardrails_BlocksDynamicExecParam(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQLWithGuardRails(t, mc, mssqlGuardRailRules)
	defer teardown()
	db := client.DB

	// go-mssqldb sends this as sp_executesql N'EXEC(@p1)', N'@p1 ...', @p1=<value>.
	_, err := db.Exec("EXEC(@p1)", "SELECT 1 FROM secret_table")
	if err == nil {
		t.Fatal("expected guardrail to block the dynamic-exec parameter value, got nil")
	}
	var sqlErr mssql.Error
	if !asMSSQLError(err, &sqlErr) {
		t.Fatalf("expected mssql.Error, got %T: %v", err, err)
	}
	if sqlErr.Number != 50000 {
		t.Errorf("expected guardrail error number 50000, got %d (%q)", sqlErr.Number, sqlErr.Message)
	}
}

// TestMSSQL_Guardrails_AllowsCompliantQueries verifies that statements which do
// not match any rule flow through the guardrail path unchanged, for both the
// SQLBatch and the RPC sp_executesql paths.
func TestMSSQL_Guardrails_AllowsCompliantQueries(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	client, teardown := dialMSSQLWithGuardRails(t, mc, mssqlGuardRailRules)
	defer teardown()
	db := client.DB

	// SQLBatch path.
	var n int
	if err := db.QueryRow("SELECT 7 AS n").Scan(&n); err != nil {
		t.Fatalf("compliant SQLBatch was blocked unexpectedly: %v", err)
	}
	if n != 7 {
		t.Errorf("expected 7, got %d", n)
	}

	// RPC sp_executesql path.
	var m int
	if err := db.QueryRow("SELECT @p1 AS m", 42).Scan(&m); err != nil {
		t.Fatalf("compliant sp_executesql was blocked unexpectedly: %v", err)
	}
	if m != 42 {
		t.Errorf("expected 42, got %d", m)
	}
}

// asMSSQLError is errors.As specialized for mssql.Error, kept local to
// avoid an extra import in the test body.
func asMSSQLError(err error, target *mssql.Error) bool {
	for err != nil {
		if me, ok := err.(mssql.Error); ok {
			*target = me
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
