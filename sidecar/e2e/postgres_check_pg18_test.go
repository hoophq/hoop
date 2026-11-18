//go:build integration

package e2e_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// PostgreSQL 18 reports search_path, so the check follows a switch made inside
// a function and a connection that never goes idle still gets cached facts.
func TestPostgresResourceCheckFollowsTheReportedSearchPath(t *testing.T) {
	pgAddr := startPostgresImage(t, "postgres:18-alpine")
	direct := pgConnect(t, pgAddr)
	seedTables(t, direct)
	for _, stmt := range []string{
		"CREATE SCHEMA tenant_small",
		"CREATE SCHEMA tenant_big",
		"CREATE TABLE tenant_small.orders (id int, v text)",
		"INSERT INTO tenant_small.orders SELECT g, 'v' || g FROM generate_series(1, 10) g",
		"CREATE TABLE tenant_big.orders (id int, v text)",
		"INSERT INTO tenant_big.orders SELECT g, 'v' || g FROM generate_series(1, 200000) g",
		"ANALYZE tenant_small.orders",
		"ANALYZE tenant_big.orders",
		"CREATE FUNCTION public.use_tenant(s text) RETURNS text LANGUAGE sql AS $$ SELECT pg_catalog.set_config('search_path', s || ', public', false) $$",
	} {
		if err := pgExec(t, direct, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	model := startRiskModel(t, func(string) bool { return false })
	s, _ := startCheckingSidecar(t, pgAddr, model.url)

	t.Run("a function that switches the tenant", func(t *testing.T) {
		conn := pgConnect(t, s.addr)
		const stmt = "SELECT * FROM orders WHERE v = 'v7'"
		for _, tenant := range []string{"tenant_small", "tenant_big"} {
			if err := pgExec(t, conn, "SELECT public.use_tenant('"+tenant+"')", pgx.QueryExecModeSimpleProtocol); err != nil {
				t.Fatal(err)
			}
			if err := pgExec(t, conn, stmt, pgx.QueryExecModeSimpleProtocol); err != nil {
				t.Fatal(err)
			}
		}
		if in := model.input(t, stmt); !strings.Contains(in, "Table orders: about 200K rows") {
			t.Fatalf("the big tenant was judged on the small tenant's check:\n%s", in)
		}
	})

	t.Run("a new connection inside a transaction gets cached facts", func(t *testing.T) {
		if err := pgExec(t, pgConnect(t, s.addr), "SELECT count(*) FROM small", pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
		conn := pgConnect(t, s.addr)
		ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
		defer cancel()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "DELETE FROM small WHERE id = 4"); err != nil {
			t.Fatal(err)
		}
		in := model.input(t, "DELETE FROM small WHERE id = 4")
		if !strings.Contains(in, "Table small: about 10 rows") || strings.Contains(in, "Plan:") {
			t.Fatalf("want another connection's cached facts and no plan:\n%s", in)
		}
	})
}
