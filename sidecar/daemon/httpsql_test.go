package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// databricksSQLBlock is the http.sql block the Databricks docs page shows,
// as JSON, so the test also pins the key names an operator writes.
const databricksSQLBlock = `{
	"dialect": "spark",
	"fields": {
		"/api/2.0/sql/statements": "statement",
		"/ai-gateway/mcp-services/**": "params.arguments.query",
		"/api/2.0/mcp/sql": "params.arguments.query"
	},
	"tools": ["execute_sql"],
	"refuse": ["disposition=EXTERNAL_LINKS", "disposition=INLINE_OR_EXTERNAL_LINKS"],
	"refuse_message": "ask for disposition INLINE: external links skip this listener"
}`

func databricksSQLConfig(t *testing.T) *HTTPSQLConfig {
	t.Helper()
	var s HTTPSQLConfig
	if err := json.Unmarshal([]byte(databricksSQLBlock), &s); err != nil {
		t.Fatalf("decode the documented block: %v", err)
	}
	return &s
}

func sqlLaneConfig(protocol string, s *HTTPSQLConfig) *Config {
	return &Config{Listeners: []ListenerConfig{{
		Name: "dbx", Protocol: protocol, Listen: ":1", Upstream: "h:1",
		HTTP: &HTTPCodecConfig{SQL: s},
	}}}
}

// The documented block validates, and every way to get it wrong is refused
// with the key that is wrong in the message.
func TestHTTPSQLConfigValidation(t *testing.T) {
	if err := sqlLaneConfig("http", databricksSQLConfig(t)).Validate(); err != nil {
		t.Fatalf("the documented block was refused: %v", err)
	}
	for _, tc := range []struct {
		name  string
		edit  func(*HTTPSQLConfig)
		proto string
		want  string
	}{
		{"no dialect", func(s *HTTPSQLConfig) { s.Dialect = "" }, "http", "http.sql.dialect"},
		{"unknown dialect", func(s *HTTPSQLConfig) { s.Dialect = "hive" }, "http", `http.sql.dialect "hive"`},
		{"no fields", func(s *HTTPSQLConfig) { s.Fields = nil }, "http", "http.sql.fields is empty"},
		{"pattern without a slash", func(s *HTTPSQLConfig) { s.Fields = map[string]string{"api/x": "statement"} }, "http", `key "api/x"`},
		{"empty path segment", func(s *HTTPSQLConfig) {
			s.Fields = map[string]string{"/x": "params..query"}
		}, "http", `"params..query"`},
		{"empty tool", func(s *HTTPSQLConfig) { s.Tools = []string{" "} }, "http", "http.sql.tools"},
		{"refuse without a value", func(s *HTTPSQLConfig) { s.Refuse = []string{"disposition"} }, "http", `refuse entry "disposition"`},
		{"block on another protocol", func(*HTTPSQLConfig) {}, "postgres", `"http" block is only valid on an http listener`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := databricksSQLConfig(t)
			tc.edit(s)
			err := sqlLaneConfig(tc.proto, s).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %s", err, tc.want)
			}
		})
	}
}

// An approval on SQL read from a body would never be spent, so a lane whose
// analyzer holds for approval cannot read SQL bodies yet. A blocking
// analyzer is fine.
func TestHTTPSQLRefusesAnApprovalHoldingAnalyzer(t *testing.T) {
	cfg := sqlLaneConfig("http", databricksSQLConfig(t))
	cfg.Listeners[0].Analyzer = &LaneAnalyzerConfig{HighRisk: "require_review"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "never be spent") {
		t.Fatalf("an approval-holding analyzer on an http.sql lane was accepted: %v", err)
	}
	cfg.Listeners[0].Analyzer = &LaneAnalyzerConfig{HighRisk: "block"}
	if err := cfg.Validate(); err != nil && strings.Contains(err.Error(), "never be spent") {
		t.Fatalf("a blocking analyzer was refused: %v", err)
	}
}

// A lane built from the config reads the SQL out of the body under the
// configured dialect, and refuses a request asking for external links.
func TestHTTPSQLLaneReadsTheBody(t *testing.T) {
	f := laneCodecFactory(ListenerConfig{Protocol: "http", HTTP: &HTTPCodecConfig{SQL: databricksSQLConfig(t)}}, nil)
	post := func(body string) string {
		return fmt.Sprintf("POST /api/2.0/sql/statements HTTP/1.1\r\nHost: h\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	}

	stmts, _, err := f().Decode(inspect.FromClient, []byte(post(`{"statement":"DROP TABLE customers"}`)))
	if err != nil || len(stmts) != 2 {
		t.Fatalf("Decode: %d statements, %v", len(stmts), err)
	}
	if stmts[1].Operation != inspect.OpDrop || stmts[1].Metadata["http.sql_dialect"] != "spark" {
		t.Errorf("SQL statement = %+v", stmts[1])
	}

	_, _, err = f().Decode(inspect.FromClient, []byte(post(`{"statement":"SELECT 1","disposition":"EXTERNAL_LINKS"}`)))
	if !errors.Is(err, inspect.ErrStreamUnsafe) || !strings.Contains(err.Error(), "ask for disposition INLINE") {
		t.Errorf("EXTERNAL_LINKS: err = %v", err)
	}
}
