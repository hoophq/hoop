package lexer_test

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
)

func TestMaxParam(t *testing.T) {
	for sql, want := range map[string]int{
		"SELECT 1":                                    0,
		"SELECT * FROM t WHERE id = $1":               1,
		"SELECT $2, $10, $3":                          10,
		"SELECT '$5', $$ $7 $$, \"$9\" FROM t -- $8":  0,
		"SELECT $tag$ $4 $tag$, $1 /* $6 */":          1,
		"UPDATE t SET v = $2 WHERE id = $1 AND x > 3": 2,
		"SELECT $1.f FROM t":                          1,
		"SELECT ($3).f, $2.g":                         3,
	} {
		got, ok := lexer.MaxParam(sql, lexer.Postgres)
		if !ok || got != want {
			t.Errorf("MaxParam(%q) = %d, %v; want %d", sql, got, ok, want)
		}
	}
	if _, ok := lexer.MaxParam("SELECT 'open", lexer.Postgres); ok {
		t.Error("an unterminated literal scanned in full")
	}
}
