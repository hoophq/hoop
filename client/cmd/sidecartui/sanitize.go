package sidecartui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// clean makes untrusted text safe to draw: a client chooses its statement's
// bytes, and the approval dialog shows them to the person who releases it.
//
// An ESC would start a terminal sequence (move the cursor, erase a line,
// recolor) and let a client hide or rewrite part of its own statement on the
// screen where it is approved; a bidi override would reorder it the same way.
// Every control character but newline and tab, every C1 control, every bidi
// control and every invalid byte is written as a visible escape instead, so
// the person sees that something was there.
func clean(s string) string {
	if !needsClean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == 0x1b:
			b.WriteString("␛")
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			fmt.Fprintf(&b, `\x%02x`, r)
		case isBidiControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

func needsClean(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || (r < 0x20 && r != '\n' && r != '\t') ||
			r == 0x7f || (r >= 0x80 && r <= 0x9f) || isBidiControl(r) {
			return true
		}
		i += size
	}
	return false
}

// isBidiControl reports the characters that reorder the text around them
// (the "Trojan Source" set): marks, embeddings, overrides and isolates.
func isBidiControl(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

func cleanAll(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = clean(s)
	}
	return out
}

func cleanMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[clean(k)] = clean(v)
	}
	return out
}

// cleanEvent returns the event with every string the screen can draw made
// safe. The event the daemon recorded is not touched: this is a copy for the
// screen, and the saved trail keeps the bytes as they were.
func cleanEvent(ev audit.Event) audit.Event {
	ev.Principal = clean(ev.Principal)
	ev.Connection = clean(ev.Connection)
	ev.Operation = inspect.Operation(clean(string(ev.Operation)))
	ev.Statement = clean(ev.Statement)
	ev.Tables = cleanAll(ev.Tables)
	ev.Rule = clean(ev.Rule)
	ev.Message = clean(ev.Message)
	ev.MaskedEntities = cleanAll(ev.MaskedEntities)
	ev.Error = clean(ev.Error)
	ev.Metadata = cleanMap(ev.Metadata)
	if ev.HTTP != nil {
		h := *ev.HTTP
		h.Method, h.Path, h.Host, h.Resource = clean(h.Method), clean(h.Path), clean(h.Host), clean(h.Resource)
		ev.HTTP = &h
	}
	return ev
}

func cleanRecord(r LogRecord) LogRecord {
	r.Level, r.Msg = clean(r.Level), clean(r.Msg)
	if r.Attrs != nil {
		attrs := make([]Attr, len(r.Attrs))
		for i, a := range r.Attrs {
			attrs[i] = Attr{Key: clean(a.Key), Value: clean(a.Value)}
		}
		r.Attrs = attrs
	}
	return r
}

// cutRunes shortens s to at most n bytes without splitting a character.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
