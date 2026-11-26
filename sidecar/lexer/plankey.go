package lexer

import (
	"slices"
	"strconv"
	"strings"
)

const maxPlaceholders = 65535

// Parameterize reads sql as PostgreSQL and returns it with $n in place of each
// literal whose value does not change the plan's row estimates, so one
// generic plan serves every value:
//   - a value that SET assigns or that a VALUES list holds;
//   - a string or integer compared with =, <> or != to a column that unique
//     reports as a unique key, or listed in IN after one, in a statement with
//     no subquery or WITH.
//
// Other literals stay. A generic plan estimates col > $1, or col = $1 on a
// skewed column, at a fixed share of the table, which can be far below the
// rows the value touches. n counts the placeholders. ok is false when sql does
// not scan to the end, scans differently with backslash escapes in strings
// (standard_conforming_strings off), holds a bind of its own, or needs more
// than 65535 placeholders.
func Parameterize(sql string, unique func(column string) bool) (out string, n int, ok bool) {
	rules := Postgres.rules()
	toks, spans, ok := scanSpans(sql, rules)
	if !ok || !sameWithBackslashes(sql, rules, spans) {
		return "", 0, false
	}
	if unique != nil && hasSubquery(toks) {
		unique = nil
	}
	key := func(k int) bool {
		return unique != nil && k >= 0 && toks[k].isName() && unique(folded(sql, toks[k], spans[k]))
	}
	type frame struct{ list, afterList bool }
	stack := []frame{{}}
	setDepth := -1
	var b strings.Builder
	last := 0
	for i, t := range toks {
		top := &stack[len(stack)-1]
		if isPunct(t, "$") && i+1 < len(toks) && toks[i+1].Kind == Number {
			return "", 0, false
		}
		if i > 0 && (t.Kind == Literal || t.Kind == Number && wholeNumber(sql, spans[i])) {
			op, start := operatorBefore(sql, toks, spans, i)
			listed := top.list && isPunct(toks[i-1], "(", ",")
			assigned := op == "=" && setDepth == len(stack)
			keyed := (op == "=" || op == "<>" || op == "!=") && plainValue(t) && key(start-1)
			if listed || assigned || keyed {
				if n++; n > maxPlaceholders {
					return "", 0, false
				}
				b.WriteString(sql[last:spans[i][0]])
				if p := spans[i][0]; p > 0 && (isWordByte(sql[p-1]) || sql[p-1] == '$') {
					b.WriteByte(' ')
				}
				b.WriteString("$" + strconv.Itoa(n))
				last = spans[i][1]
				if last < len(sql) && (isWordByte(sql[last]) || sql[last] == '$') {
					b.WriteByte(' ')
				}
			}
		}
		switch {
		case t.isWord("set"):
			setDepth = len(stack)
		case setDepth == len(stack) && (t.isWord("where") || t.isWord("from") || t.isWord("returning")):
			setDepth = -1
		case isPunct(t, "(", "["):
			list := false
			if isPunct(t, "(") && i > 0 {
				switch p := toks[i-1]; {
				case p.isWord("values"):
					list = true
				case p.isWord("in"):
					col := i - 2
					if col >= 0 && toks[col].isWord("not") {
						col--
					}
					list = key(col)
				case isPunct(p, "(", ","):
					list = top.list || top.afterList && isPunct(p, ",")
				}
			}
			top.afterList = false
			stack = append(stack, frame{list: list})
			continue
		case isPunct(t, ")", "]"):
			if len(stack) > 1 {
				closed := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				stack[len(stack)-1].afterList = closed.list
				if setDepth > len(stack) {
					setDepth = -1
				}
				continue
			}
		case top.list && isPunct(toks[i-1], "(") && t.Kind == Word:
			switch t.Text {
			case "select", "with", "values", "table":
				top.list = false
			}
		}
		if !isPunct(t, ",") {
			top.afterList = false
		}
	}
	if n == 0 {
		return sql, 0, true
	}
	b.WriteString(sql[last:])
	return b.String(), n, true
}

// operatorBefore returns the operator that ends right before token i and the
// index of its first token. An operator is a run of punctuation with no space
// inside, so ->> and @> are not >.
func operatorBefore(sql string, toks []Token, spans [][2]int, i int) (string, int) {
	start := i
	for start > 0 && isOperator(toks[start-1]) && (start == i || spans[start-1][1] == spans[start][0]) {
		start--
	}
	if start == i {
		return "", i
	}
	return sql[spans[start][0]:spans[i-1][1]], start
}

// hasSubquery reports a second SELECT or a WITH: either can give a unique
// column's name to another value.
func hasSubquery(toks []Token) bool {
	selects := 0
	for _, t := range toks {
		if t.isWord("with") {
			return true
		}
		if t.isWord("select") {
			if selects++; selects > 1 {
				return true
			}
		}
	}
	return false
}

// wholeNumber reports whether the scanner's number is the whole literal:
// it stops before an exponent (1e3), a radix (0x1F) or a separator (1_000).
func wholeNumber(sql string, span [2]int) bool {
	return span[1] == len(sql) || !isWordByte(sql[span[1]])
}

// plainValue is a string, or an integer that PostgreSQL reads as int4 or int8.
func plainValue(t Token) bool {
	return t.Kind == Literal || t.Kind == Number && len(t.Text) <= 18 && strings.Trim(t.Text, "0123456789") == ""
}

// folded is a name as PostgreSQL stores it: a bare name with ASCII letters
// lowercased, a quoted one as written.
func folded(sql string, t Token, span [2]int) string {
	if t.Kind == Quoted {
		return t.Text
	}
	return asciiLower(sql[span[0]:span[1]])
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

func isOperator(t Token) bool {
	return t.Kind == Punct && len(t.Text) == 1 && strings.Contains("+-*/<>=~!@#%^&|`?", t.Text)
}

func isPunct(t Token, texts ...string) bool {
	return t.Kind == Punct && slices.Contains(texts, t.Text)
}

// PlanKey renders sql for a plan cache: each token as written, bare names
// with ASCII letters lowercased, one space apart. Comments are dropped,
// except an optimizer hint (/*+ ... */), which can change the plan. ok is
// false when sql does not scan to the end, or, for PostgreSQL and MySQL, when
// it scans differently with backslash escapes in strings.
func PlanKey(sql string, d Dialect) (string, bool) {
	rules := d.rules()
	toks, spans, ok := scanSpans(sql, rules)
	if !ok || (d == Postgres || d == MySQL) && !sameWithBackslashes(sql, rules, spans) {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(sql))
	end := 0
	for i, t := range toks {
		gap := sql[end:spans[i][0]]
		if i > 0 && !(gap == "" && isOperator(t) && isOperator(toks[i-1])) {
			b.WriteByte(' ')
		}
		if strings.Contains(gap, "/*+") {
			b.WriteString(strings.TrimSpace(gap))
			b.WriteByte(' ')
		}
		if t.Kind == Word {
			b.WriteString(asciiLower(sql[spans[i][0]:spans[i][1]]))
		} else {
			b.WriteString(sql[spans[i][0]:spans[i][1]])
		}
		end = spans[i][1]
	}
	if gap := sql[end:]; strings.Contains(gap, "/*+") {
		b.WriteByte(' ')
		b.WriteString(strings.TrimSpace(gap))
	}
	return b.String(), true
}

// sameWithBackslashes reports whether sql scans to the same tokens with the
// other reading of \ inside '...', a session setting the scanner cannot see.
func sameWithBackslashes(sql string, rules lexRules, spans [][2]int) bool {
	if !strings.Contains(sql, `\`) {
		return true
	}
	alt := rules
	alt.backslashInPlainString = !alt.backslashInPlainString
	_, altSpans, ok := scanSpans(sql, alt)
	return ok && slices.Equal(spans, altSpans)
}

// scanSpans returns the tokens and where each sits in src. ok is false when
// the scan is incomplete.
func scanSpans(src string, rules lexRules) ([]Token, [][2]int, bool) {
	toks, incomplete := scanWith(src, rules)
	if incomplete != "" {
		return nil, nil, false
	}
	spans := tokenSpans(src, rules)
	return toks, spans, len(spans) == len(toks)
}
