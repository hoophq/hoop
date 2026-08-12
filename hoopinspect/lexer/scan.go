package lexer

import "strings"

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

	// incomplete records the first construct the scanner could not finish.
	// It travels to Analysis.Complete, and a caller fails closed on it.
	incomplete string
}

// scan tokenizes src. The second result is empty when the whole input was
// understood, and otherwise names what defeated it.
func scan(src string, d Dialect) ([]Token, string) {
	s := &scanner{src: src, rules: d.rules()}
	// One token per ~6 bytes is close for SQL and avoids most regrowth.
	out := make([]Token, 0, len(src)/6+8)
	for {
		tok, ok := s.next()
		if !ok {
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
		return s.plainString(), true

	case s.rules.escapeString && (c == 'E' || c == 'e') && s.peek(1) == '\'':
		s.pos++
		return s.escapedString(), true

	case s.rules.nationalString && (c == 'N' || c == 'n') && s.peek(1) == '\'':
		s.pos++
		return s.plainString(), true

	case c == '"':
		return s.delimitedIdent('"', '"'), true

	case s.rules.unicodeIdent && (c == 'U' || c == 'u') && s.peek(1) == '&' && s.peek(2) == '"':
		s.pos += 2
		return s.delimitedIdent('"', '"'), true

	case s.rules.bracketIdent && c == '[':
		return s.delimitedIdent('[', ']'), true

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
	if s.peek(0) == '-' && s.peek(1) == '-' {
		for s.pos < len(s.src) && s.src[s.pos] != '\n' {
			s.pos++
		}
		return true
	}
	if s.peek(0) != '/' || s.peek(1) != '*' {
		return false
	}
	// Both supported engines NEST block comments, unlike the standard. A
	// scanner stopping at the first close reads the tail of an outer
	// comment as live SQL: `/* a /* b */ DELETE FROM t */` is entirely
	// comment, and stopping early reports a delete.
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

// plainString consumes '...' with the doubled '' escape.
func (s *scanner) plainString() Token {
	s.pos++ // opening quote
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if s.rules.backslashInPlainString && c == '\\' && s.pos+1 < len(s.src) {
			s.pos += 2
			continue
		}
		if c == '\'' {
			if s.peek(1) == '\'' {
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
// escape: "a""b", [a]]b].
func (s *scanner) delimitedIdent(open, close byte) Token {
	s.pos++ // opening delimiter
	var b strings.Builder
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		if c == close {
			if s.peek(1) == close {
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
	s := &scanner{src: sql, rules: d.rules()}
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
