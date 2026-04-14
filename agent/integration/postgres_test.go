//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/agent/controller"
	"github.com/hoophq/hoop/agent/integration/testutil"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

const pgTestTimeout = 30 * time.Second

func TestPG_SimpleQuery(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	connID := "conn-1"

	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery("SELECT 1 AS num"))
	pkts := testutil.CollectPGResponses(t, tr, pgTestTimeout)

	results := testutil.ExtractAllQueryResults(pkts)
	if len(results) != 1 {
		t.Fatalf("expected 1 query result, got %d", len(results))
	}

	result := results[0]
	if len(result.Columns) != 1 || result.Columns[0] != "num" {
		t.Errorf("expected columns [num], got %v", result.Columns)
	}
	if result.RowCount() != 1 {
		t.Fatalf("expected 1 row, got %d", result.RowCount())
	}
	row := result.Row(0)
	if len(row) != 1 || row[0] != "1" {
		t.Errorf("expected row [1], got %v", row)
	}
	if result.CommandTag != "SELECT 1" {
		t.Errorf("expected command tag 'SELECT 1', got %q", result.CommandTag)
	}

	shutdownAgent(t, agent, tr)
}

func TestPG_CreateInsertSelectUpdateDelete(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	connID := "conn-1"
	tableName := fmt.Sprintf("test_crud_%d", time.Now().UnixNano())

	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	connectAndQuery := func(t *testing.T, sql string) []testutil.QueryResult {
		testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery(sql))
		pkts := testutil.CollectPGResponses(t, tr, pgTestTimeout)
		return testutil.ExtractAllQueryResults(pkts)
	}

	t.Run("CreateTable", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("CREATE TABLE %s (id serial PRIMARY KEY, name text NOT NULL)", tableName))
		if len(results) == 0 || results[0].CommandTag != "CREATE TABLE" {
			t.Fatalf("expected CREATE TABLE, got %v", results)
		}
	})

	t.Run("Insert", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("INSERT INTO %s (name) VALUES ('alice')", tableName))
		if len(results) == 0 || results[0].CommandTag != "INSERT 0 1" {
			t.Fatalf("expected INSERT 0 1, got %v", results)
		}
	})

	t.Run("Select", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("SELECT id, name FROM %s", tableName))
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].RowCount() != 1 {
			t.Fatalf("expected 1 row, got %d", results[0].RowCount())
		}
		row := results[0].Row(0)
		if row[1] != "alice" {
			t.Errorf("expected name 'alice', got %q", row[1])
		}
	})

	t.Run("Update", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("UPDATE %s SET name = 'bob' WHERE name = 'alice'", tableName))
		if len(results) == 0 || results[0].CommandTag != "UPDATE 1" {
			t.Fatalf("expected UPDATE 1, got %v", results)
		}
	})

	t.Run("SelectAfterUpdate", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("SELECT name FROM %s", tableName))
		if len(results) != 1 || results[0].RowCount() != 1 {
			t.Fatalf("expected 1 row, got %d", results[0].RowCount())
		}
		row := results[0].Row(0)
		if row[0] != "bob" {
			t.Errorf("expected name 'bob', got %q", row[0])
		}
	})

	t.Run("Delete", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("DELETE FROM %s", tableName))
		if len(results) == 0 || results[0].CommandTag != "DELETE 1" {
			t.Fatalf("expected DELETE 1, got %v", results)
		}
	})

	t.Run("SelectAfterDelete", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("SELECT count(*) FROM %s", tableName))
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].RowCount() != 1 {
			t.Fatalf("expected 1 row, got %d", results[0].RowCount())
		}
		row := results[0].Row(0)
		if row[0] != "0" {
			t.Errorf("expected count 0, got %q", row[0])
		}
	})

	t.Run("DropTable", func(t *testing.T) {
		results := connectAndQuery(t, fmt.Sprintf("DROP TABLE %s", tableName))
		if len(results) == 0 || results[0].CommandTag != "DROP TABLE" {
			t.Fatalf("expected DROP TABLE, got %v", results)
		}
	})

	shutdownAgent(t, agent, tr)
}

func TestPG_TransactionCommit(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	connID := "conn-1"
	tableName := fmt.Sprintf("test_tx_commit_%d", time.Now().UnixNano())

	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	runQuery := func(sql string) []testutil.QueryResult {
		testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery(sql))
		pkts := testutil.CollectPGResponses(t, tr, pgTestTimeout)
		return testutil.ExtractAllQueryResults(pkts)
	}

	runQuery(fmt.Sprintf("CREATE TABLE %s (id serial PRIMARY KEY, val text)", tableName))
	runQuery(fmt.Sprintf("INSERT INTO %s (val) VALUES ('committed')", tableName))
	runQuery(fmt.Sprintf("SELECT val FROM %s WHERE val = 'committed'", tableName))

	runQuery("BEGIN")
	runQuery(fmt.Sprintf("INSERT INTO %s (val) VALUES ('tx_committed')", tableName))
	runQuery("COMMIT")

	results := runQuery(fmt.Sprintf("SELECT val FROM %s WHERE val = 'tx_committed'", tableName))
	if len(results) == 0 || results[0].RowCount() != 1 {
		t.Fatalf("expected row 'tx_committed' to exist after COMMIT, got %v", results)
	}

	runQuery(fmt.Sprintf("DROP TABLE %s", tableName))
	shutdownAgent(t, agent, tr)
}

func TestPG_TransactionRollback(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	connID := "conn-1"
	tableName := fmt.Sprintf("test_tx_rollback_%d", time.Now().UnixNano())

	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	runQuery := func(sql string) []testutil.QueryResult {
		testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery(sql))
		pkts := testutil.CollectPGResponses(t, tr, pgTestTimeout)
		return testutil.ExtractAllQueryResults(pkts)
	}

	runQuery(fmt.Sprintf("CREATE TABLE %s (id serial PRIMARY KEY, val text)", tableName))

	runQuery("BEGIN")
	runQuery(fmt.Sprintf("INSERT INTO %s (val) VALUES ('rolled_back')", tableName))
	runQuery("ROLLBACK")

	results := runQuery(fmt.Sprintf("SELECT count(*) FROM %s WHERE val = 'rolled_back'", tableName))
	if len(results) == 0 || results[0].RowCount() != 1 {
		t.Fatalf("expected 1 result row, got %v", results)
	}
	row := results[0].Row(0)
	if row[0] != "0" {
		t.Errorf("expected count 0 after ROLLBACK, got %q", row[0])
	}

	runQuery(fmt.Sprintf("DROP TABLE %s", tableName))
	shutdownAgent(t, agent, tr)
}

func TestPG_MultipleSequentialQueries(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	connID := "conn-1"

	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	runQuery := func(sql string) []testutil.QueryResult {
		testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery(sql))
		pkts := testutil.CollectPGResponses(t, tr, pgTestTimeout)
		return testutil.ExtractAllQueryResults(pkts)
	}

	expected := map[string]string{
		"SELECT 1 AS a":    "1",
		"SELECT 2 AS a":    "2",
		"SELECT 3 AS a":    "3",
		"SELECT 'hello'":   "hello",
		"SELECT true AS b": "t",
	}

	for sql, expectedVal := range expected {
		t.Run(sql, func(t *testing.T) {
			results := runQuery(sql)
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			row := results[0].Row(0)
			if row[0] != expectedVal {
				t.Errorf("expected %q, got %q", expectedVal, row[0])
			}
		})
	}

	shutdownAgent(t, agent, tr)
}

func TestPG_MultipleConcurrentConnections(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)

	tableName := fmt.Sprintf("test_multi_conn_%d", time.Now().UnixNano())
	connID1 := "conn-a"
	connID2 := "conn-b"

	// Handshake each connection exactly once
	testutil.SendPGConnectHandshake(t, tr, sessionID, connID1, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)
	testutil.SendPGConnectHandshake(t, tr, sessionID, connID2, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	runQuery := func(connID, sql string) []testutil.QueryResult {
		testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery(sql))
		pkts := testutil.CollectPGResponses(t, tr, pgTestTimeout)
		return testutil.ExtractAllQueryResults(pkts)
	}

	runQuery(connID1, fmt.Sprintf("CREATE TABLE %s (id serial PRIMARY KEY, conn_id text)", tableName))
	runQuery(connID1, fmt.Sprintf("INSERT INTO %s (conn_id) VALUES ('a')", tableName))
	runQuery(connID2, fmt.Sprintf("INSERT INTO %s (conn_id) VALUES ('b')", tableName))

	resultsA := runQuery(connID1, fmt.Sprintf("SELECT conn_id FROM %s WHERE conn_id = 'a'", tableName))
	resultsB := runQuery(connID2, fmt.Sprintf("SELECT conn_id FROM %s WHERE conn_id = 'b'", tableName))

	if len(resultsA) != 1 || resultsA[0].RowCount() != 1 || resultsA[0].Row(0)[0] != "a" {
		t.Errorf("conn-a: expected to find 'a', got %v", resultsA)
	}
	if len(resultsB) != 1 || resultsB[0].RowCount() != 1 || resultsB[0].Row(0)[0] != "b" {
		t.Errorf("conn-b: expected to find 'b', got %v", resultsB)
	}

	runQuery(connID1, fmt.Sprintf("DROP TABLE %s", tableName))
	shutdownAgent(t, agent, tr)
}

func TestPG_SessionTeardown(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	connID := "conn-1"

	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery("SELECT 1"))
	testutil.CollectPGResponses(t, tr, pgTestTimeout)

	closePkt := &pb.Packet{
		Type: pbagent.TCPConnectionClose,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(connID),
		},
	}
	tr.Inject(closePkt)

	shutdownAgent(t, agent, tr)
}

func TestPG_ErrorBadQuery(t *testing.T) {
	pg := testutil.StartPostgres(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenPGSession(t, tr, pg.Host, pg.Port, pg.User, pg.Password, pg.Database)
	connID := "conn-1"

	testutil.SendPGConnectHandshake(t, tr, sessionID, connID, pg.User, pg.Database)
	testutil.WaitForPGReady(t, tr, pgTestTimeout)

	testutil.SendPGWrite(t, tr, sessionID, connID, testutil.PGSimpleQuery("SELECT * FROM nonexistent_table_xyz"))
	pkts := testutil.CollectPGResponses(t, tr, pgTestTimeout)

	foundError := false
	foundReady := false
	for _, pkt := range pkts {
		msgs := testutil.ParsePGMessages(pkt.Payload)
		for _, msg := range msgs {
			if msg.Type == 'E' {
				errFields := msg.AsErrorResponse()
				sqlState, ok := errFields['C']
				if ok && sqlState == "42P01" {
					foundError = true
				}
				msgStr, ok := errFields['M']
				if ok {
					if !strings.Contains(strings.ToLower(msgStr), "nonexistent_table_xyz") {
						t.Errorf("error message should reference the table, got: %s", msgStr)
					}
				}
			}
			if msg.Type == 'Z' {
				foundReady = true
			}
		}
	}

	if !foundError {
		t.Error("expected ErrorResponse for nonexistent table, got none")
	}
	if !foundReady {
		t.Error("expected ReadyForQuery after error (connection should survive)")
	}

	shutdownAgent(t, agent, tr)
}

func TestPG_ErrorBadCredentials(t *testing.T) {
	pg := testutil.StartPostgres(t)
	_, tr := startAgent(t)

	badEnvVars := testutil.BuildPGEnvVars(pg.Host, pg.Port, pg.User, "wrongpassword", pg.Database, "disable")
	pkt := testutil.BuildSessionOpenPacket("bad-cred-session", string(pb.ConnectionTypePostgres), badEnvVars)
	tr.Inject(pkt)

	// Wait for SessionOpenOK, then send the PG handshake
	openOK := tr.ExpectType(t, pbclient.SessionOpenOK, 15*time.Second)
	_ = openOK

	testutil.SendPGConnectHandshake(t, tr, "bad-cred-session", "conn-1", pg.User, pg.Database)

	// The libhoop proxy should fail authentication and the agent should
	// send either a PG ErrorResponse or a SessionClose with an error message.
	timeout := 15 * time.Second
	deadline := time.After(timeout)
	gotAuthError := false
	for !gotAuthError {
		select {
		case resp := <-tr.RecvCh():
			switch resp.Type {
			case pbclient.SessionClose:
				payload := string(resp.Payload)
				if len(payload) == 0 {
					t.Fatal("expected SessionClose with error payload for bad credentials, got empty payload")
				}
				// The error message should reference authentication failure
				gotAuthError = true
			case pbclient.PGConnectionWrite:
				msgs := testutil.ParsePGMessages(resp.Payload)
				for _, msg := range msgs {
					if msg.Type == 'E' {
						errFields := msg.AsErrorResponse()
						sqlState := errFields['C']
						// 28P01 = invalid_password, 28000 = invalid_authorization, 08006 = connection_failure
						if sqlState == "28P01" || sqlState == "28000" || sqlState == "08006" {
							gotAuthError = true
						}
					}
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for auth error response for bad credentials")
		}
	}
}

func startAgent(t *testing.T) (*controller.Agent, *testutil.MockTransport) {
	tr := testutil.NewMockTransport()
	cfg := &config.Config{
		Token:     "test-token",
		URL:       "localhost:8010",
		AgentMode: pb.AgentModeStandardType,
	}
	agent := controller.New(tr, cfg, nil)

	done := make(chan error, 1)
	go func() {
		done <- agent.Run()
	}()

	time.Sleep(100 * time.Millisecond)

	t.Cleanup(func() {
		agent.Close(fmt.Errorf("test cleanup"))
	})

	return agent, tr
}

func shutdownAgent(t *testing.T, agent *controller.Agent, tr *testutil.MockTransport) {
	agent.Close(fmt.Errorf("test shutdown"))
	_, _ = tr.Close()
}
