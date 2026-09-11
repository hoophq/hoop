// Package lexer turns SQL text into the two facts a policy needs: what the
// statement DOES, and to which relations.
//
// # Why this is not a parser
//
// A SQL grammar is large, dialect-specific and a permanent maintenance
// burden. The question a policy asks is much smaller: which relations does
// this statement write, and which does it read. Answering it needs verbs and
// the relation names next to them, both of which are token-local. It does
// NOT need expression grammar, and expression grammar is where most of a real
// parser's cost lives. So there is no precedence table here, no operator
// handling beyond "these bytes end a token", and no AST.
//
// It does need a STACK, which the previous implementation lacked. Tracking
// parenthesis depth as one integer can tell you "somewhere inside
// parentheses" but not "inside a CTE body", which is why a data-modifying
// CTE (`WITH x AS (DELETE FROM t) SELECT ...`) read as a plain select. A
// stack of LABELLED regions costs one byte per nesting level and answers it
// exactly.
//
// # The ceiling, and why it is explicit
//
// This package will meet SQL it does not model. It does not always answer,
// and it never guesses: Analysis.Complete reports whether the scan
// understood the whole statement, and a caller MUST fail closed when it is
// false. A classifier that silently guesses in the permissive direction is
// decoration; one that admits defeat is a control.
//
// Three shapes are permanently out of reach for any amount of parsing, this
// package or PostgreSQL's own:
//
//   - `DO $$ ... $$`: the body is a string interpreted at runtime.
//   - `CALL proc()` / `SELECT func()`: the body lives in the catalog.
//   - `EXECUTE p`: the statement was named elsewhere, possibly earlier.
//
// All three set Complete=false with a Reason, which is the only honest answer.
package lexer

// Dialect selects the lexical rules. It is not a grammar switch: the analysis
// that follows is shared. It exists because the same byte means different
// things per engine and no union lexer can be correct for both.
//
// The clearest case is '[': in T-SQL it opens a quoted identifier, in
// PostgreSQL it is an array subscript. A lexer treating it as an identifier
// everywhere mangles `SELECT tags[1] FROM t`; one treating it as an operator
// everywhere loses `[dbo].[customers]`.
type Dialect uint8

const (
	// Postgres covers PostgreSQL and wire-compatible engines.
	Postgres Dialect = iota

	// MSSQL covers Microsoft SQL Server and T-SQL.
	MSSQL

	// MySQL covers MySQL and MariaDB.
	MySQL

	// GoogleSQL covers ZetaSQL as Cloud Spanner and BigQuery speak it.
	//
	// It is close enough to MySQL to be mistaken for it — backticks,
	// '#' comments, backslash escapes — and different in exactly the
	// places that hide SQL: '"' opens a STRING rather than an identifier,
	// r'...' turns backslash into an ordinary byte, and '''...''' runs a
	// literal across quotes that would close anything else. Each of those
	// is a rules row below.
	GoogleSQL
)

func (d Dialect) String() string {
	switch d {
	case MSSQL:
		return "mssql"
	case MySQL:
		return "mysql"
	case GoogleSQL:
		return "googlesql"
	}
	return "postgres"
}

// lexRules is the per-dialect quoting and comment matrix.
//
// Every entry here corresponds to a way the previous lexer could be walked
// past. They are data rather than branches in the scanner so that adding an
// engine is a table row instead of an edit to the state machine.
type lexRules struct {
	// dollarQuote enables $$...$$ and $tag$...$tag$.
	//
	// PostgreSQL only. Without it a function body scans as live SQL, so a
	// CREATE FUNCTION reports phantom writes, and an unbalanced parenthesis
	// inside the body corrupts the region stack.
	dollarQuote bool

	// escapeString enables E'...' with backslash escapes.
	//
	// PostgreSQL only, and load-bearing: E'' honours backslashes in every
	// server configuration, so `SET x = E'O\'Brien'; DELETE FROM t` is one
	// literal followed by a second statement. A scanner that stops at the
	// backslash-quote swallows the semicolon and the DELETE disappears.
	escapeString bool

	// nationalString enables N'...'. T-SQL only.
	nationalString bool

	// unicodeIdent enables U&"..." and the UESCAPE clause. PostgreSQL only.
	unicodeIdent bool

	// bracketIdent enables [name] with the ]] escape. T-SQL only; in
	// PostgreSQL '[' is an array subscript and in MySQL '[' is nothing at
	// all, so `SELECT [a] FROM t` there is not a relation named a.
	bracketIdent bool

	// backtickIdent enables `name` with the doubled-backtick escape.
	//
	// MySQL only, and it is the ONLY way MySQL spells a quoted identifier
	// out of the box, so this is not an optional nicety: without it
	// ``DELETE FROM `select` `` scans the backticks as stray punctuation
	// and the relation lands in the keyword table as a bare `select`,
	// which the relation filter then drops. The delete is reported with no
	// target and a rule naming that table never fires. In PostgreSQL and
	// T-SQL a backtick is not an identifier delimiter at all — it is not
	// even valid syntax — so enabling it there would only invent names out
	// of typos.
	backtickIdent bool

	// nestedBlockComment makes /* */ nest, which PostgreSQL and T-SQL do
	// and the SQL standard does not. `/* a /* b */ DELETE FROM t */` is
	// entirely a comment there; a scanner that stops at the first close
	// reports a delete. MySQL is the standard-conforming one and does NOT
	// nest, where the same text ends at the first `*/` and the DELETE is
	// live SQL — so this flag decides which of two opposite readings is
	// the misread.
	nestedBlockComment bool

	// hashComment enables '#' as a comment to end of line.
	//
	// MySQL only. Without it `SELECT 1 # DROP TABLE t` scans the commented
	// tail as live SQL and reports a drop nobody performed, which refuses
	// a select. Enabling it for the other dialects has the mirror cost and
	// it is the worse one: '#' is a live operator in both — PostgreSQL
	// spells XOR and several geometric operators with it — so a '#' there
	// would swallow the rest of the line, and any statement after it on
	// that line disappears from the analysis entirely.
	hashComment bool

	// dashCommentUpToNewline requires whitespace (or end of input) after
	// `--` before it opens a comment.
	//
	// MySQL only, and it closes a hole rather than adding a convenience.
	// MySQL reads `--` glued to a following token as two minus signs, so
	// `SELECT 1--2; DELETE FROM t` is two statements and the delete runs.
	// A scanner applying the PostgreSQL rule treats `--2; DELETE FROM t`
	// as a comment, sees one harmless select, and the delete is never
	// analyzed at all. That is a statement executing unseen, which is the
	// one outcome this package exists to prevent.
	dashCommentNeedsSpace bool

	// executableComment enables `/*! ... */` and `/*!50000 ... */`, whose
	// bodies MySQL EXECUTES.
	//
	// MySQL only, and it is a bypass rather than a nicety. The body is real
	// SQL: `/*! DROP TABLE t */` drops the table. A scanner treating it as
	// an ordinary comment reports no verb at all, so a rule refusing `drop`
	// matches nothing and the statement is forwarded. Verified on MySQL
	// 8.4 through a relay configured to refuse destructive SQL — the table
	// was gone and nothing was denied.
	//
	// PostgreSQL and T-SQL have no such construct; there `/*!` opens an
	// ordinary comment, and enabling this would invent verbs out of
	// commentary.
	executableComment bool

	// backslashInPlainString treats \ as an escape inside '...'.
	//
	// FALSE for PostgreSQL and T-SQL. PostgreSQL defaults
	// standard_conforming_strings=on, where a backslash is an ordinary
	// character, and T-SQL never had backslash escapes. When a server runs
	// with the setting off, the literal scans short and the trailing quote
	// is left unterminated, which surfaces as Complete=false rather than as
	// a silent misread. That is the correct failure direction.
	//
	// TRUE for MySQL, where it is not a guess: backslash escapes are on
	// unless NO_BACKSLASH_ESCAPES was set, so the default reading of
	// `SELECT 'O\'Brien'; DELETE FROM t` is one literal followed by a
	// second statement. Leaving it false there swallows the semicolon into
	// the literal and the DELETE disappears — the same loss the
	// escapeString comment describes for Postgres, but on the ordinary
	// quoting every MySQL client emits.
	backslashInPlainString bool

	// doubleQuoteString makes "..." a STRING literal rather than a quoted
	// identifier.
	//
	// GoogleSQL only. There the two quote characters are interchangeable
	// string delimiters and identifiers are backticked, so reading "..."
	// with the PostgreSQL rule invents a quoted identifier out of string
	// data — `SELECT "a\";DELETE FROM t;--"` would close the "identifier"
	// at the escaped quote and scan the literal's tail, a live DELETE
	// nobody wrote, as SQL. The misread direction is a false denial, but
	// it also puts string CONTENT into a token, which the Literal kind
	// exists to prevent.
	doubleQuoteString bool

	// backtickBackslashEscape makes backslash the escape inside `...`
	// identifiers, replacing the doubled-backtick escape.
	//
	// GoogleSQL only. MySQL escapes a backtick by doubling it; GoogleSQL
	// by preceding it with a backslash. Reading GoogleSQL's `a\`b` under
	// the MySQL rule ends the identifier at the escaped backtick, and the
	// stray tail re-opens quoting that swallows whatever follows —
	// including a separator and the statement after it. The flag rather
	// than a constant because MySQL's doubling must keep working
	// unchanged.
	backtickBackslashEscape bool

	// rawString enables the r/R and byte rb/br prefixes: r'...', R"...",
	// rb'...', in which a backslash is an ORDINARY byte and never escapes
	// the closing quote.
	//
	// GoogleSQL only, and it is the misread class this dialect exists to
	// prevent: under the escaping read, r'\' swallows its terminator, the
	// literal runs on, and every live statement after it disappears into
	// a phantom string — `SELECT r'\'; DELETE FROM t; --'` is a select
	// with a hidden delete.
	rawString bool

	// tripleQuoteString enables '''...''' and """...""", which run across
	// the single quotes that would close an ordinary literal.
	//
	// GoogleSQL only. The body is data: a DELETE inside a triple-quoted
	// string must not classify, and the closing delimiter is three quotes,
	// so a scanner without this rule ends the literal two quotes early and
	// reads the rest of the string as SQL.
	tripleQuoteString bool
}

func (d Dialect) rules() lexRules {
	switch d {
	case MSSQL:
		return lexRules{
			nationalString:     true,
			bracketIdent:       true,
			nestedBlockComment: true,
		}
	case MySQL:
		// nestedBlockComment is deliberately absent, and it is not the
		// typo it looks like beside the two rows above: MySQL follows
		// the standard here and the FIRST `*/` closes the comment, so
		// `/* a /* b */ DELETE FROM t */` really does execute a delete.
		// Setting it true would file that delete as commented-out.
		//
		// backslashInPlainString is on for the reverse reason — the
		// engine's default is the non-standard one, and it is the only
		// dialect here where a bare '...' honours backslashes.
		return lexRules{
			backtickIdent:          true,
			hashComment:            true,
			dashCommentNeedsSpace:  true,
			executableComment:      true,
			backslashInPlainString: true,
		}
	case GoogleSQL:
		// The MySQL-adjacent rows (backticks, '#', backslash escapes)
		// are shared; nestedBlockComment stays absent because ZetaSQL
		// follows the standard and the FIRST `*/` closes the comment.
		// dashCommentNeedsSpace stays absent too: `--` opens a comment
		// unconditionally, as in PostgreSQL. Everything PostgreSQL-only
		// (dollar quotes, E'', U&"") and MySQL-only (executable
		// comments) is deliberately off — those bytes are inert in
		// ZetaSQL and honouring them would hide or invent statements.
		return lexRules{
			backtickIdent:           true,
			backtickBackslashEscape: true,
			hashComment:             true,
			backslashInPlainString:  true,
			doubleQuoteString:       true,
			rawString:               true,
			tripleQuoteString:       true,
		}
	default:
		return lexRules{
			dollarQuote:        true,
			escapeString:       true,
			unicodeIdent:       true,
			nestedBlockComment: true,
		}
	}
}
