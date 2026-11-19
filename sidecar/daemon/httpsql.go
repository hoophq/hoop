package daemon

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/jsonsql"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// HTTPSQLConfig makes an http lane read the SQL an API carries in its
// request, so operation and table rules judge the SQL instead of the
// request line. Databricks is the case it was written for (ENG-817): the
// Statement Execution API carries its SQL in `statement`, and the workspace
// SQL MCP server in the arguments of a tools/call. ClickHouse's HTTP
// interface carries it in the query parameter and the body (ENG-1036).
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
	// that resource. When two patterns match a request, the longer wins;
	// two of the same length that name different paths refuse it.
	Fields map[string]string `json:"fields,omitempty" label:"Fields" placeholder:"/api/2.0/sql/statements" help:"A resource pattern, as an http_resource rule writes it, and the JSON path of the SQL in a request to it, such as statement or params.arguments.query."`

	// Params maps a resource pattern to the URL parameter holding the SQL.
	// A body is read as the rest of the SQL, after a line feed: that is
	// how ClickHouse joins its query parameter and its POST body.
	Params map[string]string `json:"params,omitempty" cap:"http_sql_text" label:"URL parameters" placeholder:"/" help:"A resource pattern and the URL parameter holding the SQL of a request to it, such as query for ClickHouse. A request body is read as the rest of the SQL."`

	// Text lists resource patterns whose plain-text body is the SQL.
	Text []string `json:"text,omitempty" cap:"http_sql_text" label:"Plain-text bodies" placeholder:"/v1/statement" help:"Resource patterns whose request body is the SQL as plain text."`

	// Tools names the JSON-RPC tools whose tools/call carries SQL, for an
	// MCP server. A path under params.arguments is read only from a call to
	// one of them, so such a path requires it.
	Tools []string `json:"tools,omitempty" label:"JSON-RPC tools" help:"For an MCP server: the tools whose tools/call arguments carry SQL. Required when a path is under params.arguments; any other JSON-RPC message there carries none."`

	// Refuse lists field=value pairs that send a request's results around
	// the lane. A SQL-bearing request carrying one is refused before it is
	// forwarded, whatever the lane's rules say.
	Refuse []string `json:"refuse,omitempty" label:"Refuse" placeholder:"disposition=EXTERNAL_LINKS" help:"field=value pairs whose results would not pass through this listener. A request carrying one is refused before it is forwarded."`

	// RefuseMessage is what a refused client is told.
	RefuseMessage string `json:"refuse_message,omitempty" label:"Refuse message" help:"What a refused client is told: the value to ask for instead. Required with Refuse."`
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

// validate checks the block. sensitive is the lane's
// sensitive_query_params: a parameter the codec redacts cannot carry SQL.
func (s *HTTPSQLConfig) validate(lane string, sensitive []string) []string {
	if s == nil {
		return nil
	}
	var problems []string
	if p := sqlDialectProblem("http.sql.dialect", s.Dialect); p != "" {
		problems = append(problems, fmt.Sprintf("listener %q: %s", lane, p))
	}
	if len(s.Fields) == 0 && len(s.Params) == 0 && len(s.Text) == 0 {
		problems = append(problems, fmt.Sprintf(
			"listener %q: http.sql.fields, http.sql.params and http.sql.text are empty; name a resource and where the SQL of a request to it is",
			lane))
	}
	// A pattern in two of them would leave the source of its SQL to the
	// tie-break; the codec refuses such a request, so refuse the config.
	owner := map[string]string{}
	pattern := func(key, p string) {
		if !strings.HasPrefix(p, "/") {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.%s key %q is not a resource pattern; it starts with /", lane, key, p))
		} else if err := policy.ValidResourcePattern(p); err != nil {
			// It would match no request, and its SQL would reach the
			// upstream unread.
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.%s key %q matches no request as written: %v", lane, key, p, err))
		}
		if prev, ok := owner[p]; ok && prev != key {
			problems = append(problems, fmt.Sprintf(
				"listener %q: %q is in both http.sql.%s and http.sql.%s; a route has one source for its SQL", lane, p, prev, key))
		}
		owner[p] = key
	}
	for _, p := range slices.Sorted(maps.Keys(s.Fields)) {
		pattern("fields", p)
		if !jsonsql.ValidPath(s.Fields[p]) {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.fields[%q] = %q is not a dotted JSON path such as statement or params.arguments.query",
				lane, p, s.Fields[p]))
		}
		// Without tools no call would be read, and the lane would pass
		// every tools/call it was configured to judge.
		if jsonsql.ToolArgument(s.Fields[p]) && len(s.Tools) == 0 {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.fields[%q] = %q is a tools/call argument and http.sql.tools is empty; name the tools whose calls run SQL",
				lane, p, s.Fields[p]))
		}
	}
	for _, p := range slices.Sorted(maps.Keys(s.Params)) {
		pattern("params", p)
		name := s.Params[p]
		if name == "" || name != strings.TrimSpace(name) || strings.ContainsAny(name, "&=?# ") {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.params[%q] = %q is not a URL parameter name such as query", lane, p, name))
		} else if codechttp.Redacts(name, sensitive) {
			problems = append(problems, fmt.Sprintf(
				"listener %q: http.sql.params[%q] = %q is a parameter this listener redacts, so its SQL cannot be read", lane, p, name))
		}
	}
	for _, p := range s.Text {
		pattern("text", p)
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
	// The block is generic, so only the operator knows what a refused
	// client should ask for instead (disposition INLINE, on Databricks).
	if len(s.Refuse) > 0 && strings.TrimSpace(s.RefuseMessage) == "" {
		problems = append(problems, fmt.Sprintf(
			"listener %q: http.sql.refuse_message is empty; tell a refused client what to ask for instead", lane))
	}
	// tools and refuse read JSON bodies, which only fields routes parse.
	if len(s.Fields) == 0 && (len(s.Tools) > 0 || len(s.Refuse) > 0) {
		problems = append(problems, fmt.Sprintf(
			"listener %q: http.sql.tools and http.sql.refuse read JSON bodies and need http.sql.fields; they would apply to no request", lane))
	}
	return problems
}

// sqlDialectProblem refuses a dialect name httpSQLDialects lacks; field is
// its config key.
func sqlDialectProblem(field, name string) string {
	if _, ok := httpSQLDialects[name]; ok {
		return ""
	}
	names := make([]string, 0, len(httpSQLDialects))
	for n := range httpSQLDialects {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Sprintf("%s %q is not one of %s; the dialect decides what a comment or a string is, so it is never guessed",
		field, name, strings.Join(names, ", "))
}

// parseRefuse splits one refuse entry at its first "=".
func parseRefuse(entry string) (field, value string, ok bool) {
	field, value, ok = strings.Cut(entry, "=")
	field, value = strings.TrimSpace(field), strings.TrimSpace(value)
	return field, value, ok && jsonsql.ValidPath(field) && value != ""
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
		Params:        s.Params,
		Text:          s.Text,
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
