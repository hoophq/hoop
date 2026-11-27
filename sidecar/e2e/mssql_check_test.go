//go:build integration

package e2e_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const mssqlPass = "E2e-Secret-2026"

// checkMSSQL returns SIDECAR_E2E_MSSQL, a SQL Server whose sa password is
// SIDECAR_E2E_MSSQL_PASSWORD, or a new container.
func checkMSSQL(t *testing.T) (addr, password string) {
	t.Helper()
	if addr := os.Getenv("SIDECAR_E2E_MSSQL"); addr != "" {
		return addr, os.Getenv("SIDECAR_E2E_MSSQL_PASSWORD")
	}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mcr.microsoft.com/mssql/server:2022-latest",
			ExposedPorts: []string{"1433/tcp"},
			Env:          map[string]string{"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": mssqlPass},
			WaitingFor: wait.ForAll(
				wait.ForLog("SQL Server is now ready for client connections"),
				wait.ForListeningPort("1433/tcp"),
			).WithDeadline(4 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start mssql: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Errorf("leaked mssql container: %v", err)
		}
	})
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort(host, port.Port()), mssqlPass
}

func openMSSQL(t *testing.T, addr, password, database string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlserver", fmt.Sprintf("sqlserver://sa:%s@%s?database=%s&encrypt=disable&connection+timeout=20",
		password, addr, database))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	deadline := time.Now().Add(90 * time.Second)
	for {
		if err = db.Ping(); err == nil {
			return db
		}
		if time.Now().After(deadline) {
			t.Fatalf("ping: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// seedMSSQLCheck makes big with n rows and small with 10, neither with an
// index on v, in the database appdb.
func seedMSSQLCheck(t *testing.T, addr, password string, n int) {
	t.Helper()
	execAll(t, openMSSQL(t, addr, password, "master"),
		"IF DB_ID('appdb') IS NULL CREATE DATABASE appdb",
		"ALTER DATABASE appdb SET RECOVERY SIMPLE")
	db := openMSSQL(t, addr, password, "appdb")
	execAll(t, db,
		"DROP TABLE IF EXISTS big, small",
		"CREATE TABLE big (id INT IDENTITY PRIMARY KEY, v VARCHAR(32))",
		"CREATE TABLE small (id INT PRIMARY KEY, v VARCHAR(32))",
		"INSERT INTO small SELECT TOP 10 ROW_NUMBER() OVER (ORDER BY (SELECT NULL)), 'v' FROM sys.all_columns")
	for left := n; left > 0; left -= 500000 {
		execAll(t, db, fmt.Sprintf("INSERT INTO big (v) SELECT TOP %d CONCAT('v', ROW_NUMBER() OVER (ORDER BY (SELECT NULL))) "+
			"FROM sys.all_columns a CROSS JOIN sys.all_columns b", min(left, 500000)))
	}
	execAll(t, db, "UPDATE small SET v = CONCAT('v', id)", "UPDATE STATISTICS big", "UPDATE STATISTICS small")
}

func mssqlConfig(config string) string {
	return strings.Replace(config, `"protocol": "postgres"`, `"protocol": "mssql"`, 1)
}

// mssqlBlocked runs sql as one batch on a new connection through the sidecar.
func mssqlBlocked(t *testing.T, s *sidecar, password, sql, message string) bool {
	t.Helper()
	_, err := openMSSQL(t, s.addr, password, "appdb").Exec(sql)
	switch {
	case err == nil:
		return false
	case strings.Contains(err.Error(), message):
		return true
	}
	t.Fatalf("%s: %v", sql, err)
	return false
}

func TestMSSQLResourceCheckReachesTheModel(t *testing.T) {
	addr, password := checkMSSQL(t)
	seedMSSQLCheck(t, addr, password, 16384)
	model := startRiskModel(t, func(content string) bool {
		return strings.Contains(content, "Table big: about 16K rows")
	})
	cred := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	s := startSidecar(t, addr, mssqlConfig(strings.NewReplacer("{{model}}", model.url, "{{credentials}}", cred).Replace(resourceCheckConfig)),
		func(l *sidecarLaunch) { l.stderr = logs })
	const message = "blocked by the resource check e2e"

	t.Run("the same SELECT gets two risk levels", func(t *testing.T) {
		if !mssqlBlocked(t, s, password, "SELECT * FROM big WHERE v = 'v42'", message) {
			t.Fatal("the large table was not blocked")
		}
		if mssqlBlocked(t, s, password, "SELECT * FROM small WHERE v = 'v42'", message) {
			t.Fatal("the small table was blocked")
		}
		in := model.input(t, "FROM small WHERE v = 'v42'")
		i := strings.Index(in, "Resource check")
		if i < 0 || !strings.Contains(in[i:], "Table small: about 10 rows") || !strings.Contains(in[i:], "(primary key clustered (id))") ||
			strings.Contains(in[i:], "Plan:") || strings.Contains(in[i:], "v42") {
			t.Fatalf("the small table's check is not in the model input, or it quotes a literal:\n%s", in)
		}
		if !strings.Contains(logs.String(), `"msg":"resource check"`) {
			t.Fatalf("no resource check in the log")
		}
	})

	ctx := context.Background()
	conn, err := openMSSQL(t, s.addr, password, "appdb").Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	t.Run("the session keeps its state", func(t *testing.T) {
		if _, err := conn.ExecContext(ctx, "DELETE FROM small WHERE id > 8"); err != nil {
			t.Fatal(err)
		}
		var n, c int
		if err := conn.QueryRowContext(ctx, "SELECT @@ROWCOUNT, (SELECT COUNT(*) FROM small)").Scan(&n, &c); err != nil || n != 2 || c != 8 {
			t.Fatalf("@@ROWCOUNT %d, rows %d, %v: the check ran before a statement that reads the previous result", n, c, err)
		}
	})

	t.Run("a statement in a transaction gets no check", func(t *testing.T) {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, "UPDATE small SET v = 'x' WHERE id = 1"); err != nil {
			t.Fatal(err)
		}
		if in := model.input(t, "UPDATE small SET v = 'x'"); strings.Contains(in, "Resource check") {
			t.Fatalf("a check ran in a transaction:\n%s", in)
		}
	})
}

func TestMSSQLResourceCheckWithARealModel(t *testing.T) {
	analyzer := realAnalyzer(t)
	addr, password := checkMSSQL(t)
	seedMSSQLCheck(t, addr, password, 2000000)
	const onBig, onSmall = "SELECT * FROM big WHERE v = 'v42'", "SELECT * FROM small WHERE v = 'v42'"
	config := func(check bool) string {
		return strings.Replace(realModelConfig(t, analyzer, check), `"protocol":"postgres"`, `"protocol":"mssql"`, 1)
	}

	plain := startSidecar(t, addr, config(false))
	if mssqlBlocked(t, plain, password, onBig, "blocked by the analyzer") || mssqlBlocked(t, plain, password, onSmall, "blocked by the analyzer") {
		t.Fatal("without the check the model already rated a statement above low; the comparison proves nothing")
	}
	logs := &lockedBuffer{}
	checked := startSidecar(t, addr, config(true), func(l *sidecarLaunch) { l.stderr = logs })
	if !mssqlBlocked(t, checked, password, onBig, "blocked by the analyzer") {
		t.Errorf("with the check, the scan of 2M rows was rated low")
	}
	if mssqlBlocked(t, checked, password, onSmall, "blocked by the analyzer") {
		t.Errorf("with the check, the 10-row table was rated above low")
	}
	if !strings.Contains(logs.String(), `"msg":"resource check"`) {
		t.Errorf("no resource check in the log:\n%s", logs.String())
	}
}
