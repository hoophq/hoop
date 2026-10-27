package http

import (
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/jsonsql"
	"github.com/hoophq/hoop/sidecar/lexer"
)

// A JSON API that runs SQL carries the SQL in its request body: Databricks'
// Statement Execution API in `statement`, its SQL MCP server in the
// arguments of a JSON-RPC tools/call. An http lane stops at the request line,
// so its statement is `POST /api/2.0/sql/statements`, and an operation or
// table rule matches nothing there: a DROP passes a lane configured to
// refuse it (ENG-777).
//
// This file is the HTTP seam only: which requests carry SQL (their route),
// whose bodies are kept, and how the SQL becomes statements of this lane.
// Reading the SQL out of the JSON is the jsonsql package, which knows no
// protocol, so another protocol that carries JSON can reuse it.
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
	// MetadataSQLResource is the cleaned resource of the request that
	// carried the SQL. The SQL statement has no HTTP detail, so an analyzer
	// resource trigger reads this key instead (analyzer/evaluator.go).
	MetadataSQLResource = "http.sql_resource"
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

	// Tools, Refuse and RefuseMessage configure the JSON reading; see
	// jsonsql.Options.
	Tools         []string
	Refuse        []RefuseValue
	RefuseMessage string

	// MatchResource matches a resource against a pattern. It is the
	// http_resource matcher (policy.MatchResource), injected so this seam
	// does not import policy and the two cannot drift.
	MatchResource func(pattern, resource string) bool
}

// RefuseValue is one Refuse entry; see jsonsql.RefuseValue.
type RefuseValue = jsonsql.RefuseValue

// sqlReader is the per-codec form of SQLOptions: the patterns in a fixed
// order, so the field a request resolves to does not depend on map order.
type sqlReader struct {
	opts     SQLOptions
	patterns []string
	json     *jsonsql.Reader
}

func newSQLReader(o *SQLOptions) *sqlReader {
	if o == nil {
		return nil
	}
	r := &sqlReader{opts: *o, json: jsonsql.New(jsonsql.Options{
		Dialect:       o.Dialect,
		Tools:         o.Tools,
		Refuse:        o.Refuse,
		RefuseMessage: o.RefuseMessage,
	})}
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

// route returns the JSON path of the SQL in a request to one of the lane's
// routes. The resource is cleaned first: Databricks reads a backslash as a
// slash and resolves `.` and `..` segments and doubled or trailing slashes
// before it routes, so `/api/2.0/./sql/statements` and
// `/api/2.0\sql\statements` run a statement like the canonical path and must
// be read like it, or an operation rule never sees their SQL.
func (r *sqlReader) route(d *inspect.HTTPDetail) (sqlRoute, bool) {
	resource := d.Resource
	if resource == "" {
		resource = d.Path
	}
	resource = path.Clean("/" + strings.ReplaceAll(resource, `\`, "/"))
	field, ok := r.fieldFor(resource)
	return sqlRoute{resource: resource, field: field}, ok
}

// sqlRoute is where a request's SQL is read: the cleaned resource it went
// to, and the JSON path of the SQL in its body.
type sqlRoute struct {
	resource string
	field    string
}

// extract returns the SQL statements a request to rt carries, to be placed
// after it. A refusal comes back as an error and no statements.
func (r *sqlReader) extract(d *inspect.HTTPDetail, rt sqlRoute) ([]inspect.Statement, error) {
	if d.Body == "" && !d.BodyTruncated {
		// Nothing to run: an MCP client opens its event stream with a GET
		// on the same route, and the server answers it without SQL.
		return nil, nil
	}
	var (
		found []jsonsql.SQL
		err   error
	)
	if d.BodyTruncated {
		found, err = r.json.Unparseable(d.Body, "the body is larger than http.max_body_bytes; raise it")
	} else {
		found, err = r.json.Read(d.Body, rt.field)
	}
	if err != nil {
		return nil, err
	}
	out := make([]inspect.Statement, len(found))
	for i, s := range found {
		out[i] = r.statement(s, rt, i+1)
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

// statement is one SQL string as a statement of this lane. It carries fresh
// metadata, never the request's: that holds the credential handle.
//
// SQL the lexer cannot fully read is OpUnknown with the reason on the
// record, the same contract as every SQL codec, and the writes it did
// recognize stay in Effects, which an operation rule still matches (a Spark
// script that drops a table is a drop). SQL the lane could not read at all
// is OpUnknown with jsonsql's reason, as daemon/spanner.go reports a gRPC
// message it cannot read, so a rule naming `unknown` refuses it.
func (r *sqlReader) statement(s jsonsql.SQL, rt sqlRoute, index int) inspect.Statement {
	st := inspect.Statement{
		Protocol:  inspect.HTTP,
		Direction: inspect.FromClient,
		Text:      s.Text,
		Operation: inspect.OpUnknown,
		Metadata: map[string]string{
			MetadataSQLIndex:    strconv.Itoa(index),
			MetadataSQLField:    rt.field,
			MetadataSQLDialect:  r.opts.DialectName,
			MetadataSQLResource: rt.resource,
		},
	}
	if s.Unreadable != "" {
		st.Metadata[inspect.MetadataSQLIncomplete] = s.Unreadable
		return st
	}
	a := s.Analysis
	st.Operation, st.Effects, st.Relations, st.Tables = a.Operation, a.Effects, a.Relations, a.Tables
	if !a.Complete {
		st.Metadata[inspect.MetadataSQLIncomplete] = a.Reason
	}
	return st
}
