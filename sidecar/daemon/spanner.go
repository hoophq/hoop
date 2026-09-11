package daemon

import (
	"encoding/json"
	"strconv"

	"github.com/hoophq/hoop/sidecar/inspect"
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

// The two services whose requests carry SQL. The data-plane service runs
// queries and DML; the database admin service runs DDL. Everything else on
// a Spanner endpoint (sessions, instance admin, operations) carries no SQL
// and stays on the generic per-message statement, where method fencing and
// payload rules still apply.
const (
	spannerDataService  = "google.spanner.v1.Spanner"
	spannerAdminService = "google.spanner.admin.database.v1.DatabaseAdmin"
)

// spannerSQLStatements returns the SQL strings inside one rendered request
// message, in the order the message carries them. sqlBearing reports that
// the method is KNOWN to carry SQL, whatever the parse produced.
//
// rendered is the protojson form libhoop's renderPayload produced. That
// rendering uses proto field names (UseProtoNames), but the parsers below
// accept the lowerCamel JSON spelling too: protojson accepts both on
// input, so a future rendering change must not silently turn every lane
// into the generic path.
//
// The (nil, true) return is the load-bearing one. A SQL-bearing method
// whose rendering does not parse — a capture truncated at the payload
// budget is the reachable case, and padding a request past the budget is
// CHEAP for a client — must not fall back to the generic OpCall
// statement: the lane's operation rules match nothing there, so the very
// statement most worth reading would be the easiest to smuggle. The
// caller turns (nil, true) into an OpUnknown statement instead, the same
// fail-closed verdict an unlexable SQL string earns, and a rule naming
// `unknown` refuses it.
func spannerSQLStatements(service, method, rendered string) (sqls []string, sqlBearing bool) {
	switch service {
	case spannerDataService:
		switch method {
		case "ExecuteSql", "ExecuteStreamingSql", "PartitionQuery":
			// All three carry the query as a top-level `sql` string.
			// PartitionQuery is included because partitioning a query
			// PLANS it against real tables: the SQL names what will be
			// read, and a rule fencing a table wants to see it here, not
			// only at the ExecuteStreamingSql that follows.
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
	return nil, false
}

// spannerTopLevelSQL reads {"sql": "..."}. The field spells the same in
// proto-name and lowerCamel JSON.
func spannerTopLevelSQL(rendered string) []string {
	var m struct {
		SQL string `json:"sql"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil || m.SQL == "" {
		return nil
	}
	return []string{m.SQL}
}

// spannerBatchDMLSQL reads ExecuteBatchDml's statements array: objects
// each carrying a `sql` field. Members with no sql (an empty statement the
// server would refuse anyway) are skipped rather than emitted as empty
// statements the lexer would report unknown.
func spannerBatchDMLSQL(rendered string) []string {
	var m struct {
		Statements []struct {
			SQL string `json:"sql"`
		} `json:"statements"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil {
		return nil
	}
	var out []string
	for _, s := range m.Statements {
		if s.SQL != "" {
			out = append(out, s.SQL)
		}
	}
	return out
}

// spannerDDLStatements reads UpdateDatabaseDdl's statements array: plain
// strings, one DDL statement each.
func spannerDDLStatements(rendered string) []string {
	var m struct {
		Statements []string `json:"statements"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil {
		return nil
	}
	var out []string
	for _, s := range m.Statements {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// spannerCreateDatabaseSQL reads CreateDatabase: a `create_statement`
// string plus an `extra_statements` array of strings. These two fields are
// where the proto-name/lowerCamel split is real, so both spellings are
// read and the proto-name one wins when both are present.
func spannerCreateDatabaseSQL(rendered string) []string {
	var m struct {
		CreateStatement      string   `json:"create_statement"`
		CreateStatementCamel string   `json:"createStatement"`
		ExtraStatements      []string `json:"extra_statements"`
		ExtraStatementsCamel []string `json:"extraStatements"`
	}
	if json.Unmarshal([]byte(rendered), &m) != nil {
		return nil
	}
	create := m.CreateStatement
	if create == "" {
		create = m.CreateStatementCamel
	}
	extras := m.ExtraStatements
	if len(extras) == 0 {
		extras = m.ExtraStatementsCamel
	}
	var out []string
	if create != "" {
		out = append(out, create)
	}
	for _, s := range extras {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
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
func (s *laneStatements) spannerSQL(sql, rendered string, truncated bool, msgIndex, sqlIndex int) inspect.Statement {
	stmt := s.message(inspect.FromClient, rendered, truncated, msgIndex)
	a := inspect.AnalyzeSQL(sql, inspect.Spanner)
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
	return stmt
}

// spannerUnreadable is the statement for a SQL-bearing method whose
// rendering yielded no SQL: a truncated capture, a rendering that does not
// parse, or a message carrying only empty statements. It is the generic
// message statement with the operation forced to OpUnknown, because "this
// method carries SQL and we could not read it" is exactly the situation
// OpUnknown exists for — a rule naming `unknown` refuses it, and letting
// it ride as OpCall would make the capture budget a policy bypass: pad the
// request past max_payload_bytes and the DELETE inside is never analyzed.
func (s *laneStatements) spannerUnreadable(rendered string, truncated bool, msgIndex int) inspect.Statement {
	stmt := s.message(inspect.FromClient, rendered, truncated, msgIndex)
	stmt.Operation = inspect.OpUnknown
	reason := "spanner request rendering yielded no SQL on a SQL-bearing method"
	if truncated {
		reason = "spanner request rendering was truncated at the capture budget before the SQL could be read"
	}
	stmt.Metadata[inspect.MetadataSQLIncomplete] = reason
	return stmt
}
