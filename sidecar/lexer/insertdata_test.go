package lexer_test

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/lexer"
)

func TestInsertDataSplitsTheRowsOff(t *testing.T) {
	for _, tc := range []struct {
		sql, head string
	}{
		{"INSERT INTO t FORMAT JSONEachRow\n{\"a\":\"O'Brien\"}", "INSERT INTO t FORMAT JSONEachRow"},
		{"INSERT INTO db.t (a, format) FORMAT TSV\n1\tx", "INSERT INTO db.t (a, format) FORMAT TSV"},
		{"insert into t settings async_insert=1 format RowBinary\x00\x01\xff", "insert into t settings async_insert=1 format RowBinary"},
		{"INSERT INTO format FORMAT CSV\n1", "INSERT INTO format FORMAT CSV"},
		{"INSERT INTO db.format FORMAT CSV", "INSERT INTO db.format FORMAT CSV"},
		{"INSERT INTO FUNCTION format(CSV, 'x') FORMAT TSV\n", "INSERT INTO FUNCTION format(CSV, 'x') FORMAT TSV"},
		{"/* c */ INSERT INTO t FORMAT CSV\n1,'a'", "/* c */ INSERT INTO t FORMAT CSV"},
	} {
		head, ok := lexer.InsertData(tc.sql, lexer.ClickHouse)
		if !ok || head != tc.head {
			t.Errorf("InsertData(%q) = %q, %v; want %q", tc.sql, head, ok, tc.head)
		}
	}
}

func TestInsertDataLeavesOtherStatementsWhole(t *testing.T) {
	for _, sql := range []string{
		"SELECT * FROM t FORMAT JSON",
		"INSERT INTO t SELECT * FROM s FORMAT TSV",
		"INSERT INTO t WITH x AS (SELECT 1) SELECT * FROM x",
		"INSERT INTO t VALUES (1, 'a')",
		"INSERT INTO t FORMAT Values (1, (SELECT secret FROM s))",
		"INSERT INTO t FORMAT `values` (1)",
		"INSERT INTO t FORMAT `Val\\x75es` (1, (SELECT secret FROM s))",
		"INSERT INTO t FORMAT `TSV`\nDROP TABLE t",
		"INSERT INTO t; DROP TABLE t FORMAT TSV",
		"INSERT INTO t FORMAT",
		"INSERT INTO t ('a FORMAT TSV",
		"DROP TABLE t",
		"",
	} {
		if head, ok := lexer.InsertData(sql, lexer.ClickHouse); ok {
			t.Errorf("InsertData(%q) split at %q", sql, head)
		}
	}
	if _, ok := lexer.InsertData("INSERT INTO t FORMAT TSV\n1", lexer.Postgres); ok {
		t.Error("only ClickHouse carries rows after FORMAT")
	}
}
