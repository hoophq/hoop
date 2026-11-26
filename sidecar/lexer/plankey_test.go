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
		"SELECT * FROM t WHERE id = 42":                                    "SELECT * FROM t WHERE id = $1",
		"SELECT * FROM t WHERE t.ID<>'x' AND \"Email\" != 'a'":             "SELECT * FROM t WHERE t.ID<>$1 AND \"Email\" != $2",
		"SELECT * FROM t WHERE id IN (1, 2, 3)":                            "SELECT * FROM t WHERE id IN ($1, $2, $3)",
		"SELECT * FROM t WHERE id NOT IN ('a')":                            "SELECT * FROM t WHERE id NOT IN ($1)",
		"INSERT INTO t (a, b) VALUES (1, 'x'), (2.5, 'y')":                 "INSERT INTO t (a, b) VALUES ($1, $2), ($3, $4)",
		"UPDATE t SET v = E'it''s', n = 1.5 WHERE id = $$7$$":              "UPDATE t SET v = $1, n = $2 WHERE id = $3",
		"UPDATE t SET v = 'x' WHERE status = 'done'":                       "UPDATE t SET v = $1 WHERE status = 'done'",
		"SELECT * FROM t WHERE id='a'AND v=1":                              "SELECT * FROM t WHERE id=$1 AND v=1",
		"SELECT * FROM t /* id = 5 */ WHERE id = 5 -- = 6":                 "SELECT * FROM t /* id = 5 */ WHERE id = $1 -- = 6",
		"INSERT INTO t VALUES (1, ARRAY[1, 2], lower('A'))":                "INSERT INTO t VALUES ($1, ARRAY[1, 2], lower('A'))",
		"INSERT INTO t VALUES (1) ON CONFLICT (id) DO UPDATE SET n = 2":    "INSERT INTO t VALUES ($1) ON CONFLICT (id) DO UPDATE SET n = $2",
		"DELETE FROM ONLY s.t AS x WHERE x.id = 5 RETURNING *":             "DELETE FROM ONLY s.t AS x WHERE x.id = $1 RETURNING *",
		"UPDATE t x SET n = 1 WHERE t.id = 5 AND x.id IS DISTINCT FROM 6":  "UPDATE t x SET n = $1 WHERE t.id = $2 AND x.id IS DISTINCT FROM 6",
		"SELECT extract(year FROM ts) FROM t WHERE id = 5 FOR UPDATE;":     "SELECT extract(year FROM ts) FROM t WHERE id = $1 FOR UPDATE;",
		"DELETE FROM t WHERE NOT id = 5 OR (t.id = 6) OR w = 1 AND id = 7": "DELETE FROM t WHERE NOT id = $1 OR (t.id = $2) OR w = 1 AND id = $3",
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
		"DELETE FROM t USING (VALUES ('open')) v(s) WHERE t.status = v.s",
		"UPDATE t SET n = n + 1 FROM (VALUES (1, 'a')) v(k, s) WHERE t.k = v.k",
		"SELECT * FROM t WHERE status IN (VALUES ('a'), ('b'))",
		"SELECT * FROM t WHERE set IS NOT NULL AND status = 'open'",
		"INSERT INTO t SELECT * FROM (VALUES (1)) v(x)",
		"SELECT * FROM t WHERE lower(id) = 'x'",
		"DELETE FROM t USING (VALUES ('closed', 7)) v(s, id) WHERE t.status = v.s AND v.id = 8",
		"UPDATE t SET n = n FROM (VALUES (7)) v(id) WHERE v.id = 8",
		"DELETE FROM t WHERE status IN (SELECT s FROM (VALUES ('a', 7)) v(s, id) WHERE id = 8)",
		"DELETE FROM t WHERE id = 8 AND EXISTS (SELECT 1)",
		"SELECT * FROM t JOIN u ON u.k = t.k WHERE id = 5",
		"SELECT * FROM t, generate_series(1, 9) g(id) WHERE id = 5",
		"SELECT * FROM t AS x(k, id) WHERE id = 5",
		"SELECT * FROM t WHERE o.id = 5 OR (r).id = 5 OR s.t.id = 5 OR k::id = 5",
		"SELECT * FROM t WHERE id = 5 UNION SELECT * FROM u WHERE id = 6",
		"INSERT INTO t SELECT * FROM u WHERE id = 5",
		"SELECT * FROM t TABLESAMPLE system (1) WHERE id = 5",
		"SELECT * FROM t WHERE id = 5 AND v IS DISTINCT FROM 'a' AND w FROM u",
		"SELECT id = 5",
		"DELETE FROM t WHERE w - id = 1 OR w || t.id = 'a' OR -id = 3 OR w - id IN (1, 2)",
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
