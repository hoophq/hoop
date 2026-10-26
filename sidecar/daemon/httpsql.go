package daemon

import (
	"fmt"
	"sort"
	"strings"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// HTTPSQLConfig makes an http lane read the SQL a JSON API carries in its
// request body, so operation and table rules judge the SQL instead of the
// request line. Databricks is the case it was written for (ENG-817): the
// Statement Execution API carries its SQL in `statement`, and the workspace
// SQL MCP server in the arguments of a tools/call.
//
// It is flat on purpose. The control plane renders the listener form from
// daemon/schema.json, which has a string map and a string list but no list
// of objects, so a list of per-route blocks could not be edited there.
type HTTPSQLConfig struct {
	// Dialect is the SQL the API runs. It is required: reading Spark SQL
	// with another dialect's comment and string rules hides writes.
	Dialect string `json:"dialect,omitempty" label:"Dialect" enum:"spark,postgres,mysql,mssql,googlesql,clickhouse,oracle" help:"The SQL dialect the API runs. spark is Databricks SQL."`

	// Fields maps a resource pattern, written as an http_resource rule
	// writes it, to the dotted JSON path of the SQL string in a request to
	// that resource.
	Fields map[string]string `json:"fields,omitempty" label:"Fields" placeholder:"/api/2.0/sql/statements" help:"A resource pattern, as an http_resource rule writes it, and the JSON path of the SQL in a request to it, such as statement or params.arguments.query."`

	// Tools names the JSON-RPC tools whose tools/call carries SQL, for an
	// MCP server. Any other JSON-RPC message carries none.
	Tools []string `json:"tools,omitempty" label:"JSON-RPC tools" help:"For an MCP server: the tools whose tools/call arguments carry SQL. Any other JSON-RPC message carries none."`

	// Refuse lists field=value pairs that send a request's results around
	// the lane. A SQL-bearing request carrying one is refused before it is
	// forwarded, whatever the lane's rules say.
	Refuse []string `json:"refuse,omitempty" label:"Refuse" placeholder:"disposition=EXTERNAL_LINKS" help:"field=value pairs whose results would not pass through this listener. A request carrying one is refused before it is forwarded."`

	// RefuseMessage is what a refused client is told.
	RefuseMessage string `json:"refuse_message,omitempty" label:"Refuse message" help:"What a refused client is told."`
}

// httpSQLDialects maps a config's dialect name to the lexer's.
var httpSQLDialects = map[string]lexer.Dialect{
	"spark":      lexer.Spark,
	"postgres":   lexer.Postgres,
	"mysql":      lexer.MySQL,
	"mssql":      lexer.MSSQL,
	"googlesql":  lexer.GoogleSQL,
	"clickhouse": lexer.ClickHouse,
	"oracle":     lexer.Oracle,
}

func (s *HTTPSQLConfig) validate(lane string) []string {
	if s == nil {
		return nil
	}
	var problems []string
	if _, ok := httpSQLDialects[s.Dialect]; !ok {
		names := make([]string, 0, len(httpSQLDialects))
		for n := range httpSQLDialects {
			names = append(names, n)
		}
		sort.Strings(names)
		problems = append(problems, fmt.Sprintf(
			"listener %q: http.sql.dialect %q is not one of %s; the dialect decides what a comment or a string is, so it is never guessed",
			lane, s.Dialect, strings.Join(names, ", ")))
	}
	if len(s.Fields) == 0 {
		problems = append(problems, fmt.Sprintf(
			"listener %q: http.sql.fields is empty; name a resource and the JSON path of the SQL in a request to it",
			lane))
	}
	patterns := make([]string, 0, len(s.Fields))
	for p := range s.Fields {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	for _, p := range patterns {
		if !strings.HasPrefix(p, "/") {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.fields key %q is not a resource pattern; it starts with /", lane, p))
		}
		if !validJSONPath(s.Fields[p]) {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.fields[%q] = %q is not a dotted JSON path such as statement or params.arguments.query",
				lane, p, s.Fields[p]))
		}
	}
	for _, t := range s.Tools {
		if strings.TrimSpace(t) == "" {
			problems = append(problems, fmt.Sprintf("listener %q: http.sql.tools has an empty name", lane))
		}
	}
	for _, r := range s.Refuse {
		if _, _, ok := parseRefuse(r); !ok {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.refuse entry %q is not field=value, such as disposition=EXTERNAL_LINKS", lane, r))
		}
	}
	return problems
}

// validJSONPath accepts dot-separated member names with no empty segment.
func validJSONPath(p string) bool {
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, ".") {
		if strings.TrimSpace(seg) == "" {
			return false
		}
	}
	return true
}

// parseRefuse splits one refuse entry at its first "=".
func parseRefuse(entry string) (field, value string, ok bool) {
	field, value, ok = strings.Cut(entry, "=")
	field, value = strings.TrimSpace(field), strings.TrimSpace(value)
	return field, value, ok && validJSONPath(field) && value != ""
}

// codecOptions is the codec's form of a validated block.
func (s *HTTPSQLConfig) codecOptions() *codechttp.SQLOptions {
	if s == nil {
		return nil
	}
	o := &codechttp.SQLOptions{
		Dialect:       httpSQLDialects[s.Dialect],
		DialectName:   s.Dialect,
		Fields:        s.Fields,
		Tools:         s.Tools,
		RefuseMessage: s.RefuseMessage,
		MatchResource: policy.MatchResource,
	}
	for _, r := range s.Refuse {
		if field, value, ok := parseRefuse(r); ok {
			o.Refuse = append(o.Refuse, codechttp.RefuseValue{Field: field, Value: value})
		}
	}
	return o
}
