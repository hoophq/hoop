//go:build integration

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const postgresImage = "postgres:17-alpine"

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        postgresImage,
			ExposedPorts: []string{"5432/tcp"},
			Env:          map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust"},
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Errorf("leaked postgres container: %v", err)
		}
	})
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort(host, port.Port())
}

func pgConnect(t *testing.T, addr string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, "postgres://postgres@"+addr+"/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("connect %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func pgExec(t *testing.T, conn *pgx.Conn, sql string, args ...any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
	defer cancel()
	_, err := conn.Exec(ctx, sql, args...)
	return err
}

// riskModel answers like the OpenAI API and records every user message. It
// rates high whatever the decide func says is high.
type riskModel struct {
	mu     sync.Mutex
	inputs []string
	url    string
}

func startRiskModel(t *testing.T, high func(content string) bool) *riskModel {
	t.Helper()
	m := &riskModel{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := req.Messages[len(req.Messages)-1].Content
		m.mu.Lock()
		m.inputs = append(m.inputs, content)
		m.mu.Unlock()
		tool := "report_low_risk"
		if high(content) {
			tool = "report_high_risk"
		}
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"tool_calls": []any{map[string]any{
				"function": map[string]string{"name": tool, "arguments": `{"title":"rated from the resource check","explanation":"e2e"}`},
			}}},
		}}})
	}))
	t.Cleanup(srv.Close)
	m.url = srv.URL
	return m
}

// input returns the last model input that contains marker.
func (m *riskModel) input(t *testing.T, marker string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.inputs) - 1; i >= 0; i-- {
		if strings.Contains(m.inputs[i], marker) {
			return m.inputs[i]
		}
	}
	t.Fatalf("no model input contains %q; got %q", marker, m.inputs)
	return ""
}

const resourceCheckConfig = `{
  "log_level": "debug",
  "audit": {"file": "-"},
  "analyzer": {
    "provider": "openai",
    "model": "e2e",
    "endpoint": "{{model}}",
    "credentials_file": "{{credentials}}",
    "fail_open": false
  },
  "listeners": [{
    "name": "pg",
    "protocol": "postgres",
    "listen": "{{listen}}",
    "upstream": "{{upstream}}",
    "analyzer": {
      "high": "block",
      "low": "allow",
      "message": "blocked by the resource check e2e",
      "resource_check": true,
      "cache": {"size": 1000, "ttl_sec": 60}
    }
  }]
}`

func startCheckingSidecar(t *testing.T, upstream, modelURL string) (*sidecar, *lockedBuffer) {
	t.Helper()
	cred := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := strings.NewReplacer("{{model}}", modelURL, "{{credentials}}", cred).Replace(resourceCheckConfig)
	logs := &lockedBuffer{}
	return startSidecar(t, upstream, config, func(l *sidecarLaunch) { l.stderr = logs }), logs
}

func seedTables(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	for _, stmt := range []string{
		"CREATE TABLE big (id int PRIMARY KEY, v text)",
		"INSERT INTO big SELECT g, 'v' || g FROM generate_series(1, 200000) g",
		"CREATE TABLE small (id int PRIMARY KEY, v text)",
		"INSERT INTO small SELECT g, 'v' || g FROM generate_series(1, 10) g",
		"ANALYZE big",
		"ANALYZE small",
	} {
		if err := pgExec(t, conn, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func TestPostgresResourceCheckReachesTheModel(t *testing.T) {
	pgAddr := startPostgres(t)
	direct := pgConnect(t, pgAddr)
	seedTables(t, direct)

	model := startRiskModel(t, func(content string) bool {
		return strings.Contains(content, "Seq Scan on big") && strings.Contains(content, "Table big: about 200K rows")
	})
	s, logs := startCheckingSidecar(t, pgAddr, model.url)

	t.Run("the same SELECT gets two risk levels", func(t *testing.T) {
		err := pgExec(t, pgConnect(t, s.addr), "SELECT * FROM big WHERE v = 'v42'", pgx.QueryExecModeSimpleProtocol)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || !strings.Contains(pgErr.Message, "blocked by the resource check e2e") {
			t.Fatalf("the large table was not blocked: %v", err)
		}
		if err := pgExec(t, pgConnect(t, s.addr), "SELECT * FROM small WHERE v = 'v42'", pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatalf("the small table was blocked: %v", err)
		}
		in := model.input(t, "FROM small")
		if !strings.Contains(in, "Table small: about 10 rows") || !strings.Contains(in, "Seq Scan on small") {
			t.Fatalf("the small table's check is not in the model input:\n%s", in)
		}
	})

	conn := pgConnect(t, s.addr)

	t.Run("a parameterized statement gets a generic plan", func(t *testing.T) {
		var v string
		ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
		defer cancel()
		if err := conn.QueryRow(ctx, "SELECT v FROM big WHERE id = $1", 7).Scan(&v); err != nil || v != "v7" {
			t.Fatalf("got %q, %v", v, err)
		}
		in := model.input(t, "WHERE id = $1")
		if !strings.Contains(in, "Index Scan on big using big_pkey") {
			t.Fatalf("no generic plan in the model input:\n%s", in)
		}
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM pg_prepared_statements WHERE name LIKE '_hoop_chk%'").Scan(&n); err != nil || n != 0 {
			t.Fatalf("the check left %d prepared statements (%v)", n, err)
		}
	})

	t.Run("a DELETE inside a transaction gets cached facts and sends no check", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
		defer cancel()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "DELETE FROM small WHERE id = 3"); err != nil {
			t.Fatal(err)
		}
		in := model.input(t, "DELETE FROM small WHERE id = 3")
		if !strings.Contains(in, "Table small: about 10 rows") || strings.Contains(in, "Plan:") {
			t.Fatalf("want cached facts and no plan:\n%s", in)
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a locked table keeps the facts and the session", func(t *testing.T) {
		locker := pgConnect(t, pgAddr)
		if err := pgExec(t, locker, "BEGIN"); err != nil {
			t.Fatal(err)
		}
		if err := pgExec(t, locker, "LOCK TABLE big IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(1500 * time.Millisecond)
			_ = pgExec(t, locker, "ROLLBACK")
		}()
		start := time.Now()
		var n int
		ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
		defer cancel()
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM big WHERE id < 5", pgx.QueryExecModeSimpleProtocol).Scan(&n); err != nil || n != 4 {
			t.Fatalf("got %d, %v", n, err)
		}
		if took := time.Since(start); took < time.Second {
			t.Fatalf("the statement ran in %s, before the lock was released", took)
		}
		in := model.input(t, "WHERE id < 5")
		if !strings.Contains(in, "Table big: about 200K rows") || strings.Contains(in, "Plan:") {
			t.Fatalf("want facts without a plan:\n%s", in)
		}
		if !strings.Contains(logs.String(), "lock_not_available") {
			t.Fatalf("the log does not say why the plan is missing:\n%s", logs.String())
		}
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM small").Scan(&n); err != nil || n != 10 {
			t.Fatalf("the session broke after the check: %d, %v", n, err)
		}
	})
}
