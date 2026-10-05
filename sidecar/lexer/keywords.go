package lexer

// Verb is a normalized SQL operation.
//
// The vocabulary is this package's rather than the caller's so that lexer
// stays a leaf with no import back into the module root. It is also WIDER:
// merge, copy and explain are modelled here because the analysis needs them,
// and they have no inspect.Operation. The root package folds them onto
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
	"select":    Select,
	"table":     Select, // TABLE t is shorthand for SELECT * FROM t
	"values":    Select,
	"insert":    Insert,
	"update":    Update,
	"delete":    Delete,
	"merge":     Merge,
	"create":    Create,
	"drop":      Drop,
	"alter":     Alter,
	"truncate":  Truncate,
	"grant":     Grant,
	"revoke":    Revoke,
	"call":      Call,
	"do":        Call,
	"execute":   Call,
	"exec":      Call,
	"copy":      Copy,
	"show":      Show,
	"set":       Set,
	"reset":     Set,
	"begin":     Begin,
	"start":     Begin,
	"commit":    Commit,
	"end":       Commit,
	"rollback":  Rollback,
	"abort":     Rollback,
	"savepoint": Other,
	"explain":   Explain,
	"analyze":   Other,
	"vacuum":    Other,
	"comment":   Other,
	"prepare":   Other,
	"declare":   Other,
	"fetch":     Other,
	"close":     Other,
	"listen":    Other,
	"notify":    Other,
	"lock":      Other,
	"refresh":   Other,
	"reindex":   Other,
	"cluster":   Other,
	"discard":   Other,
	"use":       Other,
	"with":      Other, // resolved by the CTE walk, never left as-is
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

// oracleVerb overrides statementVerb under the Oracle dialect, for the
// keywords whose meaning there differs from PostgreSQL's.
//
// BEGIN and DECLARE are the load-bearing rows. Oracle has no BEGIN
// transaction statement and no SQL-level DECLARE CURSOR: both open a PL/SQL
// anonymous block, which is DO $$...$$ without the quotes. Reading BEGIN
// as a transaction start files `BEGIN DELETE FROM t; END;` under begin,
// which a read-only lane forwards. They map to Call, the verb DO and CALL
// already carry, and oracleOpaque marks them unreadable.
//
// FLASHBACK TABLE rewinds a table's rows or undoes its DROP, and PURGE
// destroys a dropped table past recovery. Both are DDL on the named
// object, and both sit in the severity order where their consequence does.
var oracleVerb = map[string]Verb{
	"rename":    Alter, // RENAME t TO u, Oracle's table rename
	"begin":     Call,
	"declare":   Call,
	"flashback": Alter,
	"purge":     Drop,
}

// oracleNotAVerb are statementVerb keywords that begin no statement in
// Oracle. END closes a PL/SQL block, IF or LOOP; left as PostgreSQL's
// COMMIT, every block would report a commit it never ran.
var oracleNotAVerb = map[string]bool{
	"end": true,
}

// oracleOpaque extends opaque for the Oracle dialect.
//
// A PL/SQL block is procedural code: its effects depend on branches,
// loops, cursors and EXECUTE IMMEDIATE strings the scanner cannot run.
// The DML the scanner does see is still reported as effects, but the
// statement is Complete=false, which is the DO $$ ... $$ answer.
var oracleOpaque = map[string]string{
	"begin":   "PL/SQL anonymous block; body is procedural code",
	"declare": "PL/SQL anonymous block; body is procedural code",
}

// oracleInlinePLSQL are the words after WITH that declare PL/SQL a query
// runs (Oracle 12c and later). An autonomous function there can call a
// procedure that writes, so the statement is a block, not a CTE list.
var oracleInlinePLSQL = map[string]bool{
	"function": true, "procedure": true,
}

// plsqlHeadAfter are words after which a statement may begin inside PL/SQL.
// `BEGIN DELETE FROM t` and `LOOP UPDATE t SET ...` put DML right after a
// block keyword, with no semicolon to restore head position.
var plsqlHeadAfter = map[string]bool{
	"begin": true, "declare": true, "loop": true,
}

// plsqlDML are the verbs recognised ANYWHERE inside a PL/SQL block, not
// only in head position.
//
// PL/SQL puts DML where SQL never does: `FORALL i IN 1 .. n DELETE FROM
// t WHERE ...` has the DELETE after an expression. Under-reading records t
// as READ, which a rule guarding writes to t misses. Over-reading is not
// free either: a rule naming an operation matches the effects of an
// unknown statement, so a false DELETE refuses the block. plsqlNotDML
// excludes the two known false readings, member calls (`v.DELETE`) and
// `FOR UPDATE`. SELECT is absent because it never hides a write, and SET,
// VALUES and TABLE are absent because inside DML they are clauses.
var plsqlDML = map[string]bool{
	"insert": true, "update": true, "delete": true, "merge": true,
	"create": true, "drop": true, "alter": true, "truncate": true,
	"grant": true, "revoke": true, "execute": true, "call": true,
}

// oracleStoredUnit are the objects whose CREATE carries a PL/SQL (or Java)
// body inline, with no dollar quote around it. Everything after the header
// is source text Oracle compiles and does not run, so the body is data —
// the same verdict PostgreSQL's CREATE FUNCTION ... AS $$...$$ earns.
var oracleStoredUnit = map[string]bool{
	"procedure": true, "function": true, "package": true,
	"trigger": true, "type": true, "library": true, "java": true,
}

// oracleCreateModifier may sit between CREATE and the stored-unit keyword:
// CREATE OR REPLACE EDITIONABLE PACKAGE BODY, CREATE OR REPLACE AND
// COMPILE JAVA SOURCE.
var oracleCreateModifier = map[string]bool{
	"or": true, "replace": true, "editionable": true, "noneditionable": true,
	"editioning": true, "and": true, "compile": true, "resolve": true,
	"noforce": true, "force": true,
}

// triggerBody are the words that end a trigger's header and open its
// body: a PL/SQL block, a CALL, or a compound trigger.
var triggerBody = map[string]bool{
	"begin": true, "declare": true, "call": true, "compound": true,
}

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
// the target; `COPY t FROM STDIN` reported a write to stdin and lost t;
// GoogleSQL's `INSERT OR UPDATE INTO t` had the mid-statement UPDATE claim
// INTO as its target, and the t behind it — the relation a rule would
// name — was never recorded. INTO is reserved in every dialect here, so a
// bare `into` can never be the name.
var notARelation = map[string]bool{
	"set": true, "values": true, "select": true, "where": true,
	"do": true, "on": true, "returning": true, "default": true,
	"null": true, "stdin": true, "stdout": true, "program": true,
	"nothing": true, "conflict": true, "into": true,
}

// relSkip are keywords that may sit between an introducer and the name.
var relSkip = map[string]bool{
	"only":         true,
	"if":           true,
	"exists":       true,
	"not":          true,
	"table":        true,
	"tables":       true,
	"lateral":      true,
	"outer":        true,
	"inner":        true,
	"left":         true,
	"right":        true,
	"full":         true,
	"cross":        true,
	"natural":      true,
	"concurrently": true,
	"materialized": true,
	"recursive":    true,
	"temporary":    true,
	"temp":         true,
	"unlogged":     true,
	"global":       true,
	"local":        true,
}

// headAfter are keywords after which a statement verb may begin, and after
// which one may begin UNCONDITIONALLY. They are how a nested statement is
// recognised without a grammar: `WHEN MATCHED THEN DELETE`, `SELECT ... UNION
// SELECT`.
//
// AS is not here. It heads a statement in `CREATE TABLE x AS SELECT` and names
// an alias in `SELECT 1 AS delete`, and only the surrounding statement tells
// the two apart; analyzer.headFollows decides it.
var headAfter = map[string]bool{
	"as":        false, // conditional; see analyzer.headFollows
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
