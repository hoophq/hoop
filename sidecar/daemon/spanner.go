package daemon

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
)

// A spanner lane is a grpc-transport lane (ADR-0013 mechanics, unchanged)
// whose statements read THROUGH the RPC to the GoogleSQL inside it. Cloud
// Spanner's data plane is one gRPC API where every query and mutation
// travels as a string field of a request message; a lane that stops at the
// method name can fence Spanner the service but cannot tell SELECT from
// DELETE. This file is the reading-through: which methods carry SQL, where
// in the message it sits, and how the extracted text becomes a statement
// the same operation and table rules that police postgres can match.

// spannerSQLIndexMetadata is the 1-based ordinal of one extracted SQL
// statement within its RPC message. A batch method carries several SQL
// strings in one message, and each becomes its own statement; without the
// index an operator reading the trail cannot tell which member of the
// batch a denial names. analyzer/content.go reads the same key (as a
// literal — it cannot import this package) to pick SQL-style rendering.
const spannerSQLIndexMetadata = "spanner.sql_index"

// spannerDialectMetadata records which lexical dialect read an extracted
// statement, so the trail says why `"Songs"` was a table on one database
// and a string on another.
const spannerDialectMetadata = "spanner.dialect"

// spannerDatabaseMetadata records the database resource name the SQL ran
// against, read from the request's session or database field.
const spannerDatabaseMetadata = "spanner.database"

// The two services whose requests carry SQL. The data-plane service runs
// queries and DML; the database admin service runs DDL. Everything else on
// a Spanner endpoint (sessions, instance admin, operations) carries no SQL
// and stays on the generic per-message statement, where method fencing and
// payload rules still apply.
const (
	spannerDataService  = "google.spanner.v1.Spanner"
	spannerAdminService = "google.spanner.admin.database.v1.DatabaseAdmin"
)

// The dialect names a config spells. They are the sidecar's, not the API's
// enum names, because an operator writes the config and reads the trail.
const (
	SpannerDialectGoogleSQL  = "googlesql"
	SpannerDialectPostgreSQL = "postgresql"
	// SpannerDialectPerDatabase is the fail-closed setting: every database
	// the lane sees must be listed, and SQL against one that is not reads
	// as OpUnknown so a rule naming `unknown` refuses it.
	SpannerDialectPerDatabase = "per_database"
)

// SpannerConfig tells a spanner lane which SQL dialect its databases speak.
//
// Cloud Spanner fixes the dialect when a database is created — GoogleSQL or
// the PostgreSQL interface — and one instance holds databases of both. The
// data plane never says which: ExecuteSql carries a session and a string.
// Reading PostgreSQL SQL with the GoogleSQL lexer turns `"Songs"` from a
// table into a string literal, and a table rule fencing songs never fires.
// So the dialect is configuration, keyed on the database resource name
// every SQL-bearing request already carries, and never inferred from the
// text: a client that could pick the grammar could pick the one that hides
// its statement.
//
// The one request that does declare a dialect is CreateDatabase
// (database_dialect), and the lane believes it for that request's own
// statements: they create the database it names.
type SpannerConfig struct {
	// Dialect is the lane default: googlesql (the API default, and what an
	// absent block means), postgresql, or per_database. Under per_database
	// a database absent from Databases is not guessed at.
	Dialect string `json:"dialect,omitempty"`

	// Databases maps a database resource name
	// (projects/P/instances/I/databases/D) to googlesql or postgresql,
	// overriding Dialect for that database.
	Databases map[string]string `json:"databases,omitempty"`
}

func (s *SpannerConfig) validate(lane string) []string {
	if s == nil {
		return nil
	}
	var problems []string
	switch s.Dialect {
	case "", SpannerDialectGoogleSQL, SpannerDialectPostgreSQL:
	case SpannerDialectPerDatabase:
		if len(s.Databases) == 0 {
			problems = append(problems, fmt.Sprintf(
				"%s: spanner.dialect is per_database but spanner.databases is empty; "+
					"every statement would read as unknown", lane))
		}
	default:
		problems = append(problems, fmt.Sprintf(
			"%s: unknown spanner.dialect %q (%s, %s or %s)", lane, s.Dialect,
			SpannerDialectGoogleSQL, SpannerDialectPostgreSQL, SpannerDialectPerDatabase))
	}
	names := make([]string, 0, len(s.Databases))
	for name := range s.Databases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !isSpannerDatabaseName(name) {
			problems = append(problems, fmt.Sprintf(
				"%s: spanner.databases key %q is not a database resource name "+
					"(projects/P/instances/I/databases/D)", lane, name))
		}
		switch s.Databases[name] {
		case SpannerDialectGoogleSQL, SpannerDialectPostgreSQL:
		default:
			problems = append(problems, fmt.Sprintf(
				"%s: spanner.databases[%q] = %q; want %s or %s", lane, name,
				s.Databases[name], SpannerDialectGoogleSQL, SpannerDialectPostgreSQL))
		}
	}
	return problems
}

// isSpannerDatabaseName accepts projects/P/instances/I/databases/D with
// non-empty segments and nothing after: a session path pasted by mistake
// would silently never match a request's database.
func isSpannerDatabaseName(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[2] != "instances" || parts[4] != "databases" {
		return false
	}
	return parts[1] != "" && parts[3] != "" && parts[5] != ""
}

// dialectFor resolves the lexical dialect for SQL against one database.
// declared is the request's own claim (CreateDatabase) and wins when set.
// ok is false only under per_database for a database not listed. A nil
// receiver is the absent block: GoogleSQL, the API default and the lane's
// behavior before this block existed.
func (s *SpannerConfig) dialectFor(database, declared string) (name string, d lexer.Dialect, ok bool) {
	switch declared {
	case SpannerDialectGoogleSQL:
		return declared, lexer.GoogleSQL, true
	case SpannerDialectPostgreSQL:
		return declared, lexer.Postgres, true
	}
	name = SpannerDialectGoogleSQL
	if s != nil {
		if mapped, listed := s.Databases[database]; listed {
			name = mapped
		} else if s.Dialect == SpannerDialectPerDatabase {
			return "", 0, false
		} else if s.Dialect != "" {
			name = s.Dialect
		}
	}
	if name == SpannerDialectPostgreSQL {
		return name, lexer.Postgres, true
	}
	return name, lexer.GoogleSQL, true
}

// spannerRequest is what a SQL-bearing request message yields: the SQL
// strings in message order, the database they run against, and the
// dialect the request itself declared, which only CreateDatabase does.
type spannerRequest struct {
	sqls     []string
	database string
	declared string
}

// spannerSQLStatements returns the SQL strings inside one rendered request
// message, in the order the message carries them, with the database they
// target. sqlBearing reports that the method is KNOWN to carry SQL,
// whatever the parse produced.
//
// rendered is the protojson form libhoop's renderPayload produced. That
// rendering uses proto field names (UseProtoNames), but the parsers below
// accept the lowerCamel JSON spelling too: protojson accepts both on
// input, so a future rendering change must not silently turn every lane
// into the generic path.
//
// The (no SQL, true) return is the load-bearing one. A SQL-bearing method
// whose rendering does not parse — a capture truncated at the payload
// budget is the reachable case, and padding a request past the budget is
// CHEAP for a client — must not fall back to the generic OpCall
// statement: the lane's operation rules match nothing there, so the very
// statement most worth reading would be the easiest to smuggle. The
// caller turns it into an OpUnknown statement instead, the same
// fail-closed verdict an unlexable SQL string earns, and a rule naming
// `unknown` refuses it.
func spannerSQLStatements(service, method, rendered string) (req spannerRequest, sqlBearing bool) {
	switch service {
	case spannerDataService:
		switch method {
		case "ExecuteSql", "ExecuteStreamingSql", "PartitionQuery":
			// All three carry the query as a top-level `sql` string and
			// the session it runs in. PartitionQuery is included because
			// partitioning a query PLANS it against real tables: the SQL
			// names what will be read, and a rule fencing a table wants
			// to see it here, not only at the ExecuteStreamingSql that
			// follows.
			return spannerTopLevelSQL(rendered), true
		case "ExecuteBatchDml":
			return spannerBatchDMLSQL(rendered), true
		}
	case spannerAdminService:
		switch method {
		case "UpdateDatabaseDdl":
			return spannerDDLStatements(rendered), true
		case "CreateDatabase":
			return spannerCreateDatabaseSQL(rendered), true
		}
	}
	return spannerRequest{}, false
}

// spannerDatabaseOf reduces a session resource name to its database:
// projects/P/instances/I/databases/D/sessions/S loses the session part. A
// database name passes through unchanged; anything else returns "" so a
// per_database lane fails closed rather than matching a malformed key.
func spannerDatabaseOf(session string) string {
	if i := strings.Index(session, "/sessions/"); i > 0 {
		session = session[:i]
	}
	if !isSpannerDatabaseName(session) {
		return ""
	}
	return session
}

// spannerTopLevelSQL reads {"session": "...", "sql": "..."}. Both fields
// spell the same in proto-name and lowerCamel JSON.
func spannerTopLevelSQL(rendered string) spannerRequest {
	var m struct {
		Session string `json:"session"`
		SQL     string `json:"sql"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil || m.SQL == "" {
		return spannerRequest{}
	}
	return spannerRequest{sqls: []string{m.SQL}, database: spannerDatabaseOf(m.Session)}
}

// spannerBatchDMLSQL reads ExecuteBatchDml's statements array: objects
// each carrying a `sql` field. Members with no sql (an empty statement the
// server would refuse anyway) are skipped rather than emitted as empty
// statements the lexer would report unknown.
func spannerBatchDMLSQL(rendered string) spannerRequest {
	var m struct {
		Session    string `json:"session"`
		Statements []struct {
			SQL string `json:"sql"`
		} `json:"statements"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil {
		return spannerRequest{}
	}
	req := spannerRequest{database: spannerDatabaseOf(m.Session)}
	for _, s := range m.Statements {
		if s.SQL != "" {
			req.sqls = append(req.sqls, s.SQL)
		}
	}
	return req
}

// spannerDDLStatements reads UpdateDatabaseDdl's statements array: plain
// strings, one DDL statement each, against the `database` field.
func spannerDDLStatements(rendered string) spannerRequest {
	var m struct {
		Database   string   `json:"database"`
		Statements []string `json:"statements"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil {
		return spannerRequest{}
	}
	req := spannerRequest{database: spannerDatabaseOf(m.Database)}
	for _, s := range m.Statements {
		if s != "" {
			req.sqls = append(req.sqls, s)
		}
	}
	return req
}

// spannerCreateDatabaseSQL reads CreateDatabase: a `create_statement`
// string plus an `extra_statements` array of strings, and the request's
// own `database_dialect`, the one place the API states a dialect. These
// fields are where the proto-name/lowerCamel split is real, so both
// spellings are read and the proto-name one wins when both are present.
// The enum renders by name; an absent or UNSPECIFIED value is GoogleSQL,
// which is what the service creates in that case.
func spannerCreateDatabaseSQL(rendered string) spannerRequest {
	var m struct {
		CreateStatement      string   `json:"create_statement"`
		CreateStatementCamel string   `json:"createStatement"`
		ExtraStatements      []string `json:"extra_statements"`
		ExtraStatementsCamel []string `json:"extraStatements"`
		Dialect              string   `json:"database_dialect"`
		DialectCamel         string   `json:"databaseDialect"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil {
		return spannerRequest{}
	}
	create := m.CreateStatement
	if create == "" {
		create = m.CreateStatementCamel
	}
	extras := m.ExtraStatements
	if len(extras) == 0 {
		extras = m.ExtraStatementsCamel
	}
	dialect := m.Dialect
	if dialect == "" {
		dialect = m.DialectCamel
	}
	req := spannerRequest{declared: SpannerDialectGoogleSQL}
	if dialect == "POSTGRESQL" {
		req.declared = SpannerDialectPostgreSQL
	}
	if create != "" {
		req.sqls = append(req.sqls, create)
	}
	for _, s := range extras {
		if s != "" {
			req.sqls = append(req.sqls, s)
		}
	}
	return req
}

// spannerSQL builds the statement for one extracted SQL string. msgIndex is
// the RPC message the SQL came from (grpc.message_index, as on the generic
// message statement); sqlIndex is 1-based within that message.
//
// Two rule families read this RPC, and they read DIFFERENT statements. The
// RPC-open statement (laneStatements.request) keeps Tables = service and
// service/method, because that is what a `table` rule fencing a method
// matches — it fires at the request headers, before the upstream is dialed,
// whatever the payload turns out to hold. THIS statement replaces Tables
// with the relations the SQL names, because an operation or table rule
// about data ("nothing deletes from accounts") is asking about the query,
// not about the RPC envelope around it. Folding both fact sets into one
// statement would make a method fence fire on every query that happens to
// travel through the fenced method — the rule families must not observe
// each other's vocabulary.
func (s *laneStatements) spannerSQL(sql, rendered string, truncated bool, msgIndex, sqlIndex int, database, dialect string, d lexer.Dialect) inspect.Statement {
	stmt := s.message(inspect.FromClient, rendered, truncated, msgIndex)
	a := inspect.AnalyzeSQLIn(sql, d)
	stmt.Text = sql
	stmt.Operation = a.Operation
	stmt.Effects = a.Effects
	stmt.Relations = a.Relations
	stmt.Tables = a.Tables
	if !a.Complete {
		// Same contract as every SQL codec: an unreadable statement is
		// OpUnknown with the reason on the record, so a rule naming
		// `unknown` refuses it and the trail says why.
		stmt.Metadata[inspect.MetadataSQLIncomplete] = a.Reason
	}
	stmt.Metadata[spannerSQLIndexMetadata] = strconv.Itoa(sqlIndex)
	stmt.Metadata[spannerDialectMetadata] = dialect
	if database != "" {
		stmt.Metadata[spannerDatabaseMetadata] = database
	}
	return stmt
}

// spannerUnreadable is the statement for a SQL-bearing method whose SQL
// cannot be read: a truncated capture, a rendering that does not parse, a
// message carrying only empty statements, or — under dialect per_database
// — a database the config does not list. It is the generic message
// statement with the operation forced to OpUnknown, because "this method
// carries SQL and we could not read it" is exactly the situation OpUnknown
// exists for — a rule naming `unknown` refuses it, and letting it ride as
// OpCall would make the capture budget a policy bypass: pad the request
// past max_payload_bytes and the DELETE inside is never analyzed.
func (s *laneStatements) spannerUnreadable(rendered string, truncated bool, msgIndex int, reason string) inspect.Statement {
	stmt := s.message(inspect.FromClient, rendered, truncated, msgIndex)
	stmt.Operation = inspect.OpUnknown
	if reason == "" {
		reason = "spanner request rendering yielded no SQL on a SQL-bearing method"
		if truncated {
			reason = "spanner request rendering was truncated at the capture budget before the SQL could be read"
		}
	}
	stmt.Metadata[inspect.MetadataSQLIncomplete] = reason
	return stmt
}
