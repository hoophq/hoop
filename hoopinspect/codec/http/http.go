// Package http inspects HTTP requests and responses.
//
// # Relation to Envoy
//
// Envoy's ext_authz already hands OPA the method, path and headers of a
// request, and can buffer a bounded slice of the request body. For "may alice
// call POST /admin", that is enough, and this package does not replace it.
//
// This package adds two things ext_authz leaves out:
//
//  1. **Responses.** ext_authz is request-side by construction: it decides
//     before the upstream is called. The response body is where data leaves
//     the building.
//  2. **Stable resource identity.** `/users/12345/orders` and
//     `/users/67890/orders` are the same resource; keying policy on raw paths
//     means a regex per endpoint.
//
// # Two entry points
//
// HTTP arrives in two shapes the wire-protocol codecs never see, so this
// package offers both:
//
//   - Codec implements hoopinspect.Codec over a raw byte stream, for a
//     caller holding a socket (the hoop-inspect relay, the hoop agent's
//     packet stream).
//   - Inspector takes an already-parsed *net/http Request or Response, for
//     a caller inside an HTTP pipeline (libhoop's ReverseProxy, an ext_proc
//     server). No re-parsing, no second copy of the body.
//
// Inspector is the one that matters for libhoop: `ReverseProxy` already has
// `*http.Request` in `inspectHandler` and `*http.Response` in
// `modifyResponse`, with the body buffered. Handing those straight to
// InspectRequest / InspectResponse costs one struct build.
package http

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/hoophq/hoopinspect"
)

func init() {
	hoopinspect.Register(func() hoopinspect.Codec { return New(Options{}) })
}

// Options configures what an inspector captures.
//
// The defaults expose nothing: no bodies, no headers. A policy engine's
// decision log is a copy of everything you send it, so "we forwarded every
// Authorization header to OPA" turns up in your next audit. Opt in
// per-field.
type Options struct {
	// CaptureBody includes request and response bodies in the Statement.
	// Off by default.
	CaptureBody bool

	// MaxBodyBytes truncates a captured body. Ignored when CaptureBody is
	// false. Defaults to DefaultMaxBodyBytes.
	MaxBodyBytes int

	// Headers names the headers to expose to policy, matched
	// case-insensitively. Anything not listed is dropped, and there is no
	// "capture all" switch.
	Headers []string
}

// DefaultMaxBodyBytes bounds a captured body. A policy that needs more than
// 64 KiB of body is doing content scanning, which belongs in a redaction
// engine rather than an authorization decision.
const DefaultMaxBodyBytes = 64 << 10

// maxHeaderBytes bounds the head of a message during stream decoding.
const maxHeaderBytes = 1 << 20

// ErrMalformed means the stream is not valid HTTP/1.x.
var ErrMalformed = errors.New("hoopinspect/http: malformed message")

// Inspector converts parsed HTTP messages into Statements. It holds no
// per-connection state and is safe for concurrent use.
type Inspector struct {
	opts    Options
	headers map[string]bool // lowercased allowlist
}

// New returns an Inspector.
func New(opts Options) *Inspector {
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	h := make(map[string]bool, len(opts.Headers))
	for _, name := range opts.Headers {
		lower := strings.ToLower(name)
		// The correlation header is captured into its own field and is
		// never content. Allowlisting it would put it in Headers as well,
		// where it would reach policy rules, audit records and model
		// prompts — so the allowlist cannot take it, whatever the config
		// says. The sidecar refuses it at startup with an explanation;
		// this is the library-level guarantee behind that.
		if lower == strings.ToLower(hoopinspect.CorrelationHeader) {
			continue
		}
		h[lower] = true
	}
	return &Inspector{opts: opts, headers: h}
}

func (i *Inspector) Protocol() hoopinspect.Protocol { return hoopinspect.HTTP }

// InspectRequest builds a Statement from a parsed request.
//
// body is the already-buffered body. Pass nil when there is none, or when the
// caller has not buffered it. This function never reads r.Body, so it cannot
// consume a body the caller still needs.
//
// libhoop's ReverseProxy.inspectHandler enters here, holding both values
// already.
func (i *Inspector) InspectRequest(r *http.Request, body []byte) hoopinspect.Statement {
	return i.inspectRequest(r, drained{data: body})
}

func (i *Inspector) inspectRequest(r *http.Request, b drained) hoopinspect.Statement {
	path := r.URL.Path
	if path == "" {
		path = "/"
	}

	d := &hoopinspect.HTTPDetail{
		Method:      strings.ToUpper(r.Method),
		Path:        path,
		Host:        requestHost(r),
		Resource:    NormalizePath(path),
		ContentType: contentType(r.Header),
		Headers:     i.pickHeaders(r.Header),
		// Always read, never allowlisted: see HTTPDetail.CorrelationID.
		CorrelationID: strings.TrimSpace(r.Header.Get(hoopinspect.CorrelationHeader)),
	}
	if q := r.URL.Query(); len(q) > 0 {
		d.Query = q
	}
	i.attachBody(d, b)

	return hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Direction: hoopinspect.FromClient,
		Text:      d.Method + " " + r.URL.RequestURI(),
		Operation: operationFor(d),
		Tables:    resourceTables(d),
		HTTP:      d,
		Metadata:  map[string]string{"http.proto": r.Proto},
	}
}

// InspectResponse builds a Statement from a parsed response.
//
// req may be nil; when supplied, the method, path and resource of the
// originating request are carried onto the response Statement so a policy can
// say "no 200 with a body on GET /users/*/ssn" in one rule.
//
// libhoop's ReverseProxy.modifyResponse enters here, with resp.Request
// available.
func (i *Inspector) InspectResponse(resp *http.Response, req *http.Request, body []byte) hoopinspect.Statement {
	return i.inspectResponse(resp, req, drained{data: body})
}

func (i *Inspector) inspectResponse(resp *http.Response, req *http.Request, b drained) hoopinspect.Statement {
	d := &hoopinspect.HTTPDetail{
		StatusCode:  resp.StatusCode,
		ContentType: contentType(resp.Header),
		Headers:     i.pickHeaders(resp.Header),
	}
	if req == nil {
		req = resp.Request
	}
	if req != nil && req.URL != nil {
		path := req.URL.Path
		if path == "" {
			path = "/"
		}
		d.Method = strings.ToUpper(req.Method)
		d.Path = path
		d.Host = requestHost(req)
		d.Resource = NormalizePath(path)
	}
	i.attachBody(d, b)

	return hoopinspect.Statement{
		Protocol:  hoopinspect.HTTP,
		Direction: hoopinspect.FromServer,
		Text:      resp.Status,
		Operation: operationFor(d),
		Tables:    resourceTables(d),
		HTTP:      d,
		Metadata:  map[string]string{"http.proto": resp.Proto},
	}
}

// Decode implements hoopinspect.Codec for a raw HTTP/1.x byte stream.
//
// A caller holding a socket rather than a parsed message enters here. HTTP/2
// and HTTP/3 framing belongs to whatever terminated the connection, which by
// then has a *http.Request: use InspectRequest.
func (i *Inspector) Decode(dir hoopinspect.Direction, data []byte) ([]hoopinspect.Statement, int, error) {
	var stmts []hoopinspect.Statement
	pos := 0

	for pos < len(data) {
		rest := data[pos:]

		// Nothing can be decided before the head is complete.
		headEnd := headEndIndex(rest)
		if headEnd < 0 {
			if len(rest) > maxHeaderBytes {
				return stmts, pos, ErrMalformed
			}
			return stmts, pos, nil // wait for the rest of the head
		}

		br := bufio.NewReader(bytes.NewReader(rest))

		if dir == hoopinspect.FromServer {
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				return stmts, pos, ErrMalformed
			}
			b, complete := drainBody(resp.Body, i.opts.MaxBodyBytes)
			resp.Body.Close()
			if !complete {
				return stmts, pos, nil // body still arriving
			}
			consumed := len(rest) - br.Buffered()
			stmts = append(stmts, i.inspectResponse(resp, nil, b))
			pos += consumed
			continue
		}

		req, err := http.ReadRequest(br)
		if err != nil {
			return stmts, pos, ErrMalformed
		}
		b, complete := drainBody(req.Body, i.opts.MaxBodyBytes)
		req.Body.Close()
		if !complete {
			return stmts, pos, nil
		}
		consumed := len(rest) - br.Buffered()
		stmts = append(stmts, i.inspectRequest(req, b))
		pos += consumed
	}

	return stmts, pos, nil
}

// headEndIndex returns the index just past the blank line terminating the
// message head, or -1 when it has not arrived. Both CRLFCRLF and LFLF are
// accepted; real clients send the former, but a hand-rolled one may not.
func headEndIndex(b []byte) int {
	if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
		return i + 4
	}
	if i := bytes.Index(b, []byte("\n\n")); i >= 0 {
		return i + 2
	}
	return -1
}

// drained is one message body as drainBody recovered it: the retained
// prefix, plus whether bytes followed it.
//
// The flag has to travel with the bytes. A caller cannot infer truncation
// from the length, because a body cut at the limit and a body that happens
// to end there are the same slice.
type drained struct {
	data      []byte
	truncated bool
}

// drainBody reads a body, retaining at most limit bytes while still
// consuming the rest so the reader lands on the next message boundary.
// complete is false when the body was cut short, meaning more bytes are on
// the way.
//
// The remainder past limit goes to io.Discard rather than into a buffer that
// is then sliced away: the limit exists to bound what this package holds in
// memory, and a 2 GiB upload must not allocate 2 GiB to throw it away.
func drainBody(rc io.ReadCloser, limit int) (b drained, complete bool) {
	if rc == nil {
		return drained{}, true
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, int64(limit))); err != nil {
		// An unexpected EOF means the body is still in flight.
		return drained{}, false
	}
	rest, err := io.Copy(io.Discard, rc)
	if err != nil {
		return drained{}, false
	}
	return drained{data: buf.Bytes(), truncated: rest > 0}, true
}

// attachBody records the body on the detail, flagging a prefix as such.
//
// Truncation arrives two ways and both must set the flag: drainBody reports
// it for a stream it capped itself, and a caller entering at InspectRequest
// hands over a whole body that this function caps here.
func (i *Inspector) attachBody(d *hoopinspect.HTTPDetail, b drained) {
	if !i.opts.CaptureBody || len(b.data) == 0 {
		return
	}
	if len(b.data) > i.opts.MaxBodyBytes {
		d.Body = string(b.data[:i.opts.MaxBodyBytes])
		d.BodyTruncated = true
		return
	}
	d.Body = string(b.data)
	d.BodyTruncated = b.truncated
}

// pickHeaders returns only the allowlisted headers, and nil when the
// allowlist is empty, so the field drops out of the policy input rather than
// serializing as {}.
func (i *Inspector) pickHeaders(h http.Header) map[string]string {
	if len(i.headers) == 0 {
		return nil
	}
	out := map[string]string{}
	for name, vals := range h {
		if !i.headers[strings.ToLower(name)] || len(vals) == 0 {
			continue
		}
		out[strings.ToLower(name)] = strings.Join(vals, ", ")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// operationFor picks the Statement operation from the request method.
func operationFor(d *hoopinspect.HTTPDetail) hoopinspect.Operation {
	switch d.Method {
	case "GET":
		return hoopinspect.OpGet
	case "POST":
		return hoopinspect.OpPost
	case "PUT":
		return hoopinspect.OpPut
	case "PATCH":
		return hoopinspect.OpPatch
	case "DELETE":
		return hoopinspect.OpDelete
	case "HEAD":
		return hoopinspect.OpHead
	case "OPTIONS":
		return hoopinspect.OpOptions
	case "CONNECT":
		return hoopinspect.OpConnect
	case "TRACE":
		return hoopinspect.OpTrace
	case "":
		return hoopinspect.OpUnknown
	}
	return hoopinspect.OpOther
}

// resourceTables exposes the normalized resource through Statement.Tables so
// a policy.MatchTable rule works uniformly across SQL and HTTP.
func resourceTables(d *hoopinspect.HTTPDetail) []string {
	if d.Resource == "" {
		return nil
	}
	return []string{strings.ToLower(d.Resource)}
}

func contentType(h http.Header) string {
	ct := h.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

func requestHost(r *http.Request) string {
	if r.Host != "" {
		return r.Host
	}
	if r.URL != nil {
		return r.URL.Host
	}
	return ""
}

// ParseQuery is a thin wrapper so callers holding a raw query string can get
// the same parsed shape the codec produces.
func ParseQuery(rawQuery string) map[string][]string {
	v, err := url.ParseQuery(rawQuery)
	if err != nil || len(v) == 0 {
		return nil
	}
	return v
}
