package http_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// clickhouseSQL is the block a lane in front of ClickHouse's HTTP interface
// sets: the SQL is the query parameter, then the body.
func clickhouseSQL() *codechttp.SQLOptions {
	return &codechttp.SQLOptions{
		Dialect:       lexer.ClickHouse,
		DialectName:   "clickhouse",
		Params:        map[string]string{"/**": "query"},
		MatchResource: policy.MatchResource,
	}
}

func request(method, target, headers, body string) string {
	return fmt.Sprintf("%s %s HTTP/1.1\r\nHost: h\r\n%sContent-Length: %d\r\n\r\n%s", method, target, headers, len(body), body)
}

// sqlOf decodes one request and returns the SQL statement after it, failing
// when there is not exactly one.
func sqlOf(t *testing.T, o codechttp.Options, raw string) inspect.Statement {
	t.Helper()
	stmts := mustDecode(t, codechttp.New(o), raw)
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want the request and its SQL: %+v", len(stmts), stmts)
	}
	if stmts[0].Metadata[codechttp.MetadataSQLStatements] != "1" {
		t.Errorf("request metadata = %v", stmts[0].Metadata)
	}
	return stmts[1]
}

// ClickHouse takes its SQL in the query parameter, in the body, or split
// between them with a line feed; every shape is read and classified.
func TestSQLParamReadsEveryClickHouseShape(t *testing.T) {
	for _, tc := range []struct {
		name, method, target, body string
		text                       string
		op                         inspect.Operation
	}{
		{"GET query", "GET", "/?query=SELECT%20name%20FROM%20appdb.customers&database=appdb", "",
			"SELECT name FROM appdb.customers", inspect.OpSelect},
		{"POST query", "POST", "/?query=DROP+TABLE+appdb.customers", "", "DROP TABLE appdb.customers", inspect.OpDrop},
		{"body only", "POST", "/", "DELETE FROM appdb.customers WHERE id = 1",
			"DELETE FROM appdb.customers WHERE id = 1", inspect.OpDelete},
		{"split", "POST", "/?query=DROP%20TABLE", "appdb.customers", "DROP TABLE\nappdb.customers", inspect.OpDrop},
		{"select then a drop in the body", "POST", "/?query=SELECT%201", "; DROP TABLE appdb.customers",
			"SELECT 1\n; DROP TABLE appdb.customers", inspect.OpDrop},
		{"name in another case", "GET", "/?QUERY=DROP%20TABLE%20appdb.customers", "", "DROP TABLE appdb.customers", inspect.OpDrop},
		{"escaped name", "GET", "/?database=appdb&qu%65ry=DROP%20TABLE%20appdb.customers", "", "DROP TABLE appdb.customers", inspect.OpDrop},
		{"a dot segment", "GET", "/x/../?query=DROP%20TABLE%20appdb.customers", "", "DROP TABLE appdb.customers", inspect.OpDrop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql := sqlOf(t, codechttp.Options{SQL: clickhouseSQL()}, request(tc.method, tc.target, "", tc.body))
			if sql.Text != tc.text || sql.Operation != tc.op || sql.HTTP != nil {
				t.Errorf("SQL statement %q %s, want %q %s", sql.Text, sql.Operation, tc.text, tc.op)
			}
			if !slices.Equal(sql.Tables, []string{"appdb.customers"}) {
				t.Errorf("tables = %v", sql.Tables)
			}
			for k, want := range map[string]string{
				codechttp.MetadataSQLField:    "?query",
				codechttp.MetadataSQLDialect:  "clickhouse",
				codechttp.MetadataSQLResource: "/",
				codechttp.MetadataSQLMethod:   strings.ToLower(tc.method),
			} {
				if got := sql.Metadata[k]; got != want {
					t.Errorf("metadata %s = %q, want %q", k, got, want)
				}
			}
			if r, ok := sql.Metadata[inspect.MetadataSQLIncomplete]; ok {
				t.Errorf("read as incomplete: %s", r)
			}
		})
	}

	// ClickHouse 25.3 runs /query?query= as it runs /?query=.
	sql := sqlOf(t, codechttp.Options{SQL: clickhouseSQL()}, request("POST", "/query?query=DROP%20TABLE%20t", "", ""))
	if sql.Operation != inspect.OpDrop || sql.Metadata[codechttp.MetadataSQLResource] != "/query" {
		t.Errorf("/query: %q %s %v", sql.Text, sql.Operation, sql.Metadata)
	}

	// curl --data sends a form content type, and ClickHouse reads the body as SQL.
	sql = sqlOf(t, codechttp.Options{SQL: clickhouseSQL()},
		request("POST", "/", "Content-Type: application/x-www-form-urlencoded\r\n", "SELECT 7 % 3 FROM appdb.customers"))
	if sql.Operation != inspect.OpSelect {
		t.Errorf("a form-typed SQL body: %q %s", sql.Text, sql.Operation)
	}
}

// A request that carries no SQL, a health check or a page of the web UI,
// is the request alone.
func TestSQLParamNoSQLIsNoStatement(t *testing.T) {
	for _, raw := range []string{
		"GET /ping HTTP/1.1\r\nHost: h\r\n\r\n",
		"GET /?database=appdb HTTP/1.1\r\nHost: h\r\n\r\n",
		request("POST", "/?query=", "", ""),
		request("POST", "/", "", "  \n"),
	} {
		if stmts := mustDecode(t, codechttp.New(codechttp.Options{SQL: clickhouseSQL()}), raw); len(stmts) != 1 {
			t.Errorf("%q produced %d statements: %+v", raw, len(stmts), stmts)
		}
	}
}

// The rows of an INSERT ... FORMAT are data the server parses with that
// format, never SQL: the statement is judged as an insert on its table
// whatever the rows hold, how large they are or how they are encoded.
func TestSQLParamInsertRowsAreNotReadAsSQL(t *testing.T) {
	small := codechttp.Options{SQL: clickhouseSQL(), MaxBodyBytes: 64}
	for _, tc := range []struct {
		name, target, headers, body string
		text                        string
	}{
		{"binary rows under lz4", "/?query=INSERT%20INTO%20appdb.customers%20FORMAT%20RowBinary",
			"Content-Encoding: lz4\r\n", "\x04\x22\x4d\x18\x00\xff", "INSERT INTO appdb.customers FORMAT RowBinary"},
		{"rows past max_body_bytes", "/?query=INSERT%20INTO%20appdb.customers%20FORMAT%20JSONEachRow",
			"", strings.Repeat(`{"name":"O'Brien"}`+"\n", 20), "INSERT INTO appdb.customers FORMAT JSONEachRow"},
		{"statement and rows in the body", "/", "",
			"INSERT INTO appdb.customers FORMAT TSV\nO'Brien\tDROP TABLE x\n" + strings.Repeat("a\tb\n", 40),
			"INSERT INTO appdb.customers FORMAT TSV"},
		{"statement split across both", "/?query=INSERT%20INTO%20appdb.customers", "",
			"FORMAT CSV\n1,\"x\"", "INSERT INTO appdb.customers\nFORMAT CSV"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql := sqlOf(t, small, request("POST", tc.target, tc.headers, tc.body))
			if sql.Text != tc.text || sql.Operation != inspect.OpInsert || !slices.Equal(sql.Tables, []string{"appdb.customers"}) {
				t.Errorf("SQL statement %q %s %v", sql.Text, sql.Operation, sql.Tables)
			}
			if sql.Metadata[codechttp.MetadataSQLInlineRows] != "true" {
				t.Errorf("metadata = %v, want the inline rows marked", sql.Metadata)
			}
			if r, ok := sql.Metadata[inspect.MetadataSQLIncomplete]; ok {
				t.Errorf("read as incomplete: %s", r)
			}
		})
	}
}

// SQL the lane should read and cannot is an unknown statement with the
// reason recorded, so a rule naming unknown refuses it. Nothing binary
// reaches its text.
func TestSQLParamUnreadableIsUnknown(t *testing.T) {
	cutAtFormat := "INSERT INTO t /*" + strings.Repeat("x", 40) + "*/ FORMAT Val"
	for _, tc := range []struct {
		name, target, headers, body string
		maxBody                     int
		reason                      string
	}{
		{"compressed body", "/", "Content-Encoding: gzip\r\n", "DROP TABLE appdb.customers", 0, "Content-Encoding gzip"},
		{"compressed body after a SELECT", "/?query=SELECT%201", "Content-Encoding: zstd\r\n", "x", 0, "Content-Encoding zstd"},
		{"multipart", "/", "Content-Type: multipart/form-data; boundary=b\r\n",
			"--b\r\nContent-Disposition: form-data; name=\"query\"\r\n\r\nDROP TABLE t\r\n--b--\r\n", 0, "multipart"},
		{"multipart after an INSERT", "/?query=INSERT%20INTO%20t%20FORMAT%20TSV", "Content-Type: multipart/form-data; boundary=b\r\n",
			"--b--\r\n", 0, "multipart"},
		{"form field", "/?query=SELECT%201", "Content-Type: application/x-www-form-urlencoded\r\n", "x=1&query=DROP+TABLE+t", 0, "form body"},
		{"truncated", "/", "", "SELECT 1 /*" + strings.Repeat("x", 200) + "*/; DROP TABLE t", 64, "max_body_bytes"},
		{"truncated in the format name", "/", "", cutAtFormat + "ues (1, (SELECT secret FROM s))", len(cutAtFormat), "max_body_bytes"},
		{"bad escape in a name", "/?qu%ZZery=DROP%20TABLE%20t", "", "", 0, "malformed percent-escape"},
		{"bad escape in the SQL", "/?query=DROP%20TABLE%20t%ZZ", "", "", 0, "malformed percent-escape"},
		{"binary body", "/", "", "\x02\x00\x00\x00\x0cDROP TABLE t", 0, "not text"},
		{"invalid UTF-8", "/", "", "SELECT '\xff'", 0, "not text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql := sqlOf(t, codechttp.Options{SQL: clickhouseSQL(), MaxBodyBytes: tc.maxBody}, request("POST", tc.target, tc.headers, tc.body))
			if sql.Operation != inspect.OpUnknown {
				t.Errorf("operation %s, want unknown", sql.Operation)
			}
			if r := sql.Metadata[inspect.MetadataSQLIncomplete]; !strings.Contains(r, tc.reason) {
				t.Errorf("reason %q does not say %q", r, tc.reason)
			}
			if strings.ContainsAny(sql.Text, "\x00\x02\xff") {
				t.Errorf("binary bytes reached the text: %q", sql.Text)
			}
		})
	}
}

// Two values for the SQL parameter are refused: reading one would leave a
// DROP in the other unread.
func TestSQLParamTwiceIsRefused(t *testing.T) {
	for _, target := range []string{
		"/?query=SELECT%201&query=DROP%20TABLE%20t",
		"/?query=SELECT%201&Query=DROP%20TABLE%20t",
	} {
		_, err := decodeClient(t, codechttp.New(codechttp.Options{SQL: clickhouseSQL()}), request("GET", target, "", ""))
		if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), "twice") {
			t.Errorf("%s: err = %v, want the request refused", target, err)
		}
	}
}

// ClickHouse drops a fragment from the request-target and runs the body
// alone, so a query parameter after # would be judged and never run.
func TestSQLParamFragmentIsRefused(t *testing.T) {
	for _, target := range []string{
		"/?max_threads=1#&query=SELECT%201",
		"/#?query=SELECT%201",
	} {
		_, err := decodeClient(t, codechttp.New(codechttp.Options{SQL: clickhouseSQL()}), request("POST", target, "", "DROP TABLE t"))
		if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), "#") {
			t.Errorf("%s: err = %v, want the request refused", target, err)
		}
	}
}

// libhoop and some servers also end a pair at ;, ClickHouse does not, so a ;
// that changes the SQL parameter is refused.
func TestSQLParamSemicolonIsRefused(t *testing.T) {
	for _, target := range []string{
		"/?max_threads=1;query=DROP%20TABLE%20t",
		"/?query=SELECT%201;query=DROP%20TABLE%20t",
		"/?query=SELECT%201;DROP%20TABLE%20t",
	} {
		_, err := decodeClient(t, codechttp.New(codechttp.Options{SQL: clickhouseSQL()}), request("GET", target, "", ""))
		if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), ";") {
			t.Errorf("%s: err = %v, want the request refused", target, err)
		}
	}
	sql := sqlOf(t, codechttp.Options{SQL: clickhouseSQL()}, request("GET", "/?param_x=a;b&query=DROP%20TABLE%20t", "", ""))
	if sql.Operation != inspect.OpDrop {
		t.Errorf("a ; in another parameter: %q %s", sql.Text, sql.Operation)
	}
}

// A text route reads the body alone, for an API that takes SQL as a plain
// body and no parameter.
func TestSQLTextReadsTheBody(t *testing.T) {
	o := &codechttp.SQLOptions{Dialect: lexer.Postgres, DialectName: "postgres", Text: []string{"/v1/statement"}, MatchResource: policy.MatchResource}
	sql := sqlOf(t, codechttp.Options{SQL: o}, request("POST", "/v1/statement?query=SELECT%201", "", "DROP TABLE t"))
	if sql.Text != "DROP TABLE t" || sql.Operation != inspect.OpDrop || sql.Metadata[codechttp.MetadataSQLField] != "body" {
		t.Errorf("SQL statement %q %s field %q", sql.Text, sql.Operation, sql.Metadata[codechttp.MetadataSQLField])
	}
	if stmts := mustDecode(t, codechttp.New(codechttp.Options{SQL: o}), request("POST", "/v1/statement", "", "")); len(stmts) != 1 {
		t.Errorf("an empty body produced %d statements", len(stmts))
	}
}

// The codec captures Content-Encoding to read a text route, and the
// statements leave without it unless the operator listed it.
func TestSQLParamContentEncodingStaysOffTheRecord(t *testing.T) {
	raw := request("POST", "/?query=INSERT%20INTO%20t%20FORMAT%20Native", "Content-Encoding: gzip\r\n", "\x1f\x8b")
	for _, tc := range []struct {
		headers []string
		want    string
	}{
		{nil, ""},
		{[]string{"Content-Encoding"}, "gzip"},
	} {
		stmts := mustDecode(t, codechttp.New(codechttp.Options{SQL: clickhouseSQL(), Headers: tc.headers}), raw)
		if got := stmts[0].HTTP.Headers["content-encoding"]; got != tc.want {
			t.Errorf("headers %v: recorded %q, want %q", tc.headers, got, tc.want)
		}
	}
}

// fields, params and text resolve by the longest pattern, as fields alone
// did. A tie naming two sources is refused, also for a request without a
// body when one of them reads the URL, and so is a pattern given twice.
func TestSQLSourcesResolveByTheLongestPattern(t *testing.T) {
	o := clickhouseSQL()
	o.Fields = map[string]string{"/api/sql": "statement"}
	sql := sqlOf(t, codechttp.Options{SQL: o}, request("POST", "/api/sql?query=SELECT%201", "", `{"statement":"DROP TABLE t"}`))
	if sql.Operation != inspect.OpDrop || sql.Metadata[codechttp.MetadataSQLField] != "statement" {
		t.Errorf("the longer fields pattern lost: %q %s", sql.Text, sql.Operation)
	}

	o.Fields = map[string]string{"/a/*": "statement"}
	o.Params = map[string]string{"/*/b": "query"}
	_, err := decodeClient(t, codechttp.New(codechttp.Options{SQL: o}), "GET /a/b?query=DROP%20TABLE%20t HTTP/1.1\r\nHost: h\r\n\r\n")
	if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), "both match") {
		t.Errorf("a tie naming two sources: err = %v", err)
	}

	o = clickhouseSQL()
	o.Text = []string{"/**"}
	_, err = decodeClient(t, codechttp.New(codechttp.Options{SQL: o}), request("POST", "/", "", "SELECT 1"))
	if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("a pattern in params and text: err = %v", err)
	}
}
