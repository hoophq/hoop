package inspect

import (
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/lexer"
)

// This file adapts the lexer package to the types a Statement carries.
//
// The analysis itself lives in lexer/ because it is a self-contained scanner
// with its own vocabulary, its own dialect table and a conformance harness
// that must not become a dependency of this module. The mapping remains
// here, plus one policy decision: an incomplete scan reports OpUnknown.
//
// # Read this before writing a rule
//
// Operation is the WORST thing the statement does, not the verb the user
// typed. `WITH x AS (DELETE FROM t) SELECT count(*)` is a delete, because a
// policy asking "may this run" is asking about the effect. Statement.Effects
// carries the full set when you need to tell them apart.
//
// The scanner's vocabulary is wider than Operation's: it models merge, copy
// and explain, which no rule and no config names. operationOf folds those
// onto the verb carrying the same consequence, so a rule naming `update`
// fires on a MERGE and one naming `insert` fires on a COPY FROM.
//
// An incomplete scan is reported as OpUnknown with the reason in
// Metadata["sql.incomplete"]. That is deliberate and it is the fail-closed
// path: a rule naming `unknown` refuses what the scanner could not read,
// and one that does not name it accepts that risk explicitly.

// AnalyzeSQL classifies a statement for the dialect the protocol implies.
//
// The dialect is not cosmetic: '[' opens a quoted identifier in T-SQL and is
// an array subscript in PostgreSQL, so one set of lexical rules cannot serve
// both without mangling one of them.
func AnalyzeSQL(sql string, proto Protocol) SQLAnalysis {
	d := lexer.Postgres
	switch proto {
	case MSSQL:
		d = lexer.MSSQL
	case MySQL:
		d = lexer.MySQL
	case GRPC:
		// gRPC statements carry protobuf renderings, not SQL. Falling
		// through to the PostgreSQL lexer would "analyze" protojson and
		// report whatever relations it hallucinates; an explicit
		// incomplete result fails closed instead, the same way an
		// unreadable SQL statement does (a rule naming `unknown` refuses
		// it, everything else declines to match).
		return SQLAnalysis{
			Operation: OpUnknown,
			Complete:  false,
			Reason:    "grpc statements carry no SQL to analyze",
		}
	}
	a := lexer.Analyze(sql, d)

	// COPY is the one verb whose consequence depends on its direction, and
	// the scanner reports that direction as the access of the relation COPY
	// named rather than as part of the verb. A statement that writes nothing
	// cannot be a COPY FROM.
	//
	// A data-modifying subquery makes this read pessimistic:
	// `COPY (DELETE FROM t RETURNING *) TO STDOUT` writes t, so the copy
	// itself is reported as an insert among the effects. Operation is
	// unaffected — the delete outranks it — and the error is a spurious
	// denial rather than a miss.
	copyWrites := false
	for _, r := range a.Relations {
		if r.Access == lexer.Write {
			copyWrites = true
			break
		}
	}

	out := SQLAnalysis{
		Operation: operationOf(a.Severity(), copyWrites),
		Complete:  a.Complete,
		Reason:    a.Reason,
	}
	if !a.Complete {
		// The scanner met something it does not model. Reporting its
		// best guess as a verb would be the silent fail-open this whole
		// design exists to remove.
		out.Operation = OpUnknown
	}
	if len(a.Effects) > 0 {
		out.Effects = make([]Operation, 0, len(a.Effects))
		for _, e := range a.Effects {
			// Folding merge onto update collapses `merge, update` to one
			// entry, so the set is deduplicated on the way out.
			if op := operationOf(e, copyWrites); !slices.Contains(out.Effects, op) {
				out.Effects = append(out.Effects, op)
			}
		}
	}
	if len(a.Relations) > 0 {
		out.Relations = make([]Relation, 0, len(a.Relations))
		out.Tables = make([]string, 0, len(a.Relations))
		for _, r := range a.Relations {
			acc := AccessRead
			if r.Access == lexer.Write {
				acc = AccessWrite
			}
			// Lowercased here rather than in the scanner. The scanner
			// keeps a quoted identifier verbatim because "Customers"
			// and customers ARE different relations in PostgreSQL, but
			// Statement.Tables has always been lowercase and policy
			// rules compare against it. Changing that quietly would
			// stop deployed table rules matching.
			name := strings.ToLower(r.Name)
			out.Relations = append(out.Relations, Relation{Name: name, Access: acc})
			out.Tables = append(out.Tables, name)
		}
	}
	return out
}

// operationOf maps a scanner verb onto the Operation vocabulary rules are
// written against.
//
// The lexer models three verbs this package does not name — merge, copy and
// explain — and converting the string straight across was a bypass. Both
// policy.MatchOperation and the AI analyzer's trigger compare for equality,
// so a read-only rule naming insert/update/delete never fired on
// `MERGE INTO customers ... WHEN MATCHED THEN UPDATE` and never stopped
// `COPY customers FROM STDIN`.
//
// Each of the three folds onto the vocabulary verb carrying the same
// consequence, never onto OpOther. OpOther sits at the bottom of the severity
// order, so parking a bulk load there would leave the hole this closes.
//
// The switch is exhaustive deliberately. A verb added to the scanner later
// lands on OpUnknown, which fails closed, rather than leaking a fourth string
// no config names.
func operationOf(v lexer.Verb, copyWrites bool) Operation {
	switch v {
	case lexer.Select:
		return OpSelect
	case lexer.Insert:
		return OpInsert
	case lexer.Update:
		return OpUpdate
	case lexer.Delete:
		return OpDelete
	case lexer.Merge:
		// A MERGE's floor is an upsert, and the scanner already ranks it
		// level with update. A branch that deletes or inserts contributes
		// that verb as its own effect, so naming the floor here loses
		// nothing: `WHEN MATCHED THEN DELETE` still reports delete.
		return OpUpdate
	case lexer.Create:
		return OpCreate
	case lexer.Drop:
		return OpDrop
	case lexer.Alter:
		return OpAlter
	case lexer.Truncate:
		return OpTruncate
	case lexer.Grant:
		return OpGrant
	case lexer.Revoke:
		return OpRevoke
	case lexer.Call:
		return OpCall
	case lexer.Copy:
		// COPY FROM is a bulk insert, COPY TO a bulk read. PostgreSQL
		// models the same split as one bool on CopyStmt. Reporting a verb
		// of its own hid a bulk load from a rule naming insert and a bulk
		// export from one naming select.
		if copyWrites {
			return OpInsert
		}
		return OpSelect
	case lexer.Show:
		return OpShow
	case lexer.Set:
		return OpSet
	case lexer.Begin:
		return OpBegin
	case lexer.Commit:
		return OpCommit
	case lexer.Rollback:
		return OpRollback
	case lexer.Explain:
		// A plan is not an execution. The scanner has already dropped the
		// mutating effects unless ANALYZE was given, and what is left is a
		// read of the relations named — which is how Relations records
		// them, so select is the coherent report.
		return OpSelect
	case lexer.Other:
		return OpOther
	}
	return OpUnknown
}

// ClassifySQL returns the operation and referenced tables for a SQL
// statement, under PostgreSQL lexical rules.
//
// Retained for callers that predate AnalyzeSQL. It flattens away the access
// split and the completeness flag, so a caller keeping this loses the ability
// to tell a write from a read and an unreadable statement from a harmless
// one. Prefer AnalyzeSQL.
func ClassifySQL(sql string) (Operation, []string) {
	a := AnalyzeSQL(sql, Postgres)
	return a.Operation, a.Tables
}
