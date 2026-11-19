//go:build integration

package e2e_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestPostgresResourceCheckSeesAnUnreportedRoleSwitch(t *testing.T) {
	pgAddr := startPostgresImage(t, "postgres:18-alpine")
	direct := pgConnect(t, pgAddr)
	seedTables(t, direct)
	for _, stmt := range []string{
		"CREATE ROLE e2e_alice LOGIN",
		"CREATE ROLE e2e_bob",
		"GRANT e2e_bob TO e2e_alice",
		"CREATE SCHEMA e2e_alice AUTHORIZATION e2e_alice",
		"CREATE SCHEMA e2e_bob AUTHORIZATION e2e_bob",
		"CREATE TABLE e2e_alice.ledger (id int, v text)",
		"INSERT INTO e2e_alice.ledger SELECT g, 'v' || g FROM generate_series(1, 10) g",
		"CREATE TABLE e2e_bob.ledger (id int, v text)",
		"INSERT INTO e2e_bob.ledger SELECT g, 'v' || g FROM generate_series(1, 200000) g",
		"ANALYZE e2e_alice.ledger",
		"ANALYZE e2e_bob.ledger",
		"GRANT ALL ON ALL TABLES IN SCHEMA e2e_alice, e2e_bob TO e2e_alice, e2e_bob",
		"CREATE FUNCTION public.act_as(r text) RETURNS text LANGUAGE sql AS $$ SELECT pg_catalog.set_config('role', r, false) $$",
	} {
		if err := pgExec(t, direct, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	model := startRiskModel(t, func(string) bool { return false })
	s, _ := startCheckingSidecar(t, pgAddr, model.url)

	t.Run("a function that switches the role", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), stmtTimeout)
		conn, err := pgx.Connect(ctx, "postgres://e2e_alice@"+s.addr+"/postgres?sslmode=disable")
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close(context.Background()) })
		const stmt = "SELECT * FROM ledger WHERE v = 'v7'"
		if err := pgExec(t, conn, stmt, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
		if in := model.input(t, stmt); !strings.Contains(in, "Table ledger: about 10 rows") {
			t.Fatalf("alice's ledger is not in the model input:\n%s", in)
		}
		if err := pgExec(t, conn, "SELECT public.act_as('e2e_bob')", pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
		if err := pgExec(t, conn, stmt, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
		if in := model.input(t, stmt); !strings.Contains(in, "Table ledger: about 200K rows") {
			t.Fatalf("bob was judged on alice's check:\n%s", in)
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
