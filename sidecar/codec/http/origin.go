package http

import (
	"bytes"
	"net/http"
	"strings"
)

// Origin makes a lane a reverse proxy in front of one origin server, the
// way Caddy's reverse_proxy and nginx's proxy_pass behave by default.
//
// # Why
//
// A browser pointed at the sidecar sends Host: 127.0.0.1:19999, the
// address it typed. Forwarded unchanged, that Host reaches an origin that
// serves virtual hosts or sits behind a CDN, and the origin answers for a
// site it does not know: Cloudflare refuses it outright. A client like curl
// can be told which Host to send; a browser cannot. So every request leaves
// naming the origin, and the name the client used travels in
// X-Forwarded-Host, which is where an application that builds absolute URLs
// looks for it.
//
// # What it does not do
//
// Nothing in a response changes: no Location, Set-Cookie domain or body
// rewrite. An application that redirects to its own absolute URL, or scopes
// a cookie to its own domain, takes the browser away from the sidecar or
// loses the cookie. That is the same limit Caddy and nginx have, and fixing
// it means editing what the origin wrote.
//
// Origin and Referer pass as the client wrote them. A WebSocket handshake
// gets the same head rewrite as any request, and the filter stops only once
// the server accepts the switch, so frames pass byte-exact while a refused
// upgrade leaves the next request rewritten like any other.
//
// # Fail closed
//
// The rewrite is a promise the origin may rely on: the Host and the
// X-Forwarded fields it reads came from the sidecar. So a request the filter
// cannot frame is refused, never forwarded as written, which is the opposite
// of the Via-only lane's choice (via.go).
type Origin struct {
	// Host is the Host every forwarded request names: the origin's host,
	// with its port only when the port is not the scheme's default.
	Host string

	// Proto is the scheme the client reached the sidecar with, "http" or
	// "https", sent as X-Forwarded-Proto.
	Proto string
}

// replacedFields are the fields Origin writes itself. A client's own copy
// is removed, never trusted: a forged X-Forwarded-Host would otherwise reach
// the origin as if the sidecar had vouched for it.
var replacedFields = []string{"Host", "X-Forwarded-Host", "X-Forwarded-Proto"}

// writeHead emits fields, a request head without its closing empty line,
// with the Host replaced and the X-Forwarded fields set; eol ends every
// added line. req is the same head as net/http parsed it.
//
// Host goes first after the request line, where clients put it and RFC 9112
// 3.2 recommends it. Every other line keeps its place and its bytes, a
// folded continuation line included: it follows the field it belongs to.
//
// An absolute-form target (GET http://other/x) is rewritten to origin-form
// (GET /x): a recipient takes the host from such a target ahead of Host
// (RFC 9112 3.2.2), so leaving it would let the client pick another virtual
// host on the origin. X-Forwarded-Host carries the Host field the client
// sent, not the target's host, and is left out when it sent none.
func (o *Origin) writeHead(out *splice, fields []byte, req *http.Request, eol []byte) {
	line, rest := cutLine(fields)
	if req.Method != http.MethodConnect && req.URL.IsAbs() {
		out.addf("%s %s %s", req.Method, req.URL.RequestURI(), req.Proto)
		out.add(lineEnd(line))
	} else {
		out.add(line)
	}
	out.addf("Host: %s", o.Host)
	out.add(eol)
	clientHost := ""
	eachField(rest, func(name string, value, whole []byte) {
		switch {
		case !isReplacedField(name):
			out.add(whole)
		case clientHost == "" && strings.EqualFold(name, "Host"):
			clientHost = string(bytes.TrimSpace(value))
		}
	})
	if clientHost != "" {
		out.addf("X-Forwarded-Host: %s", clientHost)
		out.add(eol)
	}
	out.addf("X-Forwarded-Proto: %s", o.Proto)
	out.add(eol)
}

// lineEnd is the line ending line closes with: CRLF, LF, or none.
func lineEnd(line []byte) []byte {
	if bytes.HasSuffix(line, []byte("\r\n")) {
		return line[len(line)-2:]
	}
	if bytes.HasSuffix(line, []byte("\n")) {
		return line[len(line)-1:]
	}
	return nil
}

func isReplacedField(name string) bool {
	for _, f := range replacedFields {
		if strings.EqualFold(name, f) {
			return true
		}
	}
	return false
}

// eachField calls yield once per header field in fields, header lines with
// the request line already cut off: the field's name, the value on its first
// line, and all of its bytes, the folded continuation lines that follow it
// included. Which line belongs to which field is decided here once, for every
// rewrite that drops a field. Lines end at LF, as net/http reads them, and
// the head already parsed, so a name has no whitespace before its colon.
func eachField(fields []byte, yield func(name string, value, whole []byte)) {
	for len(fields) > 0 {
		line, rest := cutLine(fields)
		n := len(line)
		for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
			cont, more := cutLine(rest)
			n, rest = n+len(cont), more
		}
		name, value, _ := bytes.Cut(line, []byte(":"))
		yield(string(name), value, fields[:n])
		fields = rest
	}
}

// cutLine splits data after its first LF. A last line without one is
// returned whole.
func cutLine(data []byte) (line, rest []byte) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return data[:i+1], data[i+1:]
	}
	return data, nil
}
