//go:build integration

package e2e_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkMySQL returns SIDECAR_E2E_MYSQL, a server with the suite's user and
// database, or a new container.
func checkMySQL(t *testing.T) string {
	t.Helper()
	if addr := os.Getenv("SIDECAR_E2E_MYSQL"); addr != "" {
		return addr
	}
	return startMySQL(t)
}

func execAll(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// seedMySQLCheck makes big with 2^n rows and small with 10, neither with an
// index on v.
func seedMySQLCheck(t *testing.T, addr string, n int) {
	t.Helper()
	db := open(t, addr)
	execAll(t, db,
		"DROP TABLE IF EXISTS big, small",
		"CREATE TABLE big (id INT PRIMARY KEY AUTO_INCREMENT, v VARCHAR(32))",
		"INSERT INTO big (v) VALUES ('v1')",
		"CREATE TABLE small (id INT PRIMARY KEY, v VARCHAR(32))",
		"INSERT INTO small VALUES (1,'v1'),(2,'v2'),(3,'v3'),(4,'v4'),(5,'v5'),(6,'v6'),(7,'v7'),(8,'v8'),(9,'v9'),(10,'v10')")
	for range n {
		execAll(t, db, "INSERT INTO big (v) SELECT CONCAT('v', id + (SELECT MAX(id) FROM big)) FROM big")
	}
	execAll(t, db, "ANALYZE TABLE big, small")
}

func startMySQLCheckingSidecar(t *testing.T, upstream, modelURL string) (*sidecar, *lockedBuffer) {
	t.Helper()
	cred := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := strings.NewReplacer("{{model}}", modelURL, "{{credentials}}", cred,
		`"protocol": "postgres"`, `"protocol": "mysql"`).Replace(resourceCheckConfig)
	logs := &lockedBuffer{}
	return startSidecar(t, upstream, config, func(l *sidecarLaunch) { l.stderr = logs }), logs
}

// mysqlBlocked runs sql on a new connection through the sidecar.
func mysqlBlocked(t *testing.T, s *sidecar, sql, message string) bool {
	t.Helper()
	db := s.dial(t, "")
	_, err := db.Exec(sql)
	switch {
	case err == nil:
		return false
	case strings.Contains(err.Error(), message):
		return true
	}
	t.Fatalf("%s: %v", sql, err)
	return false
}

func TestMySQLResourceCheckReachesTheModel(t *testing.T) {
	addr := checkMySQL(t)
	seedMySQLCheck(t, addr, 14)
	model := startRiskModel(t, func(content string) bool {
		return strings.Contains(content, "Full table scan on big") && strings.Contains(content, "Table big: about 16K rows")
	})
	s, logs := startMySQLCheckingSidecar(t, addr, model.url)
	const message = "blocked by the resource check e2e"

	t.Run("the same SELECT gets two risk levels", func(t *testing.T) {
		if !mysqlBlocked(t, s, "SELECT * FROM big WHERE v = 'v42'", message) {
			t.Fatal("the large table was not blocked")
		}
		if mysqlBlocked(t, s, "SELECT * FROM small WHERE v = 'v42'", message) {
			t.Fatal("the small table was blocked")
		}
		in := model.input(t, "FROM small WHERE v = 'v42'")
		i := strings.Index(in, "Resource check")
		if i < 0 || !strings.Contains(in[i:], "Table small: about 10 rows") || !strings.Contains(in[i:], "Full table scan on small") ||
			strings.Contains(in[i:], "v42") {
			t.Fatalf("the small table's check is not in the model input, or it quotes a literal:\n%s", in)
		}
		if !strings.Contains(logs.String(), `"msg":"resource check"`) {
			t.Fatalf("no resource check in the log")
		}
	})

	ctx := context.Background()
	conn, err := s.dial(t, "").Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	t.Run("the session keeps its state", func(t *testing.T) {
		var lwt int
		if err := conn.QueryRowContext(ctx, "SELECT @@SESSION.lock_wait_timeout").Scan(&lwt); err != nil || lwt == 1 {
			t.Fatalf("lock_wait_timeout %d, %v", lwt, err)
		}
		if _, err := conn.ExecContext(ctx, "DELETE FROM small WHERE id > 8"); err != nil {
			t.Fatal(err)
		}
		var n, c int
		if err := conn.QueryRowContext(ctx, "SELECT ROW_COUNT(), (SELECT COUNT(*) FROM small)").Scan(&n, &c); err != nil || n != 2 || c != 8 {
			t.Fatalf("ROW_COUNT() %d, rows %d, %v: the check ran before a statement that reads the previous result", n, c, err)
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

func TestMySQLResourceCheckWithARealModel(t *testing.T) {
	analyzer := realAnalyzer(t)
	addr := checkMySQL(t)
	seedMySQLCheck(t, addr, 21)
	const onBig, onSmall = "SELECT * FROM big WHERE v = 'v42'", "SELECT * FROM small WHERE v = 'v42'"
	config := func(check bool) string {
		return strings.Replace(realModelConfig(t, analyzer, check), `"protocol":"postgres"`, `"protocol":"mysql"`, 1)
	}

	plain := startSidecar(t, addr, config(false))
	if mysqlBlocked(t, plain, onBig, "blocked by the analyzer") || mysqlBlocked(t, plain, onSmall, "blocked by the analyzer") {
		t.Fatal("without the check the model already rated a statement above low; the comparison proves nothing")
	}
	logs := &lockedBuffer{}
	checked := startSidecar(t, addr, config(true), func(l *sidecarLaunch) { l.stderr = logs })
	if !mysqlBlocked(t, checked, onBig, "blocked by the analyzer") {
		t.Errorf("with the check, the scan of 2M rows was rated low")
	}
	if mysqlBlocked(t, checked, onSmall, "blocked by the analyzer") {
		t.Errorf("with the check, the 10-row table was rated above low")
	}
	if !strings.Contains(logs.String(), `"msg":"resource check"`) {
		t.Errorf("no resource check in the log:\n%s", logs.String())
	}
}
