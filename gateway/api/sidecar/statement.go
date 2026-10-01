package apisidecar

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// binaryNoticePrefix opens a binary statement's notice line. Text that starts
// with it takes the binary form, so a notice can only come from displayStatement.
const binaryNoticePrefix = "[binary statement, "

const hexDigits = "0123456789abcdef"

// displayStatement renders raw bytes as JSONB-safe text for a reviewer: printable
// text as is, else a notice, then \xNN per non-printable byte and \\ per backslash.
func displayStatement(raw []byte) string {
	if isPrintableText(raw) {
		return string(raw)
	}
	var b strings.Builder
	b.Grow(96 + 4*len(raw))
	b.WriteString(binaryNoticePrefix)
	b.WriteString(strconv.Itoa(len(raw)))
	b.WriteString(` bytes: a byte that is not printable shows as \xNN, a backslash as \\]`)
	b.WriteByte('\n')
	for len(raw) > 0 {
		r, size := utf8.DecodeRune(raw)
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		// A combining mark is escaped here: after an escape it would draw over it.
		case r == '\n' || (visible(r) && !unicode.In(r, unicode.Mn, unicode.Me) &&
			!(r == utf8.RuneError && size == 1)):
			b.Write(raw[:size])
		default:
			for _, c := range raw[:size] {
				b.WriteString(`\x`)
				b.WriteByte(hexDigits[c>>4])
				b.WriteByte(hexDigits[c&0x0f])
			}
		}
		raw = raw[size:]
	}
	return b.String()
}

// isPrintableText is strict: text a reviewer cannot see (a zero-width space, a
// bidi override, a lone \r) takes the binary form. So does text like a notice.
func isPrintableText(raw []byte) bool {
	if bytes.HasPrefix(bytes.TrimLeft(raw, " \t\r\n"), []byte(binaryNoticePrefix)) || !utf8.Valid(raw) {
		return false
	}
	s := string(raw)
	for i, r := range s {
		switch {
		case r == '\n' || r == '\t':
		case r == '\r':
			if !strings.HasPrefix(s[i+1:], "\n") {
				return false
			}
		case !visible(r):
			return false
		}
	}
	return true
}

// visible reports a rune a reviewer sees as itself. strconv.IsPrint accepts
// blank ones: Hangul fillers, variation selectors, U+2800 and similar.
func visible(r rune) bool {
	return strconv.IsPrint(r) && r != '\u2800' &&
		!unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) &&
		!unicode.Is(unicode.Variation_Selector, r)
}
