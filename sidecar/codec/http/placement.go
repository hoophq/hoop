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

// maxSecrets bounds the credentials one request is checked against. The
// check costs each credential times each header field and body byte, so a
// head padded with Authorization lines, or a connection that pipelines many
// distinct ones, would otherwise buy any amount of CPU. A real request
// carries one or two; past the bound the request is refused, since a
// credential left out would be one left unchecked.
const maxSecrets = 128

// errTooManyCredentials refuses a request over maxSecrets.
var errTooManyCredentials = errors.New("the request carries more credentials than this listener checks; the connection is closed")

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
//   - the upstream credential the lane holds, at home in its header;
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
	secrets  []secret
	overflow bool        // add met maxSecrets
	needles  [][]byte    // the distinct secret values, longest first
	stream   *needleScan // its body, then its trailer
	method   string
	path     string // escaped, without the query

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
// held is the token of the upstream credential the lane holds, zero on a
// lane that holds none.
func (g *placementGuard) begin(req *http.Request, fields []byte, callerHeader, caller string, held secret) error {
	own := authorizationCredentials(req.Header.Values("Authorization"))
	if held.home == "authorization" {
		// The lane replaces the client's Authorization, so the upstream
		// never reads it: a 2xx would teach SeenCredentials a value no
		// upstream accepted. The credential that request carries is the
		// lane's.
		own = []string{string(held.value)}
	}
	g.secrets, g.overflow = g.secrets[:0], false
	g.add(caller, callerHeader)
	g.add(string(held.value), held.home)
	for _, v := range own {
		g.add(v, "authorization")
	}
	g.addKnown()
	if g.overflow {
		return errTooManyCredentials
	}
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
	type field struct {
		name  string
		whole []byte
	}
	var parsed []field
	eachField(rest, func(name string, _, whole []byte) { parsed = append(parsed, field{name, whole}) })
	// Secret by secret, in the order begin added them, so a caller token
	// sent as the Authorization credential too is reported where it is
	// out of place, Authorization, rather than the other way round.
	for _, s := range g.secrets {
		for _, f := range parsed {
			if strings.EqualFold(f.name, s.home) || !bytes.Contains(f.whole, s.value) {
				continue
			}
			// The name is the client's to choose, and a vault fills a
			// placeholder written there too.
			if containsAny([]byte(f.name), g.needles) {
				return "header name"
			}
			return "header " + f.name
		}
	}
	return ""
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
// through without framing it, from a head it cannot parse on
// (viaFilter.giveUp).
//
// A head that carries an Authorization field is refused instead: unparsed,
// its credential cannot be told from a copy of it in the URL, and the
// codec's error would forward both under the gate's honest default. What
// passes is scanned as raw bytes for the credentials known when it started,
// and the head with its escapes decoded too: one the upstream accepts later
// on this connection is not added.
func (g *placementGuard) unframe(head []byte) error {
	_, fields := cutLine(head)
	authorized := false
	eachField(fields, func(name string, _, _ []byte) {
		authorized = authorized || strings.EqualFold(strings.TrimSpace(name), "authorization")
	})
	if authorized {
		return errUnframedCredential
	}
	g.secrets, g.overflow = g.secrets[:0], false
	g.addKnown()
	if g.overflow {
		return errTooManyCredentials
	}
	g.stream = newNeedleScan(g.needles)
	if err := g.passed(head); err != nil {
		return err
	}
	if g.stream.contains(unescape(head)) {
		return &inspect.PlacementRefusal{Where: "bytes this listener could not frame as requests"}
	}
	return nil
}

// passed scans bytes passed through unframed. Past a switch the server
// accepted it scans nothing: endMessage dropped the scan, and no switch
// follows a head that did not parse.
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
	if len(v) < minSecretBytes || g.overflow {
		return
	}
	s := secret{value: []byte(v), home: home}
	if slices.ContainsFunc(g.secrets, func(o secret) bool { return o.home == s.home && bytes.Equal(o.value, s.value) }) {
		return
	}
	if len(g.secrets) >= maxSecrets {
		g.overflow = true
		return
	}
	g.secrets = append(g.secrets, s)
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
	return &inspect.PlacementRefusal{Request: strings.TrimSpace(g.method + " " + redact(g.path, g.needles)), Where: where}
}

// redact returns path with each of needles, longest first, replaced by a
// marker, and the whole path replaced when one is still there with its
// escapes decoded: an audit row must not hold one in any spelling.
func redact(path string, needles [][]byte) string {
	pairs := make([]string, 0, 2*len(needles))
	for _, v := range needles {
		pairs = append(pairs, string(v), codechttp.RedactedValue)
	}
	path = strings.NewReplacer(pairs...).Replace(path)
	if containsAny(unescape([]byte(path)), needles) {
		return codechttp.RedactedValue
	}
	return path
}

// authorizationCredentials returns the credential in each Authorization
// value: what follows the auth scheme, or the whole value when it names
// none. A repeated header gives one credential per line. A tab ends the
// scheme as a space does: RFC 9110 names a space, but a lenient upstream
// takes either, and the whole value would be a needle no URL holds.
func authorizationCredentials(values []string) []string {
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if i := strings.IndexAny(v, " \t"); i >= 0 {
			v = strings.TrimLeft(v[i:], " \t")
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
