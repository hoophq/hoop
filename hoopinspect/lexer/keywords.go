package lexer

// Verb is a normalized SQL operation.
//
// The vocabulary is this package's rather than the caller's so that lexer
// stays a leaf with no import back into the module root. It is also WIDER:
// merge, copy and explain are modelled here because the analysis needs them,
// and they have no hoopinspect.Operation. The root package folds them onto
// the operation carrying the same consequence; the rest share their string
// with the matching Operation, and lexer_test pins that they do.
type Verb string

const (
	Select   Verb = "select"
	Insert   Verb = "insert"
	Update   Verb = "update"
	Delete   Verb = "delete"
	Merge    Verb = "merge"
	Create   Verb = "create"
	Drop     Verb = "drop"
	Alter    Verb = "alter"
	Truncate Verb = "truncate"
	Grant    Verb = "grant"
	Revoke   Verb = "revoke"
	Call     Verb = "call"
	Copy     Verb = "copy"
	Show     Verb = "show"
	Set      Verb = "set"
	Begin    Verb = "begin"
	Commit   Verb = "commit"
	Rollback Verb = "rollback"
	Explain  Verb = "explain"

	// Other is a statement that parsed but is not one this package
	// classifies. Distinct from Unknown, which means it did not parse.
	Other Verb = "other"

	// Unknown is the absence of a classification.
	Unknown Verb = "unknown"
)

// mutating reports whether a verb changes data or schema.
//
// A policy's usual question is "does this write", and answering it from a set
// of effects rather than from one leading verb is the whole point of this
// package.
func (v Verb) mutating() bool {
	switch v {
	case Insert, Update, Delete, Merge, Create, Drop, Alter, Truncate,
		Grant, Revoke, Call, Copy:
		return true
	}
	return false
}

// severity orders verbs for "report the worst thing this does". A statement
// whose effects are {select, delete} is a delete as far as policy cares.
func (v Verb) severity() int {
	switch v {
	case Drop, Truncate:
		return 6
	case Delete:
		return 5
	case Alter, Grant, Revoke:
		return 4
	case Update, Merge:
		return 3
	case Insert, Create, Copy, Call:
		return 2
	case Select, Show, Explain:
		return 1
	case Other:
		return 0
	}
	return 0
}

// statementVerb maps a leading keyword to its verb. Absent means the keyword
// does not begin a statement, which is what lets the analysis tell a real
// statement head from a keyword appearing mid-clause.
var statementVerb = map[string]Verb{
	"select":   Select,
	"table":    Select, // TABLE t is shorthand for SELECT * FROM t
	"values":   Select,
	"insert":   Insert,
	"update":   Update,
	"delete":   Delete,
	"merge":    Merge,
	"create":   Create,
	"drop":     Drop,
	"alter":    Alter,
	"truncate": Truncate,
	"grant":    Grant,
	"revoke":   Revoke,
	"call":     Call,
	"do":       Call,
	"execute":  Call,
	"exec":     Call,
	"copy":     Copy,
	"show":     Show,
	"set":      Set,
	"reset":    Set,
	"begin":    Begin,
	"start":    Begin,
	"commit":   Commit,
	"end":      Commit,
	"rollback": Rollback,
	"abort":    Rollback,
	"savepoint": Other,
	"explain":  Explain,
	"analyze":  Other,
	"vacuum":   Other,
	"comment":  Other,
	"prepare":  Other,
	"declare":  Other,
	"fetch":    Other,
	"close":    Other,
	"listen":   Other,
	"notify":   Other,
	"lock":     Other,
	"refresh":  Other,
	"reindex":  Other,
	"cluster":  Other,
	"discard":  Other,
	"use":      Other,
	"with":     Other, // resolved by the CTE walk, never left as-is
}

// opaque marks statement forms whose effect is decided at runtime, from a
// string or from the catalog. No amount of parsing resolves them, this
// package's or PostgreSQL's own, so they set Complete=false and the caller
// decides.
//
// A function call inside a SELECT list is the same problem and is NOT listed,
// deliberately: marking every `SELECT count(*)` incomplete would make the
// flag meaningless. That blind spot is documented on the package instead.
var opaque = map[string]string{
	"do":      "anonymous code block; body is interpreted at runtime",
	"call":    "stored procedure; body is in the catalog",
	"execute": "prepared statement; the text was supplied elsewhere",
	"exec":    "stored procedure; body is in the catalog",
}

// CREATE FUNCTION and friends are deliberately absent from opaque.
//
// Defining a function performs exactly one effect, a create, and the body is
// data at that moment. The unanalyzable event is the INVOCATION, which is
// already covered above: CALL, DO and EXECUTE. Marking every migration
// incomplete would make the flag noise and train operators to ignore it.

// relIntro marks keywords after which the next name is a relation.
//
// Some are conditional; see introduces. "index" is deliberately absent: the
// name after it is an index, not a relation, and the table it covers arrives
// after ON.
var relIntro = map[string]bool{
	"from":     true,
	"join":     true,
	"into":     true,
	"update":   true,
	"table":    true,
	"view":     true,
	"truncate": true,
	"using":    true,
	"copy":     true,
	"on":       true,
}

// ddlVerb reports whether a verb acts on a schema object rather than on rows.
// It decides both access and whether ON introduces a relation.
func ddlVerb(v Verb) bool {
	switch v {
	case Create, Drop, Alter, Truncate, Grant, Revoke, Other:
		return true
	}
	return false
}

// introduces reports whether a keyword names a relation under this verb.
//
// Two keywords are ambiguous and cannot be settled from the keyword alone:
//
//   - ON introduces a relation in DDL (`CREATE INDEX i ON t`, `GRANT ... ON
//     t`) and a join predicate everywhere else. Treating it as an introducer
//     unconditionally invents a relation out of `JOIN b ON a.id = b.id`.
//   - FROM introduces a relation for DML and a ROLE for REVOKE.
func introduces(intro string, verb Verb) bool {
	switch intro {
	case "on":
		return ddlVerb(verb)
	case "from":
		return verb != Grant && verb != Revoke
	}
	return true
}

// notARelation are bare words that occupy a relation position but never name
// one. Only bare words: a QUOTED "set" is a legitimate table name and arrives
// with Kind Quoted, which never reaches this table.
//
// The entries earn their place from real misreads. `WHEN MATCHED THEN UPDATE
// SET n = 1` has an UPDATE with no relation of its own, so SET was taken as
// the target; `COPY t FROM STDIN` reported a write to stdin and lost t.
var notARelation = map[string]bool{
	"set": true, "values": true, "select": true, "where": true,
	"do": true, "on": true, "returning": true, "default": true,
	"null": true, "stdin": true, "stdout": true, "program": true,
	"nothing": true, "conflict": true,
}

// relSkip are keywords that may sit between an introducer and the name.
var relSkip = map[string]bool{
	"only":     true,
	"if":       true,
	"exists":   true,
	"not":      true,
	"table":    true,
	"tables":   true,
	"lateral":  true,
	"outer":    true,
	"inner":    true,
	"left":     true,
	"right":    true,
	"full":     true,
	"cross":    true,
	"natural":  true,
	"concurrently": true,
	"materialized": true,
	"recursive":    true,
	"temporary":    true,
	"temp":         true,
	"unlogged":     true,
	"global":       true,
	"local":        true,
}

// headAfter are keywords after which a statement verb may begin. They are how
// a nested statement is recognised without a grammar: `CREATE TABLE x AS
// SELECT`, `WHEN MATCHED THEN DELETE`, `SELECT ... UNION SELECT`.
var headAfter = map[string]bool{
	"as":        true,
	"then":      true,
	"else":      true,
	"union":     true,
	"intersect": true,
	"except":    true,
	"returning": false, // RETURNING is a clause, not a new statement
}

// wrapperModifier are the option keywords that may sit between EXPLAIN and
// the statement it wraps, including inside its parenthesised option list.
//
// They exist so head position survives `EXPLAIN (ANALYZE, BUFFERS) DELETE`.
// Without them the DELETE is not recognised as a statement head and an
// executing command reports no effects at all.
var wrapperModifier = map[string]bool{
	"analyze": true, "verbose": true, "costs": true, "settings": true,
	"buffers": true, "wal": true, "timing": true, "summary": true,
	"format": true, "generic_plan": true, "memory": true, "serialize": true,
	"on": true, "off": true, "true": true, "false": true,
	"text": true, "json": true, "yaml": true, "xml": true,
}
