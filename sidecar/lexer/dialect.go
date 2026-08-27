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
)

func (d Dialect) String() string {
	switch d {
	case MSSQL:
		return "mssql"
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
	// PostgreSQL '[' is an array subscript.
	bracketIdent bool

	// nestedBlockComment makes /* */ nest, which both engines do and the
	// SQL standard does not. `/* a /* b */ DELETE FROM t */` is entirely a
	// comment; a scanner that stops at the first close reports a delete.
	nestedBlockComment bool

	// backslashInPlainString treats \ as an escape inside '...'.
	//
	// FALSE for both supported dialects. PostgreSQL defaults
	// standard_conforming_strings=on, where a backslash is an ordinary
	// character, and T-SQL never had backslash escapes. When a server runs
	// with the setting off, the literal scans short and the trailing quote
	// is left unterminated, which surfaces as Complete=false rather than as
	// a silent misread. That is the correct failure direction.
	backslashInPlainString bool
}

func (d Dialect) rules() lexRules {
	switch d {
	case MSSQL:
		return lexRules{
			nationalString:     true,
			bracketIdent:       true,
			nestedBlockComment: true,
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
