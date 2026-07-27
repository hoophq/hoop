//go:build integration

package integration

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hoophq/hoop/agent/integration/testutil"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
)

const mysqlTestTimeout = 30 * time.Second

// dialMySQL wires up the common per-test scaffolding: start the agent,
// open a MySQL session, start the demux, and build the bridged client.
// Returns the client plus a teardown that shuts the agent down. The
// ordering (OpenMySQLSession before StartRecvDemux before DialPipedMySQL)
// matters — see the helper docs.
func dialMySQL(t *testing.T, mc *testutil.MySQLContainer) (*testutil.PipedMySQLClient, func()) {
	t.Helper()
	agent, tr := startAgent(t)
	sessionID := testutil.OpenMySQLSession(t, tr, mc)
	demux := testutil.StartRecvDemux(t, tr)
	connID := "conn-1"
	client := testutil.DialPipedMySQL(t, tr, demux, mc, sessionID, connID)
	return client, func() { shutdownAgent(t, agent, tr) }
}

// TestMySQL_Ping is the end-to-end smoke test: a successful ping forces
// the full bridged handshake (server greeting → client handshake response
// → libhoop-driven upstream auth → OK) through processMySQLProtocol and
// libhoop's MySQL proxy.
func TestMySQL_Ping(t *testing.T) {
	mc := testutil.StartMySQL(t)
	client, teardown := dialMySQL(t, mc)
	defer teardown()

	if err := client.PingWithTimeout(mysqlTestTimeout); err != nil {
		t.Fatalf("mysql ping through agent failed: %v", err)
	}
}

// TestMySQL_SimpleQuery runs a trivial SELECT and verifies the scalar
// result round-trips through the proxy.
func TestMySQL_SimpleQuery(t *testing.T) {
	mc := testutil.StartMySQL(t)
	client, teardown := dialMySQL(t, mc)
	defer teardown()

	var n int
	if err := client.DB.QueryRow("SELECT 1 AS num").Scan(&n); err != nil {
		t.Fatalf("SELECT 1 failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

// TestMySQL_CreateInsertSelectUpdateDelete exercises the full DML/DDL
// lifecycle over a single bridged connection.
func TestMySQL_CreateInsertSelectUpdateDelete(t *testing.T) {
	mc := testutil.StartMySQL(t)
	client, teardown := dialMySQL(t, mc)
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
		exec(t, fmt.Sprintf("CREATE TABLE %s (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(64) NOT NULL)", table))
	})

	t.Run("Insert", func(t *testing.T) {
		res := exec(t, fmt.Sprintf("INSERT INTO %s (name) VALUES (?)", table), "alice")
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
		if err := db.QueryRow(fmt.Sprintf("SELECT name FROM %s WHERE name = ?", table), "alice").Scan(&name); err != nil {
			t.Fatalf("select failed: %v", err)
		}
		if name != "alice" {
			t.Errorf("expected 'alice', got %q", name)
		}
	})

	t.Run("Update", func(t *testing.T) {
		res := exec(t, fmt.Sprintf("UPDATE %s SET name = ? WHERE name = ?", table), "bob", "alice")
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

// TestMySQL_MultiRowResultSet verifies a result set spanning multiple
// rows decodes correctly end-to-end (column metadata + row packets +
// EOF/OK terminator all flow through the proxy).
func TestMySQL_MultiRowResultSet(t *testing.T) {
	mc := testutil.StartMySQL(t)
	client, teardown := dialMySQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_multirow_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT PRIMARY KEY, val VARCHAR(32))", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP TABLE %s", table))

	for i := 1; i <= 5; i++ {
		if _, err := db.Exec(fmt.Sprintf("INSERT INTO %s (id, val) VALUES (?, ?)", table), i, fmt.Sprintf("v%d", i)); err != nil {
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

// TestMySQL_TransactionCommit verifies a committed transaction persists
// across the proxy.
func TestMySQL_TransactionCommit(t *testing.T) {
	mc := testutil.StartMySQL(t)
	client, teardown := dialMySQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_tx_commit_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT AUTO_INCREMENT PRIMARY KEY, val VARCHAR(32)) ENGINE=InnoDB", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP TABLE %s", table))

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (val) VALUES (?)", table), "committed"); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE val = ?", table), "committed").Scan(&count); err != nil {
		t.Fatalf("select: %v", err)
	}
	if count != 1 {
		t.Errorf("expected committed row to persist, count=%d", count)
	}
}

// TestMySQL_TransactionRollback verifies a rolled-back transaction leaves
// no trace.
func TestMySQL_TransactionRollback(t *testing.T) {
	mc := testutil.StartMySQL(t)
	client, teardown := dialMySQL(t, mc)
	defer teardown()

	db := client.DB
	table := fmt.Sprintf("test_tx_rollback_%d", time.Now().UnixNano())
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (id INT AUTO_INCREMENT PRIMARY KEY, val VARCHAR(32)) ENGINE=InnoDB", table)); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Exec(fmt.Sprintf("DROP TABLE %s", table))

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (val) VALUES (?)", table), "rolled_back"); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE val = ?", table), "rolled_back").Scan(&count); err != nil {
		t.Fatalf("select: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after rollback, got %d", count)
	}
}

// TestMySQL_ErrorBadQuery verifies a server-side SQL error surfaces to the
// client as a *mysql.MySQLError (proving the error packet round-trips) and
// that the connection survives for a follow-up query.
func TestMySQL_ErrorBadQuery(t *testing.T) {
	mc := testutil.StartMySQL(t)
	client, teardown := dialMySQL(t, mc)
	defer teardown()

	db := client.DB
	_, err := db.Exec("SELECT * FROM nonexistent_table_xyz")
	if err == nil {
		t.Fatal("expected error for nonexistent table, got nil")
	}
	var myErr *mysql.MySQLError
	if !asMySQLError(err, &myErr) {
		t.Fatalf("expected *mysql.MySQLError, got %T: %v", err, err)
	}
	// 1146 = ER_NO_SUCH_TABLE
	if myErr.Number != 1146 {
		t.Errorf("expected error 1146 (no such table), got %d (%s)", myErr.Number, myErr.Message)
	}
	if !strings.Contains(strings.ToLower(myErr.Message), "nonexistent_table_xyz") {
		t.Errorf("error message should reference the table, got: %s", myErr.Message)
	}

	// Connection must survive the error.
	var n int
	if err := db.QueryRow("SELECT 1").Scan(&n); err != nil {
		t.Fatalf("connection did not survive query error: %v", err)
	}
}

// TestMySQL_BadCredentials verifies that a session whose upstream password
// is wrong fails to establish — libhoop authenticates against the upstream
// itself, so the bad password manifests as a failed ping/connection.
func TestMySQL_BadCredentials(t *testing.T) {
	mc := testutil.StartMySQL(t)
	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)

	// Open a session whose env vars carry the wrong password. libhoop
	// will fail the upstream handshake; the client never reaches a
	// usable state.
	badMC := *mc
	badMC.Password = "wrongpassword"
	sessionID := testutil.OpenMySQLSession(t, tr, &badMC)
	demux := testutil.StartRecvDemux(t, tr)
	client := testutil.DialPipedMySQL(t, tr, demux, &badMC, sessionID, "conn-bad")

	if err := client.PingWithTimeout(15 * time.Second); err == nil {
		t.Fatal("expected ping to fail with bad upstream credentials, got nil")
	}
}

// TestMySQL_CloseKillsRunningQuery verifies that closing a client
// connection while a statement is still executing terminates the backend
// thread via the KILL CONNECTION side channel in libhoop's proxy Close.
//
// It provisions a user whose grants are scoped to the test schema only —
// a regression guard for the kill side connection forcing the `mysql`
// system schema in its DSN, which such users cannot open (ERR 1044),
// silently leaving the query running after the client was gone.
func TestMySQL_CloseKillsRunningQuery(t *testing.T) {
	mc := testutil.StartMySQL(t)

	// Sidecar root connection to provision the restricted user and to
	// observe the server's processlist directly.
	rootDB, err := sql.Open("mysql", mc.ConnString())
	if err != nil {
		t.Fatalf("failed opening root connection: %v", err)
	}
	defer rootDB.Close()

	const user, pass = "kill_test_user", "kill_test_pass"
	if _, err := rootDB.Exec(fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s'", user, pass)); err != nil {
		t.Fatalf("failed creating restricted user: %v", err)
	}
	if _, err := rootDB.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%%'", mc.Database, user)); err != nil {
		t.Fatalf("failed granting privileges: %v", err)
	}

	restricted := *mc
	restricted.User = user
	restricted.Password = pass

	agent, tr := startAgent(t)
	defer shutdownAgent(t, agent, tr)
	sessionID := testutil.OpenMySQLSession(t, tr, &restricted)
	demux := testutil.StartRecvDemux(t, tr)
	connID := "conn-1"
	client := testutil.DialPipedMySQL(t, tr, demux, &restricted, sessionID, connID)

	// Establish the bridged connection before starting the long query.
	if err := client.PingWithTimeout(mysqlTestTimeout); err != nil {
		t.Fatalf("mysql ping through agent failed: %v", err)
	}

	// Long-running statement; it is expected to die with an error once
	// the backend thread is killed, so the result is discarded.
	go func() {
		rows, err := client.DB.Query("SELECT SLEEP(120)")
		if err == nil {
			rows.Close()
		}
	}()
	waitForSleepingQuery(t, rootDB, user, true, 15*time.Second)

	tr.Inject(&pb.Packet{
		Type: pbagent.TCPConnectionClose,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
	})

	// The SLEEP statement must vanish from the processlist well before
	// its 120s run time, proving the backend thread was killed rather
	// than orphaned.
	waitForSleepingQuery(t, rootDB, user, false, 15*time.Second)
}

// waitForSleepingQuery polls information_schema.PROCESSLIST until a SELECT
// SLEEP statement owned by user is present (present=true) or gone
// (present=false), failing the test on timeout.
func waitForSleepingQuery(t *testing.T, db *sql.DB, user string, present bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var count int
		err := db.QueryRow(
			"SELECT count(*) FROM information_schema.PROCESSLIST WHERE USER = ? AND INFO LIKE 'SELECT SLEEP%'",
			user).Scan(&count)
		if err != nil {
			t.Fatalf("failed querying processlist: %v", err)
		}
		if (count > 0) == present {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for sleeping query owned by %q (want present=%v, count=%d)",
				timeout, user, present, count)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// asMySQLError is errors.As specialized for *mysql.MySQLError, kept local
// to avoid an extra import in the test body.
func asMySQLError(err error, target **mysql.MySQLError) bool {
	for err != nil {
		if me, ok := err.(*mysql.MySQLError); ok {
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
