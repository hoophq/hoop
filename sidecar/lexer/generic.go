package lexer

import (
	"strconv"
	"strings"
)

const maxPlaceholders = 65535

// Parameterize reads sql as PostgreSQL and returns it with $n in place of each
// literal that is compared to a value (=, <>, !=, <, <=, >, >=), bounds a
// BETWEEN, or is an item of an IN or VALUES list. Every other literal stays:
// LIMIT, LIKE, typed literals, function arguments and arrays change the plan.
// n counts the placeholders. ok is false when sql does not scan to the end,
// holds a bind of its own, or needs more than 65535 placeholders.
func Parameterize(sql string) (out string, n int, ok bool) {
	toks, spans, ok := scanSpans(sql, Postgres.rules())
	if !ok {
		return "", 0, false
	}
	type paren struct{ list, afterList bool }
	stack := []paren{{}}
	betweenAt := -1
	afterBetween := false
	var b strings.Builder
	last := 0
	for i, t := range toks {
		top := &stack[len(stack)-1]
		if t.Kind == Punct && t.Text == "$" && i+1 < len(toks) && toks[i+1].Kind == Number {
			return "", 0, false
		}
		if (t.Kind == Literal || t.Kind == Number) && i > 0 &&
			(afterBetween || top.list && isPunct(toks[i-1], "(", ",") || comparison(sql, toks, spans, i-1)) {
			if n++; n > maxPlaceholders {
				return "", 0, false
			}
			start := spans[i][0]
			b.WriteString(sql[last:start])
			if start > 0 && (isWordByte(sql[start-1]) || sql[start-1] == '$') {
				b.WriteByte(' ')
			}
			b.WriteString("$" + strconv.Itoa(n))
			last = spans[i][1]
			if last < len(sql) && (isWordByte(sql[last]) || sql[last] == '$') {
				b.WriteByte(' ')
			}
		}
		afterBetween = false
		switch {
		case t.isWord("between"):
			betweenAt, afterBetween = len(stack), true
		case t.isWord("and") && betweenAt == len(stack):
			betweenAt, afterBetween = -1, true
		case isPunct(t, "("):
			list := i > 0 && (toks[i-1].isWord("in") || toks[i-1].isWord("values") ||
				top.list && isPunct(toks[i-1], "(", ",") || top.afterList && isPunct(toks[i-1], ","))
			top.afterList = false
			stack = append(stack, paren{list: list})
			continue
		case isPunct(t, ")"):
			if len(stack) > 1 {
				closed := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				stack[len(stack)-1].afterList = closed.list
				if betweenAt > len(stack) {
					betweenAt = -1
				}
				continue
			}
		case top.list && i > 0 && isPunct(toks[i-1], "(") && t.Kind == Word:
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

// comparison reports whether the operator that ends at token k compares two
// values. An operator is a run of punctuation with no space inside, so ->>
// and @> are not >.
func comparison(sql string, toks []Token, spans [][2]int, k int) bool {
	if !isOperator(toks[k]) {
		return false
	}
	start := k
	for start > 0 && isOperator(toks[start-1]) && spans[start-1][1] == spans[start][0] {
		start--
	}
	switch sql[spans[start][0]:spans[k][1]] {
	case "=", "<>", "!=", "<", "<=", ">", ">=":
		return true
	}
	return false
}

func isOperator(t Token) bool {
	return t.Kind == Punct && len(t.Text) == 1 && strings.Contains("+-*/<>=~!@#%^&|`?", t.Text)
}

func isPunct(t Token, any ...string) bool {
	if t.Kind != Punct {
		return false
	}
	for _, s := range any {
		if t.Text == s {
			return true
		}
	}
	return false
}

// PlanKey renders sql for a plan cache: each token as written, keywords and
// bare names lowercased, one space apart. Comments are dropped, except an
// optimizer hint (/*+ ... */), which can change the plan. ok is false when
// sql does not scan to the end, or, for MySQL, when the two backslash
// readings disagree (see analyzeMySQL).
func PlanKey(sql string, d Dialect) (string, bool) {
	key, ok := planKeyWith(sql, d.rules())
	if !ok || d != MySQL {
		return key, ok
	}
	literal := d.rules()
	literal.backslashInPlainString = false
	if alt, ok := planKeyWith(sql, literal); !ok || alt != key {
		return "", false
	}
	return key, true
}

func planKeyWith(sql string, rules lexRules) (string, bool) {
	toks, spans, ok := scanSpans(sql, rules)
	if !ok {
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
			b.WriteString(t.Text)
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
