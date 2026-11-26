package lexer_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
)

func idKey(c string) bool { return c == "id" || c == "Email" }

var placeholder = regexp.MustCompile(`\$[0-9]`)

func TestParameterize(t *testing.T) {
	for sql, want := range map[string]string{
		"SELECT * FROM t WHERE id = 42":                                 "SELECT * FROM t WHERE id = $1",
		"SELECT * FROM t WHERE t.ID<>'x' AND \"Email\" != 'a'":          "SELECT * FROM t WHERE t.ID<>$1 AND \"Email\" != $2",
		"SELECT * FROM t WHERE id IN (1, 2, 3)":                         "SELECT * FROM t WHERE id IN ($1, $2, $3)",
		"SELECT * FROM t WHERE id NOT IN ('a')":                         "SELECT * FROM t WHERE id NOT IN ($1)",
		"INSERT INTO t (a, b) VALUES (1, 'x'), (2.5, 'y')":              "INSERT INTO t (a, b) VALUES ($1, $2), ($3, $4)",
		"UPDATE t SET v = E'it''s', n = 1.5 WHERE id = $$7$$":           "UPDATE t SET v = $1, n = $2 WHERE id = $3",
		"UPDATE t SET v = 'x' WHERE status = 'done'":                    "UPDATE t SET v = $1 WHERE status = 'done'",
		"SELECT * FROM t WHERE id='a'AND v=1":                           "SELECT * FROM t WHERE id=$1 AND v=1",
		"SELECT * FROM t /* id = 5 */ WHERE id = 5 -- = 6":              "SELECT * FROM t /* id = 5 */ WHERE id = $1 -- = 6",
		"INSERT INTO t VALUES (1, ARRAY[1, 2], lower('A'))":             "INSERT INTO t VALUES ($1, ARRAY[1, 2], lower('A'))",
		"INSERT INTO t VALUES (1) ON CONFLICT (id) DO UPDATE SET n = 2": "INSERT INTO t VALUES ($1) ON CONFLICT (id) DO UPDATE SET n = $2",
	} {
		got, n, ok := lexer.Parameterize(sql, idKey)
		if !ok || got != want || n != len(placeholder.FindAllString(want, -1)) {
			t.Errorf("Parameterize(%q) = %q, %d, %v; want %q", sql, got, n, ok, want)
		}
	}
}

func TestParameterizeKeepsLiteralsThatChangeTheEstimate(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM t WHERE id > 5 OR id <= 9 OR id BETWEEN 1 AND 2",
		"SELECT * FROM t WHERE status = 'done' AND tenant IN (1, 2)",
		"SELECT * FROM t WHERE id = 1.5 OR id = 1e3 OR id = 99999999999999999999",
		"SELECT * FROM t LIMIT 10 OFFSET 20",
		"SELECT * FROM t WHERE email LIKE 'a%' OR email ~ 'c'",
		"SELECT * FROM t ORDER BY 1, 2",
		"SELECT * FROM t WHERE id = -5",
		"SELECT * FROM t WHERE data->>'id' = 'v'",
		"SELECT * FROM t WHERE id IN (SELECT id FROM t WHERE id = 5)",
		"WITH c AS (SELECT owner AS id FROM t) SELECT * FROM c WHERE id = 5",
		"SELECT * FROM t WHERE lower(id) = 'x'",
	} {
		got, n, ok := lexer.Parameterize(sql, idKey)
		if !ok || n != 0 || got != sql {
			t.Errorf("Parameterize(%q) = %q, %d, %v; want it unchanged", sql, got, n, ok)
		}
	}
	if got, n, _ := lexer.Parameterize("SELECT * FROM t WHERE id = 5", nil); n != 0 || got != "SELECT * FROM t WHERE id = 5" {
		t.Errorf("no unique key, got %q", got)
	}
}

func TestParameterizeRefuses(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM t WHERE id = $1 AND v = 'x'",
		"SELECT * FROM t WHERE v = 'open",
		`DELETE FROM t WHERE v = 'a\' AND id = 2 -- ' OR n > 0`,
		"INSERT INTO t VALUES " + strings.Repeat("(1),", 65535) + "(1)",
	} {
		if got, n, ok := lexer.Parameterize(sql, idKey); ok {
			t.Errorf("Parameterize(%.60q) = %.60q, %d; want refused", sql, got, n)
		}
	}
	if _, _, ok := lexer.Parameterize(`SELECT * FROM t WHERE id = E'a\'b'`, idKey); !ok {
		t.Error("a backslash both readings agree on was refused")
	}
}

func TestPlanKey(t *testing.T) {
	same := [][]string{
		{"SELECT * FROM t WHERE id = $1", "select *  from T\n where id=$1 /*traceparent='00-ab'*/", "-- app\nSELECT * FROM t WHERE id = $1"},
		{"/*+ SeqScan(t) */ SELECT * FROM t", "/*+ SeqScan(t) */\nselect * from t -- app"},
	}
	for _, group := range same {
		first, ok := lexer.PlanKey(group[0], lexer.Postgres)
		for _, sql := range group[1:] {
			if got, ok2 := lexer.PlanKey(sql, lexer.Postgres); !ok || !ok2 || got != first {
				t.Errorf("PlanKey(%q) = %q, want %q", sql, got, first)
			}
		}
	}
	differ := [][2]string{
		{"SELECT * FROM t WHERE id = 1", "SELECT * FROM t WHERE id = 2"},
		{"SELECT * FROM t WHERE n LIKE 'a%'", "SELECT * FROM t WHERE n LIKE '%a'"},
		{`SELECT * FROM "T"`, "SELECT * FROM t"},
		{"SELECT * FROM Ábc", "SELECT * FROM ábc"},
		{"SELECT * FROM t", "/*+ SeqScan(t) */ SELECT * FROM t"},
		{"SELECT * FROM t WHERE a <= 1", "SELECT * FROM t WHERE a < = 1"},
	}
	for _, pair := range differ {
		a, _ := lexer.PlanKey(pair[0], lexer.Postgres)
		b, _ := lexer.PlanKey(pair[1], lexer.Postgres)
		if a == b {
			t.Errorf("PlanKey(%q) == PlanKey(%q) = %q", pair[0], pair[1], a)
		}
	}
	for _, sql := range []string{"SELECT 'open", `SELECT 'a\' AND 1 -- '`} {
		if _, ok := lexer.PlanKey(sql, lexer.Postgres); ok {
			t.Errorf("PlanKey(%q) has a key", sql)
		}
	}
	if _, ok := lexer.PlanKey(`SELECT 'a\'; DELETE FROM t; -- '`, lexer.MySQL); ok {
		t.Error("two MySQL readings gave one key")
	}
}
