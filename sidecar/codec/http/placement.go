package http

import (
	"bytes"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	codechttp "github.com/hoophq/libhoop/v2/codec/http"
)

// maxInflight bounds the requests whose credentials one connection holds
// while their answers are outstanding. It matches libhoop's bound on the
// requests it pairs with answers, though nothing depends on the two being
// equal: past it the connection stops learning credentials, because an
// answer could no longer be paired with its request.
const maxInflight = 256

// placementGuard keeps a credential the lane knows in the header it belongs
// in, for one connection. It runs under viaFilter.mu.
//
// # Why
//
// OpenAI's vault fills a secret wherever its placeholder appears in a
// request toward the host it is bound to: the URL and the body, not only
// headers (ENG-860). So a token an agent writes into a URL arrives as the
// real token in the URL. The audit row records the request line in clear,
// and an upstream that echoes a path or query value (`kubectl get pod
// "$KUBE_TOKEN"` answers `pods "<token>" not found`) hands the real token
// back to the agent. A prompt telling the agent not to is not a control.
//
// So the filter refuses such a request before any of it reaches the
// upstream. It cannot redact instead: the upstream would still see, and
// may echo, the token.
//
// # Which credentials
//
// The ones the lane can know without configuration:
//
//   - the caller token the filter admitted, at home in its own header;
//   - each Authorization credential of the request, at home in
//     Authorization;
//   - the Authorization credentials of earlier requests on the connection
//     still awaiting an answer, and those the lane remembers the upstream
//     accepting (SeenCredentials), at home in Authorization too, so a
//     request that leaves its Authorization off is still checked.
//
// Each is looked for everywhere else: the request line, raw and with its
// percent escapes decoded; every other header, its name included; the body
// as the upstream reads it, with chunk framing removed, so neither a read
// boundary nor a chunk boundary splits a token out of sight; chunk
// extensions; trailers. Values under minSecretBytes are not looked for.
//
// # Limits
//
// The bytes must hold the credential as sent. A body that is compressed,
// or encodes the token (base64, a JSON \u escape), is not decoded. Neither
// is Basic's password: its credential is the base64 of user and password.
// Past a protocol switch the server accepted, the bytes are another
// protocol and are not scanned.
type placementGuard struct {
	seen *SeenCredentials

	// The request in flight, or the unframed stream; see unframe.
	secrets []secret
	needles [][]byte    // the distinct secret values, longest first
	stream  *needleScan // its body, then its trailer
	method  string
	path    string // escaped, without the query

	// inflight holds the Authorization credentials of each request sent
	// and not yet answered, oldest first: answers come in request order.
	inflight [][]string
	lost     bool // inflight overflowed; nothing more is learned here
}

// secret is one credential the guard looks for, and the header it may sit
// in.
type secret struct {
	value []byte
	home  string // lowercased header name
}

// begin checks one request head before any of it is forwarded, and readies
// the scan of its body. fields is the head as the client sent it, request
// line first, without the closing empty line. caller is the caller token
// the filter admitted under callerHeader, "" on a lane that admits none.
func (g *placementGuard) begin(req *http.Request, fields []byte, callerHeader, caller string) error {
	own := authorizationCredentials(req.Header.Values("Authorization"))
	g.secrets = g.secrets[:0]
	g.add(caller, callerHeader)
	for _, v := range own {
		g.add(v, "authorization")
	}
	g.addKnown()
	g.method, g.path = req.Method, req.URL.EscapedPath()
	if where := g.scanHead(fields); where != "" {
		return g.refusal(where)
	}
	g.stream = newNeedleScan(g.needles)
	g.track(own)
	return nil
}

// scanHead returns where in the head a credential sits outside its home,
// "" when none does.
func (g *placementGuard) scanHead(fields []byte) string {
	line, rest := cutLine(fields)
	if containsAny(line, g.needles) || containsAny(unescape(line), g.needles) {
		return "request line"
	}
	// Secret by secret, in the order begin added them, so a caller token
	// sent as the Authorization credential too is reported where it is
	// out of place, Authorization, rather than the other way round.
	where := ""
	for _, s := range g.secrets {
		eachField(rest, func(name string, _, whole []byte) {
			if where != "" || strings.EqualFold(name, s.home) || !bytes.Contains(whole, s.value) {
				return
			}
			// The name is the client's to choose, and a vault fills a
			// placeholder written there too.
			if containsAny([]byte(name), g.needles) {
				where = "header name"
			} else {
				where = "header " + name
			}
		})
		if where != "" {
			break
		}
	}
	return where
}

// scan reads the next bytes of the body, chunk framing removed, or of the
// trailer section, which is no credential's home: no credential is checked
// there.
func (g *placementGuard) scan(p []byte, where string) error {
	if g.stream.write(p) {
		return g.refusal(where)
	}
	return nil
}

// chunkLine scans one whole chunk-size line, extensions included: the
// upstream receives it, so a value in an extension reaches it as surely as
// one in the data.
func (g *placementGuard) chunkLine(line []byte) error {
	if g.stream.contains(line) {
		return g.refusal("body")
	}
	return nil
}

// trailerStart begins the trailer section, which no token spans into from
// the body.
func (g *placementGuard) trailerStart() { g.stream.reset() }

// end drops the request's scan: a token split across two requests reaches
// neither the trail nor the upstream whole.
func (g *placementGuard) end() { g.stream = nil }

// settle pairs one final answer with the oldest request awaiting one, and
// remembers that request's Authorization credentials when the upstream
// accepted them.
func (g *placementGuard) settle(status int) {
	if g.lost || len(g.inflight) == 0 {
		return
	}
	own := g.inflight[0]
	g.inflight = g.inflight[1:]
	if status/100 == 2 {
		for _, v := range own {
			g.seen.remember(v)
		}
	}
}

// errUnframedCredential refuses an unparsed head that carries an
// Authorization field.
var errUnframedCredential = errors.New("the request head does not parse, and it carries an " +
	"Authorization header this listener cannot keep out of the rest of it; the connection is closed")

// unframe readies the scan of a stream a lane that is not strict passes
// through without framing it: a head it cannot parse goes out as written
// (viaFilter.giveUp), and so do bytes sent before the server answers a
// switch. head is the unparsed head, nil for the second case.
//
// A head that carries an Authorization field is refused instead: unparsed,
// its credential cannot be told from a copy of it in the URL, and the
// codec's error would forward both under the gate's honest default. What
// passes is scanned as raw bytes for the credentials known when it started:
// one the upstream accepts later on this connection is not added.
func (g *placementGuard) unframe(head []byte) error {
	_, fields := cutLine(head)
	authorized := false
	eachField(fields, func(name string, _, _ []byte) {
		authorized = authorized || strings.EqualFold(strings.TrimSpace(name), "authorization")
	})
	if authorized {
		return errUnframedCredential
	}
	g.secrets = g.secrets[:0]
	g.addKnown()
	g.stream = newNeedleScan(g.needles)
	return g.passed(head)
}

// passed scans bytes passed through unframed. Past a switch the server
// accepted it scans nothing: endMessage dropped the scan, and unframe never
// ran.
func (g *placementGuard) passed(p []byte) error {
	if g.stream.write(p) {
		return &inspect.PlacementRefusal{Where: "bytes this listener could not frame as requests"}
	}
	return nil
}

// addKnown adds the credentials known before this request: those of the
// requests awaiting an answer, and those the lane remembers. Then it
// recomputes needles.
func (g *placementGuard) addKnown() {
	for _, vs := range g.inflight {
		for _, v := range vs {
			g.add(v, "authorization")
		}
	}
	for _, v := range g.seen.snapshot() {
		g.add(v, "authorization")
	}
	// Longest first, so the replacer in refusal consumes a token whole
	// before a shorter one inside it can match.
	g.needles = g.needles[:0]
	for _, s := range g.secrets {
		if !slices.ContainsFunc(g.needles, func(v []byte) bool { return bytes.Equal(v, s.value) }) {
			g.needles = append(g.needles, s.value)
		}
	}
	slices.SortStableFunc(g.needles, func(a, b []byte) int { return len(b) - len(a) })
}

func (g *placementGuard) add(v, home string) {
	if len(v) < minSecretBytes {
		return
	}
	s := secret{value: []byte(v), home: home}
	if !slices.ContainsFunc(g.secrets, func(o secret) bool { return o.home == s.home && bytes.Equal(o.value, s.value) }) {
		g.secrets = append(g.secrets, s)
	}
}

func (g *placementGuard) track(own []string) {
	if g.lost {
		return
	}
	if len(g.inflight) >= maxInflight {
		g.inflight, g.lost = nil, true
		return
	}
	g.inflight = append(g.inflight, own)
}

// refusal is the error for a credential found at where in the request in
// flight. Its request line names the path with every known credential
// replaced, and when one is still there with its escapes decoded, the whole
// path: the row must not hold one in any spelling.
func (g *placementGuard) refusal(where string) error {
	pairs := make([]string, 0, 2*len(g.needles))
	for _, v := range g.needles {
		pairs = append(pairs, string(v), codechttp.RedactedValue)
	}
	path := strings.NewReplacer(pairs...).Replace(g.path)
	if containsAny(unescape([]byte(path)), g.needles) {
		path = codechttp.RedactedValue
	}
	return &inspect.PlacementRefusal{Request: strings.TrimSpace(g.method + " " + path), Where: where}
}

// authorizationCredentials returns the credential in each Authorization
// value: what follows the auth scheme, or the whole value when it names
// none. A repeated header gives one credential per line.
func authorizationCredentials(values []string) []string {
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if _, cred, ok := strings.Cut(v, " "); ok {
			v = strings.TrimLeft(cred, " \t")
		}
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// unescape decodes every well-formed %XX in b and keeps everything else as
// sent. Lenient on purpose: net/url refuses a whole string over one bad
// escape, and a client could add one to keep an encoded token out of
// sight. '+' stays '+': the raw bytes are checked as well, and a decoded
// space would match nothing.
func unescape(b []byte) []byte {
	if bytes.IndexByte(b, '%') < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '%' && i+2 < len(b) {
			if hi, ok := unhex(b[i+1]); ok {
				if lo, ok := unhex(b[i+2]); ok {
					out = append(out, hi<<4|lo)
					i += 2
					continue
				}
			}
		}
		out = append(out, b[i])
	}
	return out
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
