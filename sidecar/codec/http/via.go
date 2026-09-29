package http

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// pseudonym is this process's received-by in the Via field it adds.
//
// A pseudonym rather than a host name, which RFC 9110 7.6.3 allows: the
// sidecar's address says nothing useful upstream and may be private. It is
// random per process so that two sidecars in one chain (a relay in front of
// another relay) do not take each other for themselves, and so a restarted
// process does not refuse a request a previous instance stamped.
var pseudonym = newPseudonym()

func newPseudonym() string {
	var b [8]byte
	// Read never returns an error since Go 1.24: a failing entropy source
	// crashes the process instead, which is the right outcome for a value
	// the loop guard rests on.
	rand.Read(b[:])
	return "hoop-" + hex.EncodeToString(b[:])
}

// Pseudonym is the received-by this process writes into Via, for a log line
// or a test that needs to recognize the sidecar's own mark.
func Pseudonym() string { return pseudonym }

// maxHeadBytes mirrors libhoop's bound on one message head. Past it the
// codec refuses the stream itself, so the filter only stops tracking.
const maxHeadBytes = 1 << 20

// maxChunkLineBytes mirrors net/http's bound on one chunk-size line, which
// libhoop's body reader inherits: a longer line is refused by the codec.
const maxChunkLineBytes = 4096

// viaState is where in the request stream the filter stands.
type viaState uint8

const (
	viaHead        viaState = iota // accumulating a request head
	viaFixedBody                   // a Content-Length body; remaining counts down
	viaChunkLine                   // a chunk-size line, extensions included
	viaChunkData                   // one chunk's data; remaining counts down
	viaChunkCRLF                   // the CRLF closing a chunk; remaining is 2 or 1
	viaTrailer                     // the trailer section after the last chunk
	viaPassThrough                 // no longer rewriting this connection
)

// viaFilter adds `Via: <version> <pseudonym>` to every HTTP/1 request the
// client sends, and refuses a request that already carries it.
//
// # Why
//
// RFC 9110 7.6.3 says a proxy MUST send Via, and it is what makes a
// forwarding loop detectable. That is not academic here: behind a
// transparent MITM (the customer's Envoy intercepting every outbound
// connection) the sidecar's own upstream connection can be steered back into
// its listener. Every lap looks like a fresh client, so without a marker the
// loop runs until something exhausts a resource, and the error it ends with
// names none of this. With one, the second lap is refused at once with a
// message that says what is wrong and how to fix it.
//
// # Framing
//
// The filter sees the raw client byte stream in arbitrary chunks, so it has
// to know where each request ends to find the next head: bodies go through
// untouched, framed by the head exactly as net/http reads it (chunked, else
// Content-Length, else none), because that is the reader the codec uses and
// the two must agree on where requests begin. Heads, chunk-size lines and
// trailers may split across calls at any byte. A head is held until it is
// complete; everything else streams out as it arrives.
//
// # What stops it
//
// Once a request asks to leave HTTP/1 (CONNECT, or any Upgrade), the rest of
// the connection passes through untouched: after a 2xx or a 101 the bytes
// are another protocol, and a Via line inserted into a TLS record or a
// WebSocket frame corrupts it. The filter cannot see the server's answer,
// so it stops even when the server declines; later requests on that
// connection then carry no Via and escape loop detection, which costs only
// the loop's quick diagnosis. A head net/http cannot parse, or one that is
// not HTTP/1.x (the HTTP/2 preface), stops it the same way: the codec reports
// such a stream itself, and rewriting what it cannot frame would only add
// damage.
//
// Only the client stream is rewritten. Via on responses is the server's
// side of the chain and nothing here depends on it.
type viaFilter struct {
	mu    sync.Mutex // one client pump calls this, but nothing enforces that
	state viaState
	err   error // a refused request; the connection is over

	held    []byte      // viaHead: the head so far; viaChunkLine: the line so far
	blankAt int         // viaHead: offset in held of the head's closing empty line
	line    lineTracker // viaHead, viaTrailer: the current line across calls

	remaining uint64 // viaFixedBody, viaChunkData, viaChunkCRLF
	trailer   int    // viaTrailer: bytes of the trailer section so far

	// leaving records that the request in flight switches protocols, so
	// the connection passes through once its body is done.
	leaving bool
}

// filter rewrites one chunk of the client stream.
func (f *viaFilter) filter(src []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.state == viaPassThrough {
		return src, nil
	}

	out := splice{src: src}
	pos := 0
	for pos < len(src) {
		switch f.state {
		case viaPassThrough:
			out.copy(pos, len(src))
			pos = len(src)

		case viaHead:
			n, blankLen, done := f.line.scan(src[pos:])
			f.held = append(f.held, src[pos:pos+n]...)
			pos += n
			if !done {
				if len(f.held) > maxHeadBytes {
					f.passThrough(&out)
				}
				continue
			}
			f.blankAt = len(f.held) - blankLen
			if err := f.finishHead(&out); err != nil {
				f.err = err
				return nil, err
			}

		case viaFixedBody, viaChunkData:
			n := min(uint64(len(src)-pos), f.remaining)
			out.copy(pos, pos+int(n))
			pos += int(n)
			f.remaining -= n
			if f.remaining > 0 {
				continue
			}
			if f.state == viaFixedBody {
				f.endMessage()
			} else {
				f.state, f.remaining = viaChunkCRLF, 2
			}

		case viaChunkCRLF:
			// net/http accepts exactly CRLF here; anything else desyncs
			// the codec, which then reports the body malformed.
			if src[pos] != "\r\n"[2-f.remaining] {
				f.passThrough(&out)
				continue
			}
			out.copy(pos, pos+1)
			pos++
			if f.remaining--; f.remaining == 0 {
				f.state = viaChunkLine
			}

		case viaChunkLine:
			end := len(src)
			nl := bytes.IndexByte(src[pos:], '\n')
			if nl >= 0 {
				end = pos + nl + 1
			}
			// The line streams out as it arrives; only a copy is kept, to
			// read the size from once it is whole.
			f.held = append(f.held, src[pos:end]...)
			out.copy(pos, end)
			pos = end
			if nl < 0 {
				if len(f.held) > maxChunkLineBytes {
					f.held = nil
					f.state = viaPassThrough
				}
				continue
			}
			size, ok := parseChunkSize(f.held)
			f.held = f.held[:0]
			switch {
			case !ok:
				f.state = viaPassThrough
			case size == 0:
				f.state, f.trailer, f.line = viaTrailer, 0, lineTracker{}
			default:
				f.state, f.remaining = viaChunkData, size
			}

		case viaTrailer:
			n, _, done := f.line.scan(src[pos:])
			out.copy(pos, pos+n)
			pos += n
			f.trailer += n
			if done {
				f.endMessage()
			} else if f.trailer > maxHeadBytes {
				f.state = viaPassThrough
			}
		}
	}
	return out.bytes(), nil
}

// finishHead parses the complete head in f.held and emits it, marked.
func (f *viaFilter) finishHead(out *splice) error {
	head := f.held
	if len(head) > maxHeadBytes {
		f.passThrough(out)
		return nil
	}
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
	if err != nil || req.ProtoMajor != 1 {
		f.passThrough(out)
		return nil
	}
	if elem, ok := viaNaming(req.Header.Values("Via"), pseudonym); ok {
		return fmt.Errorf("request loop: this request already passed through this sidecar (Via %s); route the sidecar's own upstream traffic around it", elem)
	}

	// Last, right before the empty line: RFC 9110 7.6.3 has each proxy
	// append its entry, and a recipient combining several Via lines reads
	// them in order. The empty line is a bare line ending, CRLF or LF as
	// the client wrote it; one copy ends the Via line, one ends the head.
	eol := head[f.blankAt:]
	out.add(head[:f.blankAt])
	out.addf("Via: %d.%d %s", req.ProtoMajor, req.ProtoMinor, pseudonym)
	out.add(eol)
	out.add(eol)

	f.held = f.held[:0]
	f.line = lineTracker{}
	f.leaving = req.Method == http.MethodConnect || req.Header.Get("Upgrade") != ""
	switch {
	case len(req.TransferEncoding) > 0:
		// net/http refuses every coding but a final chunked, so a
		// non-empty list here means chunked.
		f.state = viaChunkLine
	case req.ContentLength > 0:
		f.state, f.remaining = viaFixedBody, uint64(req.ContentLength)
	default:
		f.endMessage()
	}
	return nil
}

// endMessage moves past a finished request: to the next head, or to
// pass-through when the request switched protocols.
func (f *viaFilter) endMessage() {
	if f.leaving {
		f.state = viaPassThrough
		f.held = nil
		return
	}
	f.state = viaHead
	f.held = f.held[:0]
	f.line = lineTracker{}
}

// passThrough gives up rewriting, releasing whatever head is held exactly
// as it arrived.
func (f *viaFilter) passThrough(out *splice) {
	if f.state == viaHead {
		out.add(f.held)
	}
	f.held = nil
	f.state = viaPassThrough
}

// lineTracker finds the empty line that closes a head or a trailer section,
// across calls. Lines end in LF with an optional CR, as net/http reads them.
type lineTracker struct {
	n    int  // bytes of the current line seen before this call
	last byte // the last of them
}

// scan consumes data through the first empty line and reports that line's
// length with its LF (1 or 2). done is false when data ran out first; all of
// it was consumed.
func (t *lineTracker) scan(data []byte) (consumed, blankLen int, done bool) {
	at := 0
	for {
		k := bytes.IndexByte(data[at:], '\n')
		if k < 0 {
			if at < len(data) {
				t.n += len(data) - at
				t.last = data[len(data)-1]
			}
			return len(data), 0, false
		}
		n, last := t.n+k, t.last
		if k > 0 {
			last = data[at+k-1]
		}
		t.n, t.last = 0, 0
		if n == 0 || (n == 1 && last == '\r') {
			return at + k + 1, n + 1, true
		}
		at += k + 1
	}
}

// parseChunkSize reads the size from a whole chunk-size line, extensions
// and line ending included. Whitespace around the size is tolerated, as
// RFC 9112 7.1.1's BWS allows before an extension; net/http is stricter and
// the codec then reports the body itself.
func parseChunkSize(line []byte) (uint64, bool) {
	line = bytes.TrimRight(line, " \t\r\n")
	if i := bytes.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}
	line = bytes.Trim(line, " \t")
	if len(line) == 0 || len(line) > 16 {
		return 0, false
	}
	var n uint64
	for _, c := range line {
		var v byte
		switch {
		case '0' <= c && c <= '9':
			v = c - '0'
		case 'a' <= c && c <= 'f':
			v = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		n = n<<4 | uint64(v)
	}
	return n, true
}

// viaNaming returns the Via entry whose received-by is by, and whether
// there is one.
//
// Via is `#( received-protocol RWS received-by [ RWS comment ] )`, and a
// comment may itself hold commas and anything that looks like another
// entry, so the list is split outside comments only: splitting on every
// comma would read "1.1 edge (from 1.1 hoop-...)" as this sidecar's own mark.
func viaNaming(values []string, by string) (string, bool) {
	for _, v := range values {
		for _, elem := range viaEntries(v) {
			fields := strings.Fields(elem.bare)
			if len(fields) >= 2 && strings.EqualFold(fields[1], by) {
				return elem.raw, true
			}
		}
	}
	return "", false
}

// viaEntry is one Via list member: as sent, and with its comments removed.
type viaEntry struct{ raw, bare string }

// viaEntries splits one Via field value into its members. Comments nest and
// may escape a character with a backslash (RFC 9110 5.6.5).
func viaEntries(v string) []viaEntry {
	var (
		entries []viaEntry
		bare    strings.Builder
		depth   int
		escaped bool
		start   int
	)
	flush := func(end int) {
		if raw := strings.TrimSpace(v[start:end]); raw != "" {
			entries = append(entries, viaEntry{raw: raw, bare: bare.String()})
		}
		bare.Reset()
		start = end + 1
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case escaped:
			escaped = false
		case depth > 0 && c == '\\':
			escaped = true
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case depth == 0 && c == ',':
			flush(i)
		case depth == 0:
			bare.WriteByte(c)
		}
	}
	flush(len(v))
	return entries
}

// splice assembles one filter result. While the output is an unbroken
// prefix of src it stays an alias of it, so a chunk that is all body is
// returned as the caller's own slice, uncopied; the first byte that does not
// come from src in place materializes a copy.
type splice struct {
	src []byte
	n   int    // src[:n] is the output so far, while buf is nil
	buf []byte // the materialized output, once it diverged
}

func (s *splice) copy(from, to int) {
	if s.buf == nil && from == s.n {
		s.n = to
		return
	}
	s.materialize()
	s.buf = append(s.buf, s.src[from:to]...)
}

func (s *splice) add(b []byte) {
	s.materialize()
	s.buf = append(s.buf, b...)
}

func (s *splice) addf(format string, args ...any) {
	s.materialize()
	s.buf = fmt.Appendf(s.buf, format, args...)
}

func (s *splice) materialize() {
	if s.buf == nil {
		s.buf = append(make([]byte, 0, len(s.src)+64), s.src[:s.n]...)
	}
}

func (s *splice) bytes() []byte {
	if s.buf == nil {
		return s.src[:s.n]
	}
	return s.buf
}
