package lexer

import "strconv"

// ok is false when sql does not scan to the end.
func MaxParam(sql string, d Dialect) (n int, ok bool) {
	toks, incomplete := scan(sql, d)
	if incomplete != "" {
		return 0, false
	}
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].Kind != Punct || toks[i].Text != "$" || toks[i+1].Kind != Number {
			continue
		}
		if v, err := strconv.Atoi(toks[i+1].Text); err == nil && v > n {
			n = v
		}
	}
	return n, true
}
