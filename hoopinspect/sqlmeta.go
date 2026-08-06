package hoopinspect

import "strings"

// This file derives the normalized Operation and the table list from raw SQL.
// Every SQL-bearing codec funnels through here so that a policy sees the
// same shape regardless of which wire protocol delivered the statement.
//
// It is a lexer with a deliberate ceiling:
//
//   - A real SQL grammar is a large dependency (or a large maintenance
//     burden) and this module ships zero dependencies on purpose.
//   - Envoy's postgres_proxy has the same limitation and says so: its docs
//     warn that "currently used parser does not successfully parse all SQL
//     statements" and that metadata is best-effort.
//   - The high-value policies ("no DROP", "no DELETE on customers", "nothing
//     touching this table") are answerable from the leading verb plus the
//     relation names, which a lexer gets right.
//
// The contract you must respect: Operation is reliable, Tables is
// best-effort. Never write a policy that treats an empty Tables as proof the
// statement touches nothing. Fail closed on OpUnknown instead.

// ClassifySQL returns the normalized operation and referenced tables for a
// SQL statement. Comments and string literals are stripped first so a
// statement like `SELECT 'DROP TABLE x'` classifies as a select, and
// `/* DELETE */ SELECT 1` does not classify as a delete.
func ClassifySQL(sql string) (Operation, []string) {
	clean := stripSQLNoise(sql)
	toks := tokenizeSQL(clean)
	if len(toks) == 0 {
		return OpUnknown, nil
	}
	return classifyTokens(toks)
}

// stripSQLNoise removes line comments, block comments and quoted literals,
// replacing each with a single space so token boundaries survive.
//
// Quoted IDENTIFIERS are kept (with quotes removed) because `DELETE FROM
// "customers"` must still yield the table. Only VALUE literals ('...' and
// their doubled-quote escapes) are dropped, since a policy matching on data
// content is a different feature than matching on statement shape.
func stripSQLNoise(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))

	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		// -- line comment
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			b.WriteByte(' ')

		// # line comment (MySQL)
		case c == '#':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			b.WriteByte(' ')

		// /* block comment */. Not nested in standard SQL, and treating it
		// as non-nesting matches what the servers do.
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < len(sql) {
				i += 2 // consume */
			} else {
				i = len(sql) // unterminated
			}
			b.WriteByte(' ')

		// 'string literal', dropped. '' inside is an escaped quote.
		case c == '\'':
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			b.WriteByte(' ')

		// "quoted identifier", kept with quotes dropped. Doubled "" escapes.
		case c == '"':
			i++
			for i < len(sql) {
				if sql[i] == '"' {
					if i+1 < len(sql) && sql[i+1] == '"' {
						b.WriteByte('"')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(sql[i])
				i++
			}

		// `backtick identifier` (MySQL), kept with backticks dropped.
		case c == '`':
			i++
			for i < len(sql) && sql[i] != '`' {
				b.WriteByte(sql[i])
				i++
			}
			if i < len(sql) {
				i++
			}

		// [bracket identifier] (MSSQL), kept with brackets dropped.
		case c == '[':
			i++
			for i < len(sql) && sql[i] != ']' {
				b.WriteByte(sql[i])
				i++
			}
			if i < len(sql) {
				i++
			}

		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// tokenizeSQL splits on whitespace and punctuation that cannot appear inside
// an identifier, lowercasing as it goes. Dots are preserved so `schema.table`
// stays one token, because a policy usually wants the qualified name.
func tokenizeSQL(s string) []string {
	var toks []string
	var cur strings.Builder

	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}

	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			cur.WriteByte(c + ('a' - 'A'))
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '$', c == '.':
			cur.WriteByte(c)
		case c == ';':
			flush()
			toks = append(toks, ";")
		case c == '(', c == ')', c == ',':
			flush()
			toks = append(toks, string(c))
		default:
			flush()
		}
	}
	flush()
	return toks
}

// leadingVerb maps the first keyword of a statement to an Operation. Verbs
// needing a second token to disambiguate (CREATE TABLE vs CREATE INDEX) are
// handled in classifyTokens.
var leadingVerb = map[string]Operation{
	"select":   OpSelect,
	"with":     OpSelect, // CTE; the ultimate verb may differ, refined below
	"insert":   OpInsert,
	"replace":  OpInsert, // MySQL REPLACE INTO
	"update":   OpUpdate,
	"delete":   OpDelete,
	"create":   OpCreate,
	"drop":     OpDrop,
	"alter":    OpAlter,
	"truncate": OpTruncate,
	"grant":    OpGrant,
	"revoke":   OpRevoke,
	"call":     OpCall,
	"exec":     OpCall,
	"execute":  OpCall,
	"show":     OpShow,
	"set":      OpSet,
	"begin":    OpBegin,
	"start":    OpBegin,
	"commit":   OpCommit,
	"rollback": OpRollback,
	"explain":  OpOther,
	"analyze":  OpOther,
	"vacuum":   OpOther,
	"copy":     OpOther,
	"use":      OpOther,
	"desc":     OpShow,
	"describe": OpShow,
}

// tableIntroducer marks keywords after which the next identifier names a
// relation.
var tableIntroducer = map[string]bool{
	"from":     true,
	"join":     true,
	"into":     true,
	"update":   true,
	"table":    true,
	"truncate": true,
}

// notATable catches keywords that can follow an introducer but are not
// relation names, so `DELETE FROM ONLY t` and `DROP TABLE IF EXISTS t` do not
// record "only" or "if" as tables.
var notATable = map[string]bool{
	"only": true, "if": true, "exists": true, "not": true,
	"select": true, "lateral": true, "unnest": true, "values": true,
	"cascade": true, "restrict": true, "temporary": true, "temp": true,
}

func classifyTokens(toks []string) (Operation, []string) {
	op, ok := leadingVerb[toks[0]]
	if !ok {
		op = OpUnknown
	}

	// A CTE (`WITH x AS (...) DELETE FROM y`) is classified by its real verb,
	// which is the first top-level statement keyword after the CTE list. A
	// policy denying DELETE must not be fooled by a WITH prefix.
	if toks[0] == "with" {
		if v := verbAfterCTE(toks); v != OpUnknown {
			op = v
		}
	}

	// CREATE/DROP/ALTER apply to many object kinds. Record the kind in the
	// operation only where it changes the risk profile; for the rest the
	// verb alone is what a policy keys on.
	switch toks[0] {
	case "start":
		// START TRANSACTION only; bare `start` is not a statement.
		if len(toks) < 2 || toks[1] != "transaction" {
			op = OpUnknown
		}
	}

	return op, extractTables(toks)
}

// verbAfterCTE walks past a `WITH a AS (...), b AS (...)` prefix and returns
// the operation of the statement that follows. Parenthesis depth is tracked so
// a verb nested inside a CTE body is not mistaken for the top-level one.
func verbAfterCTE(toks []string) Operation {
	depth := 0
	for i := 1; i < len(toks); i++ {
		switch toks[i] {
		case "(":
			depth++
		case ")":
			depth--
		default:
			if depth != 0 {
				continue
			}
			// At depth 0, after the CTE list, the first real verb wins.
			if v, ok := leadingVerb[toks[i]]; ok && toks[i] != "with" {
				return v
			}
		}
	}
	return OpUnknown
}

// extractTables collects identifiers following a table-introducing keyword.
// Deduplicated, order preserved.
func extractTables(toks []string) []string {
	var out []string
	seen := map[string]bool{}

	for i := range len(toks) - 1 {
		if !tableIntroducer[toks[i]] {
			continue
		}
		// Skip modifiers and chained introducers between the keyword and the
		// name. Both occur in real statements:
		//
		//   DELETE FROM ONLY parts     -> modifier
		//   DROP TABLE IF EXISTS t     -> modifiers
		//   TRUNCATE TABLE logs        -> "truncate" and "table" are BOTH
		//                                 introducers, so without this the
		//                                 relation would be recorded as
		//                                 "table".
		j := i + 1
		for j < len(toks) && (notATable[toks[j]] || tableIntroducer[toks[j]]) {
			j++
		}
		if j >= len(toks) {
			continue
		}
		name := toks[j]
		if !isIdentifier(name) {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// isIdentifier rejects punctuation tokens and bare numbers, which appear after
// an introducer in constructs like `FROM (SELECT ...)` or `LIMIT 1`.
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	switch s {
	case "(", ")", ",", ";":
		return false
	}
	// A token that is only digits and dots is a number, not a relation.
	allNumeric := true
	for i := range len(s) {
		if (s[i] < '0' || s[i] > '9') && s[i] != '.' {
			allNumeric = false
			break
		}
	}
	return !allNumeric
}
