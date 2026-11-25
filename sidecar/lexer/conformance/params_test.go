package conformance

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
	pgquery "github.com/wasilibs/go-pgquery"
)

var paramRef = regexp.MustCompile(`"ParamRef":\s*\{\s*"number":\s*(\d+)`)

func TestMaxParamMatchesPostgres(t *testing.T) {
	cases := []string{
		"SELECT $1.f FROM t",
		"SELECT ($3).f, $2.g FROM t",
		"SELECT $4::int, '$9', $$ $8 $$, \"$7\" FROM t -- $6",
		"SELECT $tag$ $5 $tag$, $1 /* $6 */",
		"UPDATE t SET v = $2 WHERE id = $1",
		"SELECT * FROM t WHERE a = $10 OR b = $9",
		"INSERT INTO t VALUES ($1, $2), ($3, $1) RETURNING $2",
	}
	for _, c := range corpus() {
		cases = append(cases, c.sql)
	}
	for _, sql := range cases {
		got, ok := lexer.MaxParam(sql, lexer.Postgres)
		if !ok {
			continue
		}
		tree, err := pgquery.ParseToJSON(sql)
		if err != nil {
			t.Fatalf("oracle could not parse %q: %v", sql, err)
		}
		want := 0
		for _, m := range paramRef.FindAllStringSubmatch(tree, -1) {
			v, _ := strconv.Atoi(m[1])
			want = max(want, v)
		}
		if got != want {
			t.Errorf("MaxParam(%q) = %d, PostgreSQL %d", sql, got, want)
		}
	}
}
