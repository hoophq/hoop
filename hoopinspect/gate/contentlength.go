package gate

import (
	"bytes"
	"strconv"
)

// retagContentLength rewrites an HTTP response's Content-Length header after
// masking changed the body by delta bytes. It reports whether the declared
// length now describes the payload.
//
// # The bug it prevents
//
// The masker rewrites values in place and the rewrite is rarely
// length-preserving: ada@example.com (15 bytes) becomes [REDACTED:email] (16).
// The declared Content-Length is now one byte short, so the client reads that
// many bytes and stops, ending the JSON mid-token. It looks like a corrupt
// upstream, and the report that comes back says "your proxy breaks
// responses".
//
// # Refusal conditions
//
// The relay hands the gate raw TCP chunks, so the header block and the body
// may or may not arrive together. Rewriting is attempted ONLY when the buffer
// carries a complete header block, exactly one Content-Length, and a declared
// length that matches the bytes present before masking. Any doubt and
// retagContentLength returns (payload, false): a wrong Content-Length is
// worse than a stale one, because it desynchronizes a keep-alive connection
// for every request that follows.
//
// A false return means the CALLER must discard its masked payload and forward
// the original bytes. Masking a body whose length cannot be corrected is the
// truncation this function exists to prevent, just relocated.
//
// A chunked response needs no fix, since its framing is per-chunk and the
// relay forwards whole chunks. Such a response has no Content-Length, so it
// reports false and its body goes through unmasked; see maskBySubstitution.
func retagContentLength(payload []byte, delta int) ([]byte, bool) {
	if delta == 0 {
		// Nothing to correct, and nothing was broken: a length-preserving
		// mask leaves the declared length accurate.
		return payload, true
	}

	headerEnd := bytes.Index(payload, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		// No complete header block in this buffer. Either the body arrived on
		// its own (the header went out already and cannot be corrected) or
		// the headers are split across reads. Both mean: do not guess.
		return payload, false
	}
	head := payload[:headerEnd]

	// Require a visible status line. Masking runs on the server direction,
	// but a buffer starting mid-body could hold something shaped like a
	// header block.
	if !bytes.HasPrefix(head, []byte("HTTP/")) {
		return payload, false
	}

	valueStart, valueEnd, n := findContentLength(head)
	if n != 1 {
		// Zero: chunked or no body, so nothing to correct. More than one: a
		// request smuggling shape. Refuse either way.
		return payload, false
	}

	declared, err := strconv.Atoi(string(bytes.TrimSpace(payload[valueStart:valueEnd])))
	if err != nil || declared < 0 {
		return payload, false
	}

	// The whole body must be in this buffer, or delta does not describe the
	// whole entity and the corrected number would be wrong.
	bodyLen := len(payload) - (headerEnd + 4)
	if bodyLen != declared+delta {
		return payload, false
	}

	updated := declared + delta
	if updated < 0 {
		return payload, false
	}

	// Rebuild rather than patch in place: the number's width changes.
	out := make([]byte, 0, len(payload)+8)
	out = append(out, payload[:valueStart]...)
	out = strconv.AppendInt(out, int64(updated), 10)
	return append(out, payload[valueEnd:]...), true
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
