package lexer

import (
	"strings"
	"unicode/utf8"
)

// Kind classifies a token. The distinction that matters most is Word versus
// Quoted: `DELETE FROM "select"` names a table, and a scanner that folds a
// quoted identifier into a bare word loses the relation to the keyword table.
type Kind uint8

const (
	// Word is a bare identifier or keyword, lowercased.
	Word Kind = iota

	// Quoted is a quoted identifier with its quotes removed. It is NEVER a
	// keyword, however it spells.
	Quoted

	// Literal is a string constant. Its CONTENT is discarded and never
	// reaches Token.Text: a policy matching on statement data is a
	// different feature, and carrying values here would put them in an
	// audit record and a decision log.
	Literal

	// Number is a numeric constant.
	Number

	// Punct is one of ( ) , ; . * and other single-byte punctuation.
	Punct
)

// Token is one lexical unit.
type Token struct {
	Kind Kind

	// Text is lowercased for Word, verbatim for Quoted, empty for Literal.
	Text string
}

// isWord reports whether t is the given bare keyword. Quoted identifiers
// never match, which is the point of the Kind distinction.
func (t Token) isWord(s string) bool { return t.Kind == Word && t.Text == s }

// isName reports whether t can name a relation.
func (t Token) isName() bool {
	switch t.Kind {
	case Quoted:
		return t.Text != ""
	case Word:
		return t.Text != ""
	}
	return false
}

// scanner walks the input once, emitting typed tokens.
//
// One pass, deliberately. The previous implementation stripped noise into a
// new string and then tokenized that string, which erased quoting kind before
// anything could use it and left the tokenizer reconstructing nesting from
// parentheses that had leaked out of an unstripped dollar-quote. Everything
// the analysis needs is decided here, once, while the bytes still say what
// they are.
type scanner struct {
	src   string
	pos   int
	rules lexRules

	// execDepth counts open `/*! ... */` executable comments. Their bodies
	// are live SQL, so the scanner stays inside the statement and only owes
	// the closing delimiter; see skipComment.
	execDepth int

	// incomplete records the first construct the scanner could not finish.
	// It travels to Analysis.Complete, and a caller fails closed on it.
	incomplete string
}

// scan tokenizes src. The second result is empty when the whole input was
// understood, and otherwise names what defeated it.
func scan(src string, d Dialect) ([]Token, string) {
	return scanWith(src, d.rules())
}

// scanWith tokenizes src under an explicit rule set.
//
// Split out from scan because MySQL is read twice, once per backslash
// convention: the mode is a per-session setting the classifier cannot see,
// and the two readings disagree about where a literal ends. See
// analyzeMySQL.
func scanWith(src string, rules lexRules) ([]Token, string) {
	s := &scanner{src: src, rules: rules}
	// One token per ~6 bytes is close for SQL and avoids most regrowth.
	out := make([]Token, 0, len(src)/6+8)
	for {
		tok, ok := s.next()
		if !ok {
			// An executable comment left open ran off the end of the
			// input. Its body was scanned as live SQL, so the tokens are
			// real, but the statement is truncated and what follows the
			// missing close is unknown. Fail closed rather than report a
			// clean scan of half a statement.
			if s.execDepth > 0 {
				s.fail("unterminated executable comment")
			}
			return out, s.incomplete
		}
		out = append(out, tok)
	}
}

func (s *scanner) fail(what string) {
	if s.incomplete == "" {
		s.incomplete = what
	}
}

func (s *scanner) peek(off int) byte {
	if s.pos+off >= len(s.src) {
		return 0
	}
	return s.src[s.pos+off]
}

// next returns the following token, or ok=false at end of input.
func (s *scanner) next() (Token, bool) {
	for {
		s.skipSpace()
		if s.pos >= len(s.src) {
			return Token{}, false
		}
		if s.skipComment() {
			continue
		}
		break
	}

	c := s.src[s.pos]
	switch {
	case c == '\'':
		return s.stringLit('\'', false), true

	case s.rules.escapeString && (c == 'E' || c == 'e') && s.peek(1) == '\'':
		s.pos++
		return s.escapedString(), true

	case s.rules.nationalString && (c == 'N' || c == 'n') && s.peek(1) == '\'':
		s.pos++
		return s.plainString('\''), true

	// Gated, and ahead of the word case below on purpose: r/R/b/B are
	// ordinary identifier bytes everywhere else, and even under GoogleSQL
	// `rb` followed by anything but a quote is a plain word. The prefix
	// decides whether the literal is RAW, which decides where it ENDS —
	// see lexRules.rawString for what riding over that costs.
	case s.rules.rawString && (c == 'r' || c == 'R' || c == 'b' || c == 'B'):
		if n, raw, ok := s.stringPrefix(); ok {
			s.pos += n
			return s.stringLit(s.src[s.pos], raw), true
		}
		return s.word(), true

	// Ahead of the identifier case below: under GoogleSQL '"' delimits a
	// STRING, interchangeable with '\'', and identifiers are backticked.
	case s.rules.doubleQuoteString && c == '"':
		return s.stringLit('"', false), true

	case c == '"':
		return s.delimitedIdent('"', '"'), true

	case s.rules.unicodeIdent && (c == 'U' || c == 'u') && s.peek(1) == '&' && s.peek(2) == '"':
		s.pos += 2
		return s.delimitedIdent('"', '"'), true

	case s.rules.bracketIdent && c == '[':
		return s.delimitedIdent('[', ']'), true

	// Gated, and it has to be: a backtick is not punctuation the other
	// engines merely dislike, it is a byte they reject, so opening an
	// identifier on it anywhere else would turn a syntax error into a
	// confident relation name. delimitedIdent already honours the
	// doubled-close escape, which is exactly MySQL's ``a``b`` spelling.
	case s.rules.backtickIdent && c == '`':
		return s.delimitedIdent('`', '`'), true

	case s.rules.dollarQuote && c == '$':
		if tok, ok := s.dollarString(); ok {
			return tok, true
		}
		// Not a dollar tag: a $1 parameter placeholder or a stray $.
		s.pos++
		return Token{Kind: Punct, Text: "$"}, true

	case c >= '0' && c <= '9':
		return s.number(), true

	case isWordByte(c):
		return s.word(), true

	default:
		s.pos++
		return Token{Kind: Punct, Text: string(c)}, true
	}
}

func (s *scanner) skipSpace() {
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case ' ', '\t', '\r', '\n', '\f', '\v':
			s.pos++
		default:
			return
		}
	}
}

// skipComment consumes one comment and reports whether it did.
func (s *scanner) skipComment() bool {
	// MySQL requires whitespace or a control character after the second
	// dash; `1--2` is one minus applied twice, not a comment. Applying the
	// Postgres rule there hides everything after it on the line, so
	// `SELECT 1--2; DELETE FROM t` would come back as a lone select with
	// the second statement never analyzed.
	if s.peek(0) == '-' && s.peek(1) == '-' && (!s.rules.dashCommentNeedsSpace || s.dashOpensComment()) {
		for s.pos < len(s.src) && s.src[s.pos] != '\n' {
			s.pos++
		}
		return true
	}
	// MySQL's other line comment. Gated because '#' is a live operator
	// elsewhere — PostgreSQL spells XOR and several geometric operators
	// with it — so consuming to end of line there would delete real SQL.
	if s.rules.hashComment && s.peek(0) == '#' {
		for s.pos < len(s.src) && s.src[s.pos] != '\n' {
			s.pos++
		}
		return true
	}

	// The close of an executable comment opened below. Consumed as
	// whitespace, because its BODY was live SQL and the tokens are already
	// emitted; leaving it would surface as stray '*' and '/' punctuation.
	if s.execDepth > 0 && s.peek(0) == '*' && s.peek(1) == '/' {
		s.pos += 2
		s.execDepth--
		return true
	}

	if s.peek(0) != '/' || s.peek(1) != '*' {
		return false
	}

	// `/*! ... */` is MySQL's executable comment, and its contents RUN.
	// `/*! DROP TABLE t */` drops the table; a scanner discarding it as a
	// comment reports no verb, so a rule refusing `drop` matches nothing
	// and the statement is forwarded. Verified against MySQL 8.4: the
	// table disappeared through a relay configured to refuse it.
	//
	// `/*!50000 ... */` runs only on a server at or above that version.
	// This scanner does not know the server's version and must not guess:
	// treating the body as live SQL costs a false denial on an older
	// server, treating it as a comment costs a silent bypass on a current
	// one. Only one of those is survivable.
	//
	// The body is scanned in place rather than recursively: the tokens
	// belong to the surrounding statement, which is exactly why they
	// matter. execDepth records that a close is still owed.
	if s.rules.executableComment && s.peek(2) == '!' {
		s.pos += 3
		// An optional 5- or 6-digit version prefix, which is part of the
		// marker and not of the statement.
		for s.pos < len(s.src) && s.src[s.pos] >= '0' && s.src[s.pos] <= '9' {
			s.pos++
		}
		s.execDepth++
		return true
	}
	// PostgreSQL and T-SQL NEST block comments, unlike the standard. A
	// scanner stopping at the first close reads the tail of an outer
	// comment as live SQL: `/* a /* b */ DELETE FROM t */` is entirely
	// comment there, and stopping early reports a delete. MySQL does not
	// nest, where the same text ends at the first close and the delete is
	// real; nestedBlockComment picks the reading per engine.
	depth := 0
	for s.pos < len(s.src) {
		switch {
		case s.peek(0) == '/' && s.peek(1) == '*':
			depth++
			s.pos += 2
			if !s.rules.nestedBlockComment {
				// Flat engines: the first close ends it regardless.
				depth = 1
			}
		case s.peek(0) == '*' && s.peek(1) == '/':
			depth--
			s.pos += 2
			if depth == 0 {
				return true
			}
		default:
			s.pos++
		}
	}
	s.fail("unterminated block comment")
	return true
}

// dashOpensComment reports whether the `--` at the cursor is MySQL's line
// comment rather than two minus signs. The server's test is a whitespace or
// control character after the second dash; end of input also ends the line,
// so nothing can follow the dashes there either.
func (s *scanner) dashOpensComment() bool {
	if s.pos+2 >= len(s.src) {
		return true
	}
	c := s.src[s.pos+2]
	return c <= ' '
}

// plainString consumes a quoted literal with the doubled-close escape,
// honouring backslashInPlainString. q is the delimiter: the single quote
// everywhere, also '"' under GoogleSQL, where both quotes delimit strings.
//
// The doubled-close escape stays on for GoogleSQL even though ZetaSQL has
// no such escape — there a doubled quote is an empty literal beside another
// one. The two readings pair the quotes differently but always agree on where the
// LITERAL REGION ends: doubling consumes close+reopen where ZetaSQL closes
// one string and opens the next, so neither reading leaks live SQL into a
// literal or literal content into SQL.
func (s *scanner) plainString(q byte) Token {
	s.pos++ // opening quote
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if s.rules.backslashInPlainString && c == '\\' && s.pos+1 < len(s.src) {
			s.pos += 2
			continue
		}
		if c == q {
			if s.peek(1) == q {
				s.pos += 2
				continue
			}
			s.pos++
			return Token{Kind: Literal}
		}
		s.pos++
	}
	s.fail("unterminated string literal")
	return Token{Kind: Literal}
}

// stringLit consumes one string literal delimited by q, routing to the
// triple-quoted and raw forms when the rules enable them. The cursor sits ON
// the opening quote; a raw/byte prefix was already consumed by the caller.
//
// Triple detection comes first because it is a longest-match decision:
// ZetaSQL reads three quotes as the opener of a triple-quoted string, never
// as a quote-and-empty-literal, and a scanner choosing the short reading closes
// the literal two quotes early and scans its BODY as live SQL.
func (s *scanner) stringLit(q byte, raw bool) Token {
	if s.rules.tripleQuoteString && s.peek(1) == q && s.peek(2) == q {
		return s.tripleString(q, raw)
	}
	if raw {
		return s.rawStringLit(q)
	}
	return s.plainString(q)
}

// stringPrefix reports the length of the r/R/b/B string prefix at the
// cursor and whether it makes the literal raw. ok is false when the letters
// are not a valid prefix glued to a quote, in which case they are an
// ordinary word.
//
// At most one r and one b, in either order and any case — rr'...' is an
// identifier followed by a string, and accepting it here would eat a byte
// of somebody's column name.
func (s *scanner) stringPrefix() (n int, raw, ok bool) {
	for n < 2 {
		switch s.peek(n) {
		case 'r', 'R':
			if raw {
				return 0, false, false
			}
			raw = true
		case 'b', 'B':
			if ok { // ok doubles as "b seen" until the return below
				return 0, false, false
			}
			ok = true
		case '\'', '"':
			return n, raw, n > 0
		default:
			return 0, false, false
		}
		n++
	}
	if c := s.peek(n); c == '\'' || c == '"' {
		return n, raw, true
	}
	return 0, false, false
}

// rawStringLit consumes r'...' or r"...", where a backslash is an ordinary
// byte. The FIRST closing quote ends the literal, always.
//
// This is the whole point of the raw form's rules row: under the escaping
// read, r'\' consumes the terminator, the literal swallows everything to
// the next quote, and a statement between them is never analyzed. The
// converse misread — closing early on a quote ZetaSQL would have kept —
// only splits data into fragments the analysis reads separately, which
// denies nothing by itself.
func (s *scanner) rawStringLit(q byte) Token {
	s.pos++ // opening quote
	for s.pos < len(s.src) {
		if s.src[s.pos] == q {
			s.pos++
			return Token{Kind: Literal}
		}
		s.pos++
	}
	s.fail("unterminated raw string")
	return Token{Kind: Literal}
}

// tripleString consumes a triple-quoted literal, opened and closed by three
// q bytes. The body may contain bare quotes, and in the non-raw form a
// backslash escapes the next byte, so an escaped quote never begins the
// close.
func (s *scanner) tripleString(q byte, raw bool) Token {
	s.pos += 3 // opening delimiter
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if !raw && c == '\\' && s.pos+1 < len(s.src) {
			s.pos += 2
			continue
		}
		if c == q && s.peek(1) == q && s.peek(2) == q {
			s.pos += 3
			return Token{Kind: Literal}
		}
		s.pos++
	}
	s.fail("unterminated triple-quoted string")
	return Token{Kind: Literal}
}

// escapedString consumes E'...', where a backslash escapes the next byte in
// every server configuration.
func (s *scanner) escapedString() Token {
	s.pos++ // opening quote
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case '\\':
			s.pos += 2
			continue
		case '\'':
			if s.peek(1) == '\'' {
				s.pos += 2
				continue
			}
			s.pos++
			return Token{Kind: Literal}
		}
		s.pos++
	}
	s.fail("unterminated escape string")
	return Token{Kind: Literal}
}

// delimitedIdent consumes a quoted identifier, honouring the doubled-close
// escape: "a""b", [a]]b]. Under backtickBackslashEscape a backtick
// identifier instead escapes with a backslash and a doubled backtick is
// two identifiers, which is GoogleSQL's spelling; see the rules row.
func (s *scanner) delimitedIdent(open, close byte) Token {
	backslashEscape := open == '`' && s.rules.backtickBackslashEscape
	s.pos++ // opening delimiter
	var b strings.Builder
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if backslashEscape && c == '\\' && s.pos+1 < len(s.src) {
			// GoogleSQL quoted identifiers share the string-literal
			// escape table, so `\x63ustomers` and `customers` spell the
			// SAME relation. Decode every escape to its actual bytes:
			// keeping the byte after the backslash verbatim would hand
			// policy a name like "x63ustomers" that nothing was written
			// against, which is a bypass spelled with two extra bytes.
			s.identEscape(&b)
			continue
		}
		if c == close {
			if !backslashEscape && s.peek(1) == close {
				b.WriteByte(close)
				s.pos += 2
				continue
			}
			s.pos++
			return Token{Kind: Quoted, Text: b.String()}
		}
		b.WriteByte(c)
		s.pos++
	}
	s.fail("unterminated quoted identifier")
	return Token{Kind: Quoted, Text: b.String()}
}

// identEscape decodes one GoogleSQL escape sequence — the backslash sits at
// s.pos and at least one byte follows — appending the decoded bytes to b
// and advancing past the sequence. The table is GoogleSQL's string-literal
// one: the named escapes, \ooo octal (three digits, at most \377), \xhh,
// and the \uhhhh / \Uhhhhhhhh code points. An escape the dialect does not
// define fails the scan instead of guessing: an invented byte here becomes
// a relation name policy silently stops matching.
func (s *scanner) identEscape(b *strings.Builder) {
	c := s.src[s.pos+1]
	switch c {
	case 'a':
		b.WriteByte('\a')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'v':
		b.WriteByte('\v')
	case '\\', '?', '"', '\'', '`':
		b.WriteByte(c)
	case 'x', 'X':
		if v, ok := s.hexDigits(s.pos+2, 2); ok {
			b.WriteByte(byte(v))
			s.pos += 4
			return
		}
		s.fail("invalid hex escape in quoted identifier")
		s.pos += 2
		return
	case 'u':
		if v, ok := s.hexDigits(s.pos+2, 4); ok && utf8.ValidRune(rune(v)) {
			b.WriteRune(rune(v))
			s.pos += 6
			return
		}
		s.fail("invalid unicode escape in quoted identifier")
		s.pos += 2
		return
	case 'U':
		if v, ok := s.hexDigits(s.pos+2, 8); ok && v <= utf8.MaxRune && utf8.ValidRune(rune(v)) {
			b.WriteRune(rune(v))
			s.pos += 10
			return
		}
		s.fail("invalid unicode escape in quoted identifier")
		s.pos += 2
		return
	case '0', '1', '2', '3', '4', '5', '6', '7':
		v := 0
		for i := 1; i <= 3; i++ {
			d := s.peek(i)
			if d < '0' || d > '7' {
				s.fail("invalid octal escape in quoted identifier")
				s.pos += 2
				return
			}
			v = v<<3 | int(d-'0')
		}
		// Three digits reach \777; GoogleSQL stops the escape at one
		// byte, so \400 and up is not a longer number, it is invalid.
		if v > 0xFF {
			s.fail("invalid octal escape in quoted identifier")
			s.pos += 2
			return
		}
		b.WriteByte(byte(v))
		s.pos += 4
		return
	default:
		s.fail("invalid escape in quoted identifier")
		s.pos += 2
		return
	}
	s.pos += 2
}

// hexDigits reads exactly n hex digits starting at pos, reporting failure
// when the input ends first or a byte is not a hex digit.
func (s *scanner) hexDigits(pos, n int) (uint32, bool) {
	if pos+n > len(s.src) {
		return 0, false
	}
	var v uint32
	for i := 0; i < n; i++ {
		d := hexVal(s.src[pos+i])
		if d < 0 {
			return 0, false
		}
		v = v<<4 | uint32(d)
	}
	return v, true
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// dollarString consumes $$...$$ or $tag$...$tag$.
//
// Returns ok=false when the '$' does not open a tag, which covers the $1
// parameter placeholders every driver emits.
func (s *scanner) dollarString() (Token, bool) {
	end := s.pos + 1
	for end < len(s.src) && isTagByte(s.src[end]) {
		end++
	}
	if end >= len(s.src) || s.src[end] != '$' {
		return Token{}, false
	}
	tag := s.src[s.pos : end+1] // includes both '$'
	rest := s.src[end+1:]
	if i := strings.Index(rest, tag); i >= 0 {
		s.pos = end + 1 + i + len(tag)
		return Token{Kind: Literal}, true
	}
	s.fail("unterminated dollar-quoted string")
	s.pos = len(s.src)
	return Token{Kind: Literal}, true
}

func (s *scanner) number() Token {
	start := s.pos
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if (c >= '0' && c <= '9') || c == '.' {
			s.pos++
			continue
		}
		break
	}
	return Token{Kind: Number, Text: s.src[start:s.pos]}
}

func (s *scanner) word() Token {
	start := s.pos
	for s.pos < len(s.src) && isWordByte(s.src[s.pos]) {
		s.pos++
	}
	return Token{Kind: Word, Text: strings.ToLower(s.src[start:s.pos])}
}

// isWordByte reports whether c can appear in a bare identifier.
//
// '$' is excluded even though PostgreSQL allows it in identifiers after the
// first character. Including it would let a dollar-quote tag that the scanner
// declined to open glue itself onto the preceding word, and a relation named
// with an embedded '$' is rarer than a driver emitting $1.
func isWordByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c >= 0x80 // any UTF-8 continuation or lead byte
}

// isTagByte reports whether c can appear in a dollar-quote tag.
func isTagByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		c >= 0x80
}

// Split breaks a multi-statement query string on its top-level semicolons.
//
// It exists so a codec does not carry a second, weaker lexer. The previous
// arrangement had one: it knew `'...'` and `$$...$$` but not `E'...'`, so
// `SELECT E'a\';b'; DELETE FROM customers` split at the semicolon INSIDE the
// literal and produced two fragments, neither of which parsed. Policy then saw
// no delete at all. Any splitter has to make exactly the lexical decisions
// this scanner already makes, so it makes them here once.
//
// A semicolon inside a literal, a comment, a quoted identifier or a
// dollar-quoted body is not a separator. Empty statements are dropped.
func Split(sql string, d Dialect) []string {
	if d == MySQL {
		return splitMySQL(sql)
	}
	return splitWith(sql, d.rules())
}

// splitMySQL returns the FINER of the two backslash readings.
//
// Whether `\` escapes inside '...' decides where a literal ends, and so
// where a statement ends. Under NO_BACKSLASH_ESCAPES,
// `SELECT 'a\'; DELETE FROM orders; -- '` is two statements and the DELETE
// runs; under the default it is one, and the delete is literal text.
//
// The session mode is invisible here, so the split that yields MORE
// statements wins. Every fragment then reaches the classifier and the
// policy: a DELETE hidden by the other reading is evaluated rather than
// swallowed. The cost of being wrong is a harmless fragment classified
// separately, which denies nothing on its own.
func splitMySQL(sql string) []string {
	escaped := MySQL.rules()
	literal := escaped
	literal.backslashInPlainString = false

	a := splitWith(sql, escaped)
	if b := splitWith(sql, literal); len(b) > len(a) {
		return b
	}
	return a
}

// splitWith breaks sql on top-level semicolons under an explicit rule set.
func splitWith(sql string, rules lexRules) []string {
	s := &scanner{src: sql, rules: rules}
	var out []string
	start := 0
	for {
		tok, ok := s.next()
		if !ok {
			break
		}
		if tok.Kind != Punct || tok.Text != ";" {
			continue
		}
		// s.pos sits one byte past the semicolon the scanner consumed.
		if t := strings.TrimSpace(sql[start : s.pos-1]); t != "" {
			out = append(out, t)
		}
		start = s.pos
	}
	if t := strings.TrimSpace(sql[start:]); t != "" {
		out = append(out, t)
	}
	return out
}
