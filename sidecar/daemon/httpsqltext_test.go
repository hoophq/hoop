package daemon

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// clickhouseSQLBlock is the http.sql block the README shows for ClickHouse's
// HTTP interface, as JSON, so the test also pins the key names.
const clickhouseSQLBlock = `{"dialect": "clickhouse", "params": {"/": "query"}}`

func clickhouseSQLConfig(t *testing.T) *HTTPSQLConfig {
	t.Helper()
	var s HTTPSQLConfig
	if err := json.Unmarshal([]byte(clickhouseSQLBlock), &s); err != nil {
		t.Fatalf("decode the documented block: %v", err)
	}
	return &s
}

func TestHTTPSQLParamsAndTextValidation(t *testing.T) {
	if err := sqlLaneConfig("http", clickhouseSQLConfig(t)).Validate(); err != nil {
		t.Fatalf("the documented block was refused: %v", err)
	}
	mixed := databricksSQLConfig(t)
	mixed.Params = map[string]string{"/clickhouse/**": "query"}
	mixed.Text = []string{"/v1/statement"}
	if err := sqlLaneConfig("http", mixed).Validate(); err != nil {
		t.Fatalf("fields, params and text on distinct routes were refused: %v", err)
	}

	for _, tc := range []struct {
		name      string
		edit      func(*HTTPSQLConfig)
		sensitive []string
		want      string
	}{
		{"param without a slash", func(s *HTTPSQLConfig) { s.Params = map[string]string{"x": "query"} }, nil, `http.sql.params key "x"`},
		{"text pattern that matches nothing", func(s *HTTPSQLConfig) { s.Text = []string{"/a/**/b"} }, nil, `http.sql.text key "/a/**/b" matches no request`},
		{"empty parameter", func(s *HTTPSQLConfig) { s.Params = map[string]string{"/": ""} }, nil, "is not a URL parameter name"},
		{"parameter with a separator", func(s *HTTPSQLConfig) { s.Params = map[string]string{"/": "query&x"} }, nil, "is not a URL parameter name"},
		{"redacted by default", func(s *HTTPSQLConfig) { s.Params = map[string]string{"/": "Token"} }, nil, "redacts"},
		{"redacted by the lane", func(s *HTTPSQLConfig) {}, []string{"QUERY"}, "redacts"},
		{"params and text", func(s *HTTPSQLConfig) { s.Text = []string{"/"} }, nil, `"/" is in both http.sql.params and http.sql.text`},
		{"fields and params", func(s *HTTPSQLConfig) { s.Fields = map[string]string{"/": "statement"} }, nil, `"/" is in both http.sql.fields and http.sql.params`},
		{"tools without fields", func(s *HTTPSQLConfig) { s.Tools = []string{"execute_sql"} }, nil, "need http.sql.fields"},
		{"refuse without fields", func(s *HTTPSQLConfig) {
			s.Refuse, s.RefuseMessage = []string{"disposition=EXTERNAL_LINKS"}, "ask for INLINE"
		}, nil, "need http.sql.fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := clickhouseSQLConfig(t)
			tc.edit(s)
			cfg := sqlLaneConfig("http", s)
			cfg.Listeners[0].HTTP.SensitiveQueryParams = tc.sensitive
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// params and text are newer than the http.sql block, so a build that
// decodes the block but not them is never served them, and a JSON-only
// block still is.
func TestHTTPSQLTextIsServedOnlyToABuildThatDecodesIt(t *testing.T) {
	old := slices.DeleteFunc(SidecarCapabilities(), func(c string) bool { return c == "http_sql_text" })
	for name, s := range map[string]*HTTPSQLConfig{
		"params": clickhouseSQLConfig(t),
		"text":   {Dialect: "postgres", Text: []string{"/v1/statement"}},
	} {
		cfg := *sqlLaneConfig("http", s)
		if err := CheckServable(cfg, Handshake{Capabilities: SidecarCapabilities()}); err != nil {
			t.Errorf("%s: a current build was refused: %v", name, err)
		}
		err := CheckServable(cfg, Handshake{Capabilities: old})
		if err == nil || !strings.Contains(err.Error(), "http.sql."+name) {
			t.Errorf("%s: a build without http_sql_text was served it: %v", name, err)
		}
	}
	if err := CheckServable(*sqlLaneConfig("http", databricksSQLConfig(t)), Handshake{Capabilities: old}); err != nil {
		t.Errorf("a fields-only block was refused to a build that reads it: %v", err)
	}
}

// A lane built from the ClickHouse block reads the SQL in the query
// parameter, and keeps Content-Encoding, which it captures to read the
// body, off the record.
func TestHTTPSQLLaneReadsTheQueryParameter(t *testing.T) {
	f := laneCodecFactory(ListenerConfig{Protocol: "http", HTTP: &HTTPCodecConfig{SQL: clickhouseSQLConfig(t)}}, nil)
	raw := "POST /?query=DROP%20TABLE%20appdb.customers HTTP/1.1\r\nHost: h\r\nContent-Encoding: identity\r\nContent-Length: 0\r\n\r\n"
	stmts, _, err := f().Decode(inspect.FromClient, []byte(raw))
	if err != nil || len(stmts) != 2 {
		t.Fatalf("Decode: %d statements, %v", len(stmts), err)
	}
	if stmts[1].Operation != inspect.OpDrop || stmts[1].Metadata["http.sql_dialect"] != "clickhouse" {
		t.Errorf("SQL statement = %+v", stmts[1])
	}
	if _, ok := stmts[0].HTTP.Headers["content-encoding"]; ok {
		t.Errorf("the request recorded Content-Encoding: %v", stmts[0].HTTP.Headers)
	}
}
