package http

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/hoophq/hoop/sidecar/upstreamcred"
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
	viaAwaitSwitch                 // strict lanes: a switch was asked; is it granted?
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
// Once the server accepts a request to leave HTTP/1 (a 2xx to CONNECT, a
// 101 to an Upgrade), the rest of the connection passes through untouched:
// the bytes are another protocol, and a Via line inserted into a TLS record
// or a WebSocket frame corrupts it. The codec hands the filter every answer
// (answer), so a declined switch leaves the connection framed, marked and
// checked. A head net/http cannot parse, or one that is not HTTP/1.x (the
// HTTP/2 preface), stops a lane that is not strict: the codec reports such
// a stream itself, and rewriting what it cannot frame would only add damage.
//
// Only the client stream is rewritten. Via on responses is the server's
// side of the chain and nothing here depends on it.
//
// With an Origin set, the same head rewrite also points the request at the
// origin; see origin.go. With auth set, it checks each request's caller
// token before forwarding any of the request, then removes the header, so
// the token never reaches the upstream (credentials.admit). With upstream
// set, it replaces the client's copies of one header with the lane's own
// credential (Codec.HoldUpstreamCredential). On every lane it refuses a
// request that carries a credential it knows outside that credential's
// header (placementGuard). All of them share this framing because they read
// the same heads and bodies and must agree on where each one starts.
type viaFilter struct {
	mu       sync.Mutex // one client pump calls this, but nothing enforces that
	state    viaState
	err      error                    // a refused request; the connection is over
	origin   *Origin                  // nil forwards Host as the client wrote it
	auth     *credentials             // non-nil on a lane that authenticates its caller
	upstream *upstreamcred.Credential // non-nil on a lane that holds the upstream's credential
	guard    placementGuard

	held    []byte      // viaHead: the head so far; viaChunkLine: the line so far
	blankAt int         // viaHead: offset in held of the head's closing empty line
	line    lineTracker // viaHead, viaTrailer: the current line across calls

	remaining uint64 // viaFixedBody, viaChunkData, viaChunkCRLF
	trailer   int    // viaTrailer: bytes of the trailer section so far

	// leaving records that the request in flight asks to switch
	// protocols, so the filter waits for the answer once its body is done.
	leaving bool

	// Passing through on the request alone would let a client send
	// `Upgrade: x`, take an ordinary answer, and send the next request
	// unrewritten and unchecked. So the filter counts the final responses
	// the server sends (answer) and passes through only once the one
	// answering the switch request accepts it.
	sent     int  // requests whose head went out
	answered int  // final responses the server sent
	switchAt int  // which request (1-based) asked to switch, 0 for none
	connect  bool // that request is a CONNECT, which a 2xx accepts
	switched bool // the server accepted the switch
}

// filter rewrites one chunk of the client stream.
func (f *viaFilter) filter(src []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.state == viaPassThrough {
		if err := f.guard.passed(src); err != nil {
			return f.fail(err)
		}
		return src, nil
	}

	out := splice{src: src}
	pos := 0
	for pos < len(src) {
		switch f.state {
		case viaPassThrough:
			// Reached mid-call, after giveUp or viaAwaitSwitch.
			if err := f.guard.passed(src[pos:]); err != nil {
				return f.fail(err)
			}
			out.copy(pos, len(src))
			pos = len(src)

		case viaHead:
			n, blankLen, done := f.line.scan(src[pos:])
			f.held = append(f.held, src[pos:pos+n]...)
			pos += n
			if !done {
				if len(f.held) > maxHeadBytes {
					if err := f.giveUpBody(&out, "the request head exceeds 1 MiB"); err != nil {
						return nil, err
					}
				}
				continue
			}
			f.blankAt = len(f.held) - blankLen
			if err := f.finishHead(&out); err != nil {
				return f.fail(err)
			}

		case viaFixedBody, viaChunkData:
			n := min(uint64(len(src)-pos), f.remaining)
			if err := f.guard.scan(src[pos:pos+int(n)], "body"); err != nil {
				return f.fail(err)
			}
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
			// the codec, which then waits for a body that never ends.
			if src[pos] != "\r\n"[2-f.remaining] {
				if err := f.giveUpBody(&out, "a chunk does not end in CRLF"); err != nil {
					return nil, err
				}
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
					if err := f.giveUpBody(&out, "a chunk-size line exceeds 4 KiB"); err != nil {
						return nil, err
					}
				}
				continue
			}
			if err := f.guard.chunkLine(f.held); err != nil {
				return f.fail(err)
			}
			size, ok := parseChunkSize(f.held)
			f.held = f.held[:0]
			switch {
			case !ok:
				if err := f.giveUpBody(&out, "a chunk size does not parse"); err != nil {
					return nil, err
				}
			case size == 0:
				f.state, f.trailer, f.line = viaTrailer, 0, lineTracker{}
				f.guard.trailerStart()
			default:
				f.state, f.remaining = viaChunkData, size
			}

		case viaTrailer:
			n, _, done := f.line.scan(src[pos:])
			if err := f.guard.scan(src[pos:pos+n], "trailer"); err != nil {
				return f.fail(err)
			}
			if f.upstream != nil {
				// Held until whole, like a head: a client's copy of the
				// lane's header sent as a trailer reaches an upstream that
				// merges trailers into the header section, so it is dropped
				// here too.
				f.held = append(f.held, src[pos:pos+n]...)
			} else {
				out.copy(pos, pos+n)
			}
			pos += n
			f.trailer += n
			if done {
				if f.upstream != nil {
					out.add(dropMatching(f.held, 0, f.upstreamField))
				}
				f.endMessage()
			} else if f.trailer > maxHeadBytes {
				if err := f.giveUpBody(&out, "the trailer section exceeds 1 MiB"); err != nil {
					return nil, err
				}
			}

		case viaAwaitSwitch:
			// Nothing is consumed here, only the state decided. A client
			// sends after the switch request only once the answer reached
			// it, and the gate decodes that answer before forwarding it, so
			// the outcome is known then. Bytes that come before it cannot be
			// handled: rewritten as HTTP they would corrupt a tunnel the
			// server then opens, passed through they would skip the rewrite
			// and the credential check when it refuses, and held they would
			// wait for client bytes that may never come. So the connection
			// is refused, on every lane: a client that follows the protocol
			// waits for the answer.
			switch {
			case f.switched:
				f.state = viaPassThrough
			case f.switchAt == 0:
				f.state = viaHead
			default:
				return f.fail(errors.New(f.promise() + ", and the client sent " +
					"more data before the server answered its Upgrade or CONNECT, so it cannot; the connection is closed"))
			}
		}
	}
	return out.bytes(), nil
}

// fail ends the connection with err: every later call returns it too.
func (f *viaFilter) fail(err error) ([]byte, error) {
	f.err = err
	return nil, err
}

// finishHead parses the complete head in f.held and emits it, marked.
func (f *viaFilter) finishHead(out *splice) error {
	head := f.held
	if len(head) > maxHeadBytes {
		return f.giveUpBody(out, "the request head exceeds 1 MiB")
	}
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(head)))
	if err != nil {
		// Not err's text: net/http quotes the line it refused, and that
		// line can be a caller token or an Authorization header. The
		// refusal reaches the audit trail and the client's 403.
		return f.giveUp(out, "the request head does not parse")
	}
	if req.ProtoMajor != 1 {
		return f.giveUp(out, "the request is not HTTP/1")
	}
	if elem, ok := viaNaming(req.Header.Values("Via"), pseudonym); ok {
		return fmt.Errorf("request loop: this request already passed through this sidecar (Via %s); route the sidecar's own upstream traffic around it", elem)
	}

	// Last, right before the empty line: RFC 9110 7.6.3 has each proxy
	// append its entry, and a recipient combining several Via lines reads
	// them in order. The empty line is a bare line ending, CRLF or LF as
	// the client wrote it; one copy ends the Via line, one ends the head.
	eol := head[f.blankAt:]
	fields := head[:f.blankAt]
	var callerHeader, caller string
	if f.auth != nil {
		// On the head: the gate holds a request until its body has
		// arrived, so checking here refuses a stranger before the sidecar
		// buffers a body for them, and refuses a body that never comes.
		if err := f.auth.admit(req); err != nil {
			return err
		}
		callerHeader, caller = f.auth.header, f.auth.presented(req)
	}
	// One read per head: the value sent and the token the guard looks for
	// come from the same rotation.
	var held secret
	var heldValue string
	if f.upstream != nil {
		var tok string
		heldValue, tok = f.upstream.Current()
		held = secret{value: []byte(tok), home: strings.ToLower(f.upstream.Header())}
	}
	// After admit, so a stranger gets the identity refusal; on the head as
	// the client sent it, before any rewrite.
	if err := f.guard.begin(req, fields, callerHeader, caller, held); err != nil {
		return err
	}
	if f.auth != nil {
		fields = dropField(fields, f.auth.header)
	}
	if f.upstream != nil {
		// Every copy the client sent, so the upstream reads one value and
		// it is the lane's: an upstream that takes the first Authorization
		// and one that takes the last agree.
		line, _ := cutLine(fields)
		fields = dropMatching(fields, len(line), f.upstreamField)
	}
	if f.origin != nil {
		f.origin.writeHead(out, fields, req, eol)
	} else {
		out.add(fields)
	}
	if f.upstream != nil {
		out.addf("%s: %s", f.upstream.Header(), heldValue)
		out.add(eol)
	}
	out.addf("Via: %d.%d %s", req.ProtoMajor, req.ProtoMinor, pseudonym)
	out.add(eol)
	out.add(eol)

	f.held = f.held[:0]
	f.line = lineTracker{}
	f.leaving = req.Method == http.MethodConnect || req.Header.Get("Upgrade") != ""
	f.sent++
	if f.leaving {
		f.switchAt, f.connect = f.sent, req.Method == http.MethodConnect
	}
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

// strict reports a lane whose rewrite the upstream relies on: the origin
// reads the Host it was promised, a dropped header must never arrive, and
// a held credential must replace the client's copy on every request. Such
// a lane refuses a head it cannot parse, where a Via-only lane lets it go
// as written, scanned for the credentials it knows.
func (f *viaFilter) strict() bool { return f.origin != nil || f.auth != nil || f.upstream != nil }

// promise names what the lane does to every request, for the error that
// refuses a stream it cannot do it to. It names headers, never a value.
func (f *viaFilter) promise() string {
	var does []string
	if f.origin != nil {
		does = append(does, "points every request at its upstream")
	}
	if f.auth != nil {
		does = append(does, "removes the "+f.auth.header+" header from every request")
	}
	if f.upstream != nil {
		does = append(does, "sets its own "+f.upstream.Header()+" header on every request")
	}
	if len(does) == 0 {
		return "this listener checks the credentials in every request"
	}
	return "this listener " + strings.Join(does, " and ")
}

// endMessage moves past a finished request: to the next head, or, when the
// request asked to switch protocols, to wait for the server's answer.
func (f *viaFilter) endMessage() {
	f.guard.end()
	f.state = viaHead
	if f.leaving {
		f.leaving = false
		f.state = viaAwaitSwitch
	}
	f.held = f.held[:0]
	f.line = lineTracker{}
}

// answer records one response the server sent, by status, and pairs it
// with its request: to learn the credentials the upstream accepts, and
// whether it granted a switch. Interim 1xx answers other than 101 are not
// final and do not count.
func (f *viaFilter) answer(status int) {
	if status < 200 && status != http.StatusSwitchingProtocols {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.guard.settle(status)
	f.answered++
	if f.switchAt == 0 || f.answered != f.switchAt {
		return
	}
	if f.connect {
		// A 101 to CONNECT is not RFC 9110's, but libhoop takes the
		// connection for a tunnel after one, so the filter must too.
		f.switched = status/100 == 2 || status == http.StatusSwitchingProtocols
	} else {
		f.switched = status == http.StatusSwitchingProtocols
	}
	f.switchAt = 0
}

// giveUp handles a stream the filter cannot frame. A Via-only lane passes
// a head it cannot frame through as written, unless it carries a credential
// (placementGuard.unframe): the codec reports such a head itself, and a
// line added to it would only add damage. A body is giveUpBody's. A strict
// lane refuses it, because the bytes would reach the upstream with the
// client's Host and X-Forwarded fields (see origin.go) or with the header
// the lane exists to remove.
func (f *viaFilter) giveUp(out *splice, why string) error {
	if !f.strict() {
		if err := f.guard.unframe(f.held); err != nil {
			f.err = err
			return err
		}
		f.passThrough(out)
		return nil
	}
	f.err = fmt.Errorf("%s, and %s, so it cannot; the connection is closed", f.promise(), why)
	return f.err
}

// giveUpBody handles a request the filter cannot frame, and every lane
// refuses it, a Via-only one too. A body passed through would not leave:
// the codec reads a body it cannot frame as one still arriving, and the
// gate forwards a request only once the codec decodes it, so the client
// would wait unanswered until the connection idles out. A head over 1 MiB
// is cut short, so a credential past the cut would escape the check.
func (f *viaFilter) giveUpBody(out *splice, why string) error {
	if f.strict() {
		return f.giveUp(out, why)
	}
	f.err = fmt.Errorf("%s, so this listener cannot judge the request; the connection is closed", why)
	return f.err
}

// upstreamField reports whether a client field is a copy of the lane's
// upstream credential header as an upstream may read it: case aside, with
// space before the colon, and with _ for -, which CGI-style servers (PHP,
// WSGI) fold into one name. Called with f.mu held.
func (f *viaFilter) upstreamField(name string) bool {
	norm := func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "_", "-") }
	return strings.EqualFold(norm(name), norm(f.upstream.Header()))
}

// dropField returns fields, a request head without its closing empty line,
// with every field of the header name removed: a repeated header is the same
// credential twice. fields comes back as is when nothing matches, so a
// request without the header costs no copy.
func dropField(fields []byte, name string) []byte {
	line, _ := cutLine(fields) // the request line, never a field
	return dropMatching(fields, len(line), func(field string) bool { return strings.EqualFold(field, name) })
}

// dropMatching returns fields with every field from offset at on whose name
// drop reports removed, a folded continuation line with its field. fields
// comes back as is when nothing matches.
func dropMatching(fields []byte, at int, drop func(name string) bool) []byte {
	var out []byte // nil while every field is kept; fields[:at] is decided
	eachField(fields[at:], func(field string, _, whole []byte) {
		gone := drop(field)
		switch {
		case gone && out == nil:
			out = append(make([]byte, 0, len(fields)), fields[:at]...)
		case !gone && out != nil:
			out = append(out, whole...)
		}
		at += len(whole)
	})
	if out == nil {
		return fields
	}
	return out
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
