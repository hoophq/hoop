//go:build integration

package e2e_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SIDECAR_E2E_ANALYZER is a top-level analyzer block for a real provider, e.g.
// {"provider":"vertex","model":"gemini-2.5-flash","extra":{"project":"p","region":"global","publisher":"google"}}.
func realAnalyzer(t *testing.T) map[string]any {
	t.Helper()
	raw := os.Getenv("SIDECAR_E2E_ANALYZER")
	if raw == "" {
		t.Skip("SIDECAR_E2E_ANALYZER is not set")
	}
	var a map[string]any
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("SIDECAR_E2E_ANALYZER: %v", err)
	}
	a["fail_open"] = false
	return a
}

func realModelConfig(t *testing.T, analyzer map[string]any, check bool) string {
	t.Helper()
	cfg := map[string]any{
		"log_level": "debug",
		"audit":     map[string]any{"file": "-"},
		"analyzer":  analyzer,
		"listeners": []any{map[string]any{
			"name": "pg", "protocol": "postgres", "listen": "{{listen}}", "upstream": "{{upstream}}",
			"analyzer": map[string]any{
				"high": "block", "medium": "block", "low": "allow",
				"message":        "blocked by the analyzer",
				"resource_check": check,
			},
		}},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func blocked(t *testing.T, addr, sql string) bool {
	t.Helper()
	err := pgExec(t, pgConnect(t, addr), sql, pgx.QueryExecModeSimpleProtocol)
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return false
	case errors.As(err, &pgErr) && strings.Contains(pgErr.Message, "blocked by the analyzer"):
		return true
	}
	t.Fatalf("%s: %v", sql, err)
	return false
}

func TestPostgresResourceCheckWithARealModel(t *testing.T) {
	analyzer := realAnalyzer(t)
	pgAddr := startPostgres(t)
	direct := pgConnect(t, pgAddr)
	seedTables(t, direct)
	for _, stmt := range []string{
		"INSERT INTO big SELECT g, 'v' || g FROM generate_series(200001, 3000000) g",
		"ANALYZE big",
	} {
		if err := pgExec(t, direct, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	const onBig, onSmall = "SELECT * FROM big WHERE v = 'v42'", "SELECT * FROM small WHERE v = 'v42'"

	plain := startSidecar(t, pgAddr, realModelConfig(t, analyzer, false))
	if blocked(t, plain.addr, onBig) || blocked(t, plain.addr, onSmall) {
		t.Fatal("without the check the model already rated a statement above low; the comparison proves nothing")
	}

	logs := &lockedBuffer{}
	checked := startSidecar(t, pgAddr, realModelConfig(t, analyzer, true), func(l *sidecarLaunch) { l.stderr = logs })
	if !blocked(t, checked.addr, onBig) {
		t.Errorf("with the check, the scan of 3M rows was rated low")
	}
	if blocked(t, checked.addr, onSmall) {
		t.Errorf("with the check, the 10-row table was rated above low")
	}
	if !strings.Contains(logs.String(), `"msg":"resource check"`) {
		t.Errorf("no resource check in the log:\n%s", logs.String())
	}
}
