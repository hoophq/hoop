package lexer

import "strings"

// InsertData splits a ClickHouse INSERT whose rows follow its FORMAT clause,
// `INSERT INTO t FORMAT JSONEachRow\n{"id":1}`, at the end of the format name.
// The server parses what follows with that format, never as SQL, so a caller
// analyzes head alone. ok is false for any other statement or dialect, and
// when the scan cannot read up to the format name.
func InsertData(sql string, d Dialect) (head string, ok bool) {
	if d != ClickHouse {
		return "", false
	}
	s := &scanner{src: sql, rules: d.rules()}
	tok, more := s.next()
	if !more || !tok.isWord("insert") {
		return "", false
	}
	prev, depth := tok, 0
	for {
		tok, more = s.next()
		if !more || s.incomplete != "" {
			return "", false
		}
		switch {
		case tok.Kind == Punct && (tok.Text == "(" || tok.Text == "["):
			depth++
		case tok.Kind == Punct && (tok.Text == ")" || tok.Text == "]"):
			depth--
		case depth > 0 || namesTarget(prev):
		case tok.Kind == Punct && tok.Text == ";":
			return "", false
		case tok.isWord("select") || tok.isWord("with") || tok.isWord("values"):
			// INSERT ... SELECT runs SQL to its end, and VALUES rows are
			// expressions: the whole text is the statement.
			return "", false
		case tok.isWord("format"):
			name, more := s.next()
			// Values rows can be expressions with scalar subqueries, so
			// they are read as SQL like a VALUES clause.
			if !more || s.incomplete != "" || !name.isName() || strings.EqualFold(name.Text, "values") {
				return "", false
			}
			return sql[:s.pos], true
		}
		prev = tok
	}
}

// namesTarget reports whether the token after prev is the INSERT's target,
// so `INSERT INTO format FORMAT TSV` reads its table as a name.
func namesTarget(prev Token) bool {
	return prev.isWord("into") || prev.isWord("table") || prev.isWord("function") ||
		(prev.Kind == Punct && prev.Text == ".")
}
