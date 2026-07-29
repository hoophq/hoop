package gate

import (
	"bytes"
	"strconv"
)

// retagContentLength rewrites an HTTP response's Content-Length header after
// masking changed the body by delta bytes.
//
// # Why this is necessary
//
// The masker rewrites values in place and the rewrite is rarely
// length-preserving: ada@example.com (15 bytes) becomes [REDACTED:email] (16).
// The declared Content-Length is now one byte short, so the client reads that
// many bytes and stops — the JSON ends mid-token. It looks like a corrupt
// upstream, and the report that comes back is "your proxy breaks responses",
// not "your masking works".
//
// # Why it is this conservative
//
// The relay hands the gate raw TCP chunks, so the header block and the body
// may or may not arrive together. Rewriting is attempted ONLY when the buffer
// carries a complete header block, exactly one Content-Length, and a declared
// length that matches the bytes actually present before masking. Any doubt and
// the payload is returned untouched: a wrong Content-Length is worse than a
// stale one, because it desynchronizes a keep-alive connection for every
// request that follows.
//
// A chunked response needs no fix — its framing is per-chunk and the relay
// forwards whole chunks — so a response without Content-Length is left alone.
func retagContentLength(payload []byte, delta int) []byte {
	if delta == 0 {
		return payload
	}

	headerEnd := bytes.Index(payload, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		// No complete header block in this buffer. Either the body arrived on
		// its own (the header went out already and cannot be corrected) or
		// the headers are split across reads. Both mean: do not guess.
		return payload
	}
	head := payload[:headerEnd]

	// Only a response we can see the status line of. Masking runs on the
	// server direction, but a buffer starting mid-body could contain
	// something that merely looks like a header block.
	if !bytes.HasPrefix(head, []byte("HTTP/")) {
		return payload
	}

	valueStart, valueEnd, n := findContentLength(head)
	if n != 1 {
		// Zero: chunked or no body — nothing to correct. More than one: a
		// request smuggling shape. Refuse either way.
		return payload
	}

	declared, err := strconv.Atoi(string(bytes.TrimSpace(payload[valueStart:valueEnd])))
	if err != nil || declared < 0 {
		return payload
	}

	// The whole body must be in this buffer, or the delta we were handed does
	// not describe the whole entity and the corrected number would be wrong.
	bodyLen := len(payload) - (headerEnd + 4)
	if bodyLen != declared+delta {
		return payload
	}

	updated := declared + delta
	if updated < 0 {
		return payload
	}

	// Rebuild rather than patch in place: the number's width changes.
	out := make([]byte, 0, len(payload)+8)
	out = append(out, payload[:valueStart]...)
	out = strconv.AppendInt(out, int64(updated), 10)
	return append(out, payload[valueEnd:]...)
}

// findContentLength locates the single Content-Length header in a header
// block, returning the offsets of its value and how many were found. Matching
// is case-insensitive per RFC 9110.
func findContentLength(head []byte) (valueStart, valueEnd, count int) {
	const name = "content-length"

	for off := 0; off < len(head); {
		lineEnd := bytes.Index(head[off:], []byte("\r\n"))
		if lineEnd < 0 {
			lineEnd = len(head) - off
		}
		line := head[off : off+lineEnd]

		if colon := bytes.IndexByte(line, ':'); colon > 0 {
			if equalFold(bytes.TrimSpace(line[:colon]), name) {
				count++
				valueStart = off + colon + 1
				valueEnd = off + lineEnd
			}
		}

		off += lineEnd + 2
	}
	return valueStart, valueEnd, count
}

// equalFold compares an ASCII header name against a lowercase literal without
// allocating. strings.EqualFold would need a string conversion per line.
func equalFold(got []byte, lowerWant string) bool {
	if len(got) != len(lowerWant) {
		return false
	}
	for i := range got {
		c := got[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lowerWant[i] {
			return false
		}
	}
	return true
}
