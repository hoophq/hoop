package lexer_test

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
)

func TestParameterize(t *testing.T) {
	for sql, want := range map[string]string{
		"SELECT * FROM t WHERE id = 42":                                            "SELECT * FROM t WHERE id = $1",
		"SELECT * FROM t WHERE a<>'x' AND b != 2 AND c>=3":                         "SELECT * FROM t WHERE a<>$1 AND b != $2 AND c>=$3",
		"SELECT * FROM t WHERE ts BETWEEN '2024-01-01' AND '2024-02-01' AND v = 1": "SELECT * FROM t WHERE ts BETWEEN $1 AND $2 AND v = $3",
		"SELECT * FROM t WHERE id IN (1, 2, 3)":                                    "SELECT * FROM t WHERE id IN ($1, $2, $3)",
		"INSERT INTO t (a, b) VALUES (1, 'x'), (2, 'y')":                           "INSERT INTO t (a, b) VALUES ($1, $2), ($3, $4)",
		"UPDATE t SET v = E'it''s' WHERE id = $$7$$":                               "UPDATE t SET v = $1 WHERE id = $2",
		"SELECT * FROM t WHERE (a, b) IN ((1, 2), (3, 4))":                         "SELECT * FROM t WHERE (a, b) IN (($1, $2), ($3, $4))",
		"SELECT * FROM t WHERE id='a'AND v=1":                                      "SELECT * FROM t WHERE id=$1 AND v=$2",
		"SELECT * FROM t WHERE x BETWEEN'a'AND'b'":                                 "SELECT * FROM t WHERE x BETWEEN $1 AND $2",
		"SELECT * FROM t /* id = 5 */ WHERE id = 5 -- = 6":                         "SELECT * FROM t /* id = 5 */ WHERE id = $1 -- = 6",
		"SELECT * FROM t WHERE x BETWEEN SYMMETRIC 1 AND 5":                        "SELECT * FROM t WHERE x BETWEEN SYMMETRIC 1 AND $1",
		"SELECT * FROM t WHERE id IN (lower('A'), 'b')":                            "SELECT * FROM t WHERE id IN (lower('A'), $1)",
		"SELECT * FROM t WHERE data->>'k' = 'v'":                                   "SELECT * FROM t WHERE data->>'k' = $1",
		"SELECT * FROM t WHERE x BETWEEN (1) AND 5 AND y = 2":                      "SELECT * FROM t WHERE x BETWEEN (1) AND $1 AND y = $2",
	} {
		got, n, ok := lexer.Parameterize(sql)
		if !ok || got != want || n != strings.Count(want, "$") {
			t.Errorf("Parameterize(%q) = %q, %d, %v; want %q", sql, got, n, ok, want)
		}
	}
}

func TestParameterizeKeepsLiteralsThatShapeThePlan(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM t LIMIT 10 OFFSET 20",
		"SELECT * FROM t WHERE name LIKE 'a%' OR name ILIKE 'b%' OR name ~ 'c'",
		"SELECT * FROM t ORDER BY 1, 2",
		"SELECT a, count(*) FROM t GROUP BY 1",
		"SELECT * FROM t WHERE d = DATE '2024-01-01' AND ts > now() - INTERVAL '1 day'",
		"SELECT * FROM t WHERE lower(name) = lower('X')",
		"SELECT * FROM t WHERE id = ANY('{1,2}') OR tags = ARRAY['a']",
		"SELECT * FROM t WHERE x = -5",
		"SELECT * FROM t WHERE doc @> '{\"a\": 1}' AND data->'k' IS NOT NULL",
		"SELECT * FROM t WHERE id IN (SELECT id FROM u WHERE k, 'x')",
		"SELECT 1, 'a' FROM t",
	} {
		got, n, ok := lexer.Parameterize(sql)
		if !ok || n != 0 || got != sql {
			t.Errorf("Parameterize(%q) = %q, %d, %v; want it unchanged", sql, got, n, ok)
		}
	}
}

func TestParameterizeRefuses(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM t WHERE id = $1 AND v = 'x'",
		"SELECT * FROM t WHERE v = 'open",
		"INSERT INTO t VALUES " + strings.Repeat("(1),", 65535) + "(1)",
	} {
		if got, n, ok := lexer.Parameterize(sql); ok {
			t.Errorf("Parameterize(%.60q) = %.60q, %d; want refused", sql, got, n)
		}
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
	if _, ok := lexer.PlanKey("SELECT 'open", lexer.Postgres); ok {
		t.Error("an unterminated literal scanned in full")
	}
	if _, ok := lexer.PlanKey(`SELECT 'a\'; DELETE FROM t; -- '`, lexer.MySQL); ok {
		t.Error("two MySQL readings gave one key")
	}
}
