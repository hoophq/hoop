package lexer

import "strings"

// Shape renders sql with no value in it: each string, number, bind and
// quoted name is ?, each comment is gone, and keywords and bare names are
// lowercased. The control plane's approval routing agent reads it (ENG-663).
//
// A quoted name is removed too: MySQL reads "..." as a string or as a name
// by a session setting the scanner cannot see.
//
// ok is false when the scan is incomplete, since what follows the construct
// it could not read may be a value scanned as words, and for MySQL text the
// two backslash readings render differently (see analyzeMySQL).
func Shape(sql string, d Dialect) (string, bool) {
	out, ok := shapeWith(sql, d.rules())
	if !ok || d != MySQL {
		return out, ok
	}
	literal := d.rules()
	literal.backslashInPlainString = false
	if alt, ok := shapeWith(sql, literal); !ok || alt != out {
		return "", false
	}
	return out, true
}

func shapeWith(sql string, rules lexRules) (string, bool) {
	toks, incomplete := scanWith(sql, rules)
	if incomplete != "" {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(sql))
	glue := true // no space before the first token
	for _, t := range toks {
		text := "?"
		switch t.Kind {
		case Word, Punct:
			text = t.Text
		}
		if !glue && !(t.Kind == Punct && strings.Contains(",.;)", text)) {
			b.WriteByte(' ')
		}
		b.WriteString(text)
		glue = t.Kind == Punct && (text == "(" || text == ".")
	}
	return b.String(), true
}
