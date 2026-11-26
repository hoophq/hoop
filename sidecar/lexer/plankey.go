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
//   - a value that UPDATE ... SET or ON CONFLICT DO UPDATE SET assigns, or
//     that an INSERT's own VALUES list holds (a VALUES used as a table can
//     change the plan);
//   - a string or integer compared with =, <> or != to a column that unique
//     reports as a unique key, or listed in IN after one, in a SELECT, UPDATE
//     or DELETE that reads one table and nothing else (keySource).
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
	names, one := keySource(sql, toks, spans)
	if !one {
		unique = nil
	}
	key := func(k int) bool {
		if unique == nil || k < 0 || !toks[k].isName() || !unique(folded(sql, toks[k], spans[k])) {
			return false
		}
		first := k
		if k > 0 && isPunct(toks[k-1], ".") {
			if k < 2 || !toks[k-2].isName() || !slices.Contains(names, folded(sql, toks[k-2], spans[k-2])) {
				return false
			}
			first = k - 2
		}
		// In w - id the value meets an expression, which can have statistics;
		// in x::id, id is a type.
		return first == 0 || !isOperator(toks[first-1]) && !isPunct(toks[first-1], ".", ":")
	}
	insert := len(toks) > 0 && toks[0].isWord("insert")
	update := len(toks) > 0 && toks[0].isWord("update")
	type frame struct{ list, afterList bool }
	stack := []frame{{}}
	setDepth := -1
	sawSet := false
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
		case t.isWord("set") && len(stack) == 1 && (update && !sawSet || i > 0 && toks[i-1].isWord("update")):
			setDepth, sawSet = 1, true
		case setDepth == len(stack) && (t.isWord("where") || t.isWord("from") || t.isWord("returning")):
			setDepth = -1
		case isPunct(t, "(", "["):
			list := false
			if isPunct(t, "(") && i > 0 {
				switch p := toks[i-1]; {
				case p.isWord("values"):
					list = insert && len(stack) == 1
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

// keySource returns the names a column of the statement's table can carry: the
// table's name and its alias. ok is false unless the statement is a SELECT,
// UPDATE or DELETE that reads that one table and nothing else, since a join,
// subquery, VALUES or function in FROM can give a key's name to another value.
func keySource(sql string, toks []Token, spans [][2]int) (names []string, ok bool) {
	if len(toks) == 0 {
		return nil, false
	}
	verb := toks[0]
	if !verb.isWord("select") && !verb.isWord("update") && !verb.isWord("delete") {
		return nil, false
	}
	depth, from := 0, -1
	for i, t := range toks {
		switch {
		case isPunct(t, "("):
			depth++
		case isPunct(t, ")"):
			depth--
		case i > 0 && t.Kind == Word && slices.Contains(otherSources, t.Text):
			return nil, false
		case t.isWord("from") && depth == 0 && !(i > 1 && toks[i-1].isWord("distinct") && (toks[i-2].isWord("is") || toks[i-2].isWord("not"))):
			if from >= 0 {
				return nil, false
			}
			from = i
		}
	}
	i := from + 1
	switch {
	case verb.isWord("select") && from < 0, verb.isWord("delete") && from != 1, verb.isWord("update") && from >= 0:
		return nil, false
	case verb.isWord("update"):
		i = 1
	}
	if i < len(toks) && toks[i].isWord("only") {
		i++
	}
	if i >= len(toks) || !toks[i].isName() {
		return nil, false
	}
	table := folded(sql, toks[i], spans[i])
	if i += 1; i+1 < len(toks) && isPunct(toks[i], ".") && toks[i+1].isName() {
		table = folded(sql, toks[i+1], spans[i+1])
		i += 2
	}
	names = []string{table}
	if i < len(toks) && toks[i].isWord("as") {
		i++
	}
	if i < len(toks) && toks[i].isName() && !(toks[i].Kind == Word && slices.Contains(clauses, toks[i].Text)) {
		names = append(names, folded(sql, toks[i], spans[i]))
		i++
	}
	if verb.isWord("update") {
		return names, i < len(toks) && toks[i].isWord("set")
	}
	return names, i == len(toks) || isPunct(toks[i], ";") || toks[i].Kind == Word && slices.Contains(clauses, toks[i].Text)
}

var (
	otherSources = []string{"select", "values", "with", "join", "using", "lateral", "table"}
	clauses      = []string{"where", "group", "having", "window", "order", "limit", "offset", "fetch", "for", "returning", "set"}
)

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
