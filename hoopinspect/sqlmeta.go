package hoopinspect

import (
	"strings"

	"github.com/hoophq/hoopinspect/lexer"
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
// An incomplete scan is reported as OpUnknown with the reason in
// Metadata["sql.incomplete"]. That is deliberate and it is the fail-closed
// path: a rule naming `unknown` refuses what the scanner could not read,
// and one that does not name it accepts that risk explicitly.

// Access says whether a statement reads a relation or changes it.
type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

// Relation is one relation a statement touches, and how.
//
// It is what Tables should have been. A flat name list cannot express the
// difference between `INSERT INTO staging SELECT * FROM customers` and
// `DELETE FROM customers`, so a rule meaning "nothing writes to customers"
// had to be written as "nothing mentions customers" and fired on both.
type Relation struct {
	Name   string `json:"name"`
	Access Access `json:"access"`
}

// MetadataSQLIncomplete names the metadata key carrying why a scan could not
// finish. Present only when Operation is OpUnknown for that reason.
const MetadataSQLIncomplete = "sql.incomplete"

// SQLAnalysis is what one statement does.
type SQLAnalysis struct {
	// Operation is the most consequential effect, or OpUnknown when the
	// scan did not understand the statement.
	Operation Operation

	// Effects is every operation performed anywhere in the statement.
	Effects []Operation

	// Relations lists what is touched and how, deduplicated, with write
	// dominating read.
	Relations []Relation

	// Tables is Relations flattened to names, for callers predating the
	// access split.
	Tables []string

	// Complete reports whether the scan understood the whole statement.
	// False MUST fail closed.
	Complete bool

	// Reason names what defeated the scan. Empty when Complete.
	Reason string
}

// AnalyzeSQL classifies a statement for the dialect the protocol implies.
//
// The dialect is not cosmetic: '[' opens a quoted identifier in T-SQL and is
// an array subscript in PostgreSQL, so one set of lexical rules cannot serve
// both without mangling one of them.
func AnalyzeSQL(sql string, proto Protocol) SQLAnalysis {
	d := lexer.Postgres
	if proto == MSSQL {
		d = lexer.MSSQL
	}
	a := lexer.Analyze(sql, d)

	out := SQLAnalysis{
		Operation: Operation(a.Severity()),
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
			out.Effects = append(out.Effects, Operation(e))
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
