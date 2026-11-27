//go:build integration

package e2e_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	detailTitle       = "unbounded delete"
	detailExplanation = "The DELETE has no WHERE clause, so it removes every row of the table."
)

const denyDetailConfig = `{
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
    "analyzer": {"high": "block", "low": "allow"}
  }]
}`

// A blocked statement reaches psql with the model's explanation as DETAIL.
// Neither the audit trail nor the process log carries it.
func TestPostgresAnalyzerBlockSendsTheExplanationAsDetail(t *testing.T) {
	pgAddr := startPostgres(t)
	if err := pgExec(t, pgConnect(t, pgAddr), "CREATE TABLE orders (id int PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"tool_calls": []any{map[string]any{
				"function": map[string]string{"name": "report_high_risk",
					"arguments": `{"title":"` + detailTitle + `","explanation":"` + detailExplanation + `"}`},
			}}},
		}}})
	}))
	t.Cleanup(model.Close)
	cred := filepath.Join(t.TempDir(), "openai-key")
	if err := os.WriteFile(cred, []byte("sk-e2e"), 0o600); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	s := startSidecar(t, pgAddr,
		strings.NewReplacer("{{model}}", model.URL, "{{credentials}}", cred).Replace(denyDetailConfig),
		func(l *sidecarLaunch) { l.stderr = logs })

	err := pgExec(t, pgConnect(t, s.addr), "DELETE FROM orders", pgx.QueryExecModeSimpleProtocol)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("the DELETE was not refused with a Postgres error: %v", err)
	}
	if pgErr.Message != detailTitle || pgErr.Detail != detailExplanation {
		t.Errorf("message %q, detail %q", pgErr.Message, pgErr.Detail)
	}

	s.waitForAudit(t, "the blocked DELETE", func(ev auditEvent) bool {
		return ev.Kind == "violation" && ev.Statement == "DELETE FROM orders"
	})
	for _, line := range s.auditLines() {
		if strings.Contains(line, "WHERE clause") {
			t.Errorf("the audit trail carries the explanation: %s", line)
		}
	}
	if strings.Contains(logs.String(), "WHERE clause") {
		t.Errorf("the process log carries the explanation:\n%s", logs.String())
	}
}
