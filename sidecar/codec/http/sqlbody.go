package http

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
)

// A JSON API that runs SQL carries the SQL in its request body: Databricks'
// Statement Execution API in `statement`, its SQL MCP server in the
// arguments of a JSON-RPC tools/call. An http lane stops at the request line,
// so its statement is `POST /api/2.0/sql/statements`, and an operation or
// table rule matches nothing there: a DROP passes a lane configured to
// refuse it (ENG-777). This file reads through the body to the SQL, the way
// daemon/spanner.go reads through a gRPC message.
//
// Each SQL string becomes its own statement, placed right after the request
// it came from. It keeps Protocol HTTP, so a denial is answered with this
// lane's 403, and it carries no HTTP detail and no credential handle: the
// gate rotates the session and resolves the caller on the HTTP request
// statement only (isHTTPRequest), so the SQL statement is judged under the
// caller of the request that carried it, and an http_resource rule never
// matches it. Operation and table rules read the SQL statement; method and
// resource rules read the request.

// Metadata keys on an extracted SQL statement and on the request that
// carried it. analyzer/content.go reads MetadataSQLIndex as a literal, as it
// does spanner.sql_index, to render the statement as SQL.
const (
	// MetadataSQLIndex is the 1-based ordinal of one SQL statement within
	// its request body; a JSON-RPC batch can carry several.
	MetadataSQLIndex = "http.sql_index"
	// MetadataSQLField is the JSON path the SQL was read from.
	MetadataSQLField = "http.sql_field"
	// MetadataSQLDialect is the lexical dialect that read it.
	MetadataSQLDialect = "http.sql_dialect"
	// MetadataSQLStatements, on the request, counts the SQL statements its
	// body produced, so the trail ties the request to them.
	MetadataSQLStatements = "http.sql_statements"
)

// SQLOptions makes an http lane read SQL out of JSON request bodies.
type SQLOptions struct {
	// Dialect classifies the SQL; DialectName is what the trail records.
	Dialect     lexer.Dialect
	DialectName string

	// Fields maps a resource pattern to the dotted JSON path of the string
	// holding the SQL in a request to it: "statement", or
	// "params.arguments.query". Patterns are matched with MatchResource
	// against the request's normalized resource, as an http_resource rule
	// matches them.
	Fields map[string]string

	// Tools names the JSON-RPC tools whose tools/call carries SQL. A body
	// with a "jsonrpc" member is JSON-RPC: it carries SQL only as a
	// tools/call naming one of these, and any other message (initialize,
	// tools/list, another tool) carries none.
	Tools []string

	// Refuse lists body values that send a request's results around the
	// lane: Databricks' disposition EXTERNAL_LINKS answers with links to
	// cloud storage, so the rows never cross the lane that masks them. A
	// SQL-bearing request carrying one is refused before it is forwarded,
	// with RefuseMessage, whatever the lane's rules say.
	Refuse        []RefuseValue
	RefuseMessage string

	// MatchResource matches a resource against a pattern. It is the
	// http_resource matcher (policy.MatchResource), injected so this seam
	// does not import policy and the two cannot drift.
	MatchResource func(pattern, resource string) bool
}

// RefuseValue is one Refuse entry: the JSON path and the value at it,
// compared case-insensitively.
type RefuseValue struct {
	Field string
	Value string
}

// sqlReader is the per-codec form of SQLOptions: the patterns in a fixed
// order, so the field a request resolves to does not depend on map order.
type sqlReader struct {
	opts     SQLOptions
	patterns []string
}

func newSQLReader(o *SQLOptions) *sqlReader {
	if o == nil {
		return nil
	}
	r := &sqlReader{opts: *o}
	for p := range o.Fields {
		r.patterns = append(r.patterns, p)
	}
	// The longest pattern first, so a specific route wins over a "/**"
	// that also covers it; then lexically, for a stable tie-break.
	slices.SortFunc(r.patterns, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return r
}

// Both refusals wrap ErrStreamUnsafe, which the gate denies regardless of
// policy and answers with the error text: no rule configured them, and none
// can switch them off. The text leads with what the client should do; the
// sentinel's own text follows it.

// refuseResults refuses a request whose results would not cross the lane.
func (r *sqlReader) refuseResults(reason string) error {
	msg := r.opts.RefuseMessage
	if msg == "" {
		msg = "this listener refuses requests whose results would not pass through it"
	}
	return fmt.Errorf("%s (%s): %w", msg, reason, inspect.ErrStreamUnsafe)
}

// unparseable answers a body that cannot be read at all: cut at
// max_body_bytes, or not JSON. Its SQL is unknown, and so is whether it asks
// for results that skip the lane. A lane that refuses those refuses it, or
// padding a body past the limit would get it past Refuse; any other lane
// gets an OpUnknown statement for its rules to judge.
func (r *sqlReader) unparseable(body, field, reason string) ([]inspect.Statement, error) {
	if len(r.opts.Refuse) > 0 {
		return nil, fmt.Errorf("this listener could not read this request's body to check it: %s: %w",
			reason, inspect.ErrStreamUnsafe)
	}
	return []inspect.Statement{r.unreadable(body, field, 1, reason)}, nil
}

// extract returns the SQL statements a client request carries, to be placed
// after it. A refusal comes back as an error and no statements.
func (r *sqlReader) extract(req *inspect.Statement) ([]inspect.Statement, error) {
	d := req.HTTP
	resource := d.Resource
	if resource == "" {
		resource = d.Path
	}
	field, ok := r.fieldFor(resource)
	if !ok {
		return nil, nil
	}
	if d.BodyTruncated {
		return r.unparseable(d.Body, field, "the body is larger than http.max_body_bytes; raise it")
	}
	dec := json.NewDecoder(strings.NewReader(d.Body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return r.unparseable(d.Body, field, "the body is not JSON")
	}

	messages := []any{doc}
	if batch, isBatch := doc.([]any); isBatch {
		messages = batch
	}
	var out []inspect.Statement
	for _, m := range messages {
		obj, isObject := m.(map[string]any)
		if !isObject {
			out = append(out, r.unreadable(d.Body, field, len(out)+1, "the body holds a JSON value that is not an object"))
			continue
		}
		if _, rpc := obj["jsonrpc"]; rpc && !r.toolCall(obj) {
			continue // a JSON-RPC message that runs no SQL tool
		}
		for _, v := range r.opts.Refuse {
			if s, found := lookup(obj, v.Field).(string); found && strings.EqualFold(s, v.Value) {
				return nil, r.refuseResults(fmt.Sprintf("%s is %s", v.Field, s))
			}
		}
		sql, isString := lookup(obj, field).(string)
		if !isString || strings.TrimSpace(sql) == "" {
			out = append(out, r.unreadable(d.Body, field, len(out)+1, fmt.Sprintf("no SQL string at %q", field)))
			continue
		}
		out = append(out, r.statement(sql, field, len(out)+1))
	}
	return out, nil
}

// fieldFor returns the JSON path of the SQL in a request to resource.
func (r *sqlReader) fieldFor(resource string) (string, bool) {
	for _, p := range r.patterns {
		if r.opts.MatchResource(p, resource) {
			return r.opts.Fields[p], true
		}
	}
	return "", false
}

// toolCall reports whether a JSON-RPC message calls one of the SQL tools.
func (r *sqlReader) toolCall(msg map[string]any) bool {
	if method, _ := msg["method"].(string); method != "tools/call" {
		return false
	}
	name, _ := lookup(msg, "params.name").(string)
	return slices.Contains(r.opts.Tools, name)
}

// statement classifies one SQL string under the lane's dialect. It carries
// fresh metadata, never the request's: that holds the credential handle.
func (r *sqlReader) statement(sql, field string, index int) inspect.Statement {
	a := inspect.AnalyzeSQLIn(sql, r.opts.Dialect)
	st := inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      sql,
		Operation: a.Operation,
		Effects:   a.Effects,
		Relations: a.Relations,
		Tables:    a.Tables,
		Metadata:  r.metadata(field, index),
	}
	if !a.Complete {
		// Same contract as every SQL codec: SQL the lexer cannot fully read
		// is OpUnknown with the reason on the record, and the writes it did
		// recognize stay in Effects, which an operation rule still matches
		// (a Spark script that drops a table is a drop).
		st.Metadata[inspect.MetadataSQLIncomplete] = a.Reason
	}
	return st
}

// unreadable is the statement for SQL the lane was told to read and could
// not. It is OpUnknown with the reason in MetadataSQLIncomplete, as
// daemon/spanner.go reports a gRPC message it cannot read, so a rule naming
// `unknown` refuses it and forwarding it as a plain POST is the operator's
// explicit choice. Its text is the body, the closest thing to the SQL.
func (r *sqlReader) unreadable(body, field string, index int, reason string) inspect.Statement {
	md := r.metadata(field, index)
	md[inspect.MetadataSQLIncomplete] = reason
	return inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      body,
		Operation: inspect.OpUnknown,
		Metadata:  md,
	}
}

func (r *sqlReader) metadata(field string, index int) map[string]string {
	return map[string]string{
		MetadataSQLIndex:   strconv.Itoa(index),
		MetadataSQLField:   field,
		MetadataSQLDialect: r.opts.DialectName,
	}
}

// lookup follows a dotted path through JSON objects. An absent member, or a
// step into something that is not an object, yields nil.
func lookup(v any, path string) any {
	for _, key := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		if v, ok = obj[key]; !ok {
			return nil
		}
	}
	return v
}
