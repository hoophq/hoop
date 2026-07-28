// Package http inspects HTTP requests and responses.
//
// # Where this sits relative to Envoy
//
// Envoy's ext_authz already hands OPA the method, path and headers of a
// request, and can buffer a bounded slice of the request body. For "may alice
// call POST /admin", that is enough, and this package is not trying to
// replace it.
//
// Three things it does not do:
//
//  1. **GraphQL.** Every GraphQL call is `POST /graphql`. Method and path
//     cannot separate a read from a schema-mutating write, because the
//     operation is in the body. A method-and-path policy either allows all
//     GraphQL or none.
//  2. **Responses.** ext_authz is request-side by construction — it decides
//     before the upstream is called. The response body is where data actually
//     leaves the building.
//  3. **Stable resource identity.** `/users/12345/orders` and
//     `/users/67890/orders` are the same resource; keying policy on raw paths
//     means a regex per endpoint.
//
// # Two entry points
//
// Unlike the wire-protocol codecs, HTTP arrives in two very different shapes,
// so this package offers both:
//
//   - Codec — implements hoopinspect.Codec over a raw byte stream, for a
//     caller holding a socket (the hoop agent's packet stream, an Envoy WASM
//     network filter).
//   - Inspector — takes an already-parsed *net/http Request or Response, for
//     a caller inside an HTTP pipeline (libhoop's ReverseProxy, an ext_proc
//     server). No re-parsing, no second copy of the body.
//
// The second is the one that matters for libhoop: `ReverseProxy` already has
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
// The defaults are deliberately conservative about data exposure: no bodies,
// no headers. A policy engine's decision log is a copy of everything you send
// it, and "we forwarded every Authorization header to OPA" is a finding, not
// a feature. Opt in per-field.
type Options struct {
	// CaptureBody includes request and response bodies in the Statement.
	// Off by default.
	CaptureBody bool

	// MaxBodyBytes truncates a captured body. Ignored when CaptureBody is
	// false. Defaults to DefaultMaxBodyBytes.
	MaxBodyBytes int

	// Headers names the headers to expose to policy, matched
	// case-insensitively. Anything not listed is dropped. There is no
	// "capture all" switch on purpose.
	Headers []string

	// ParseGraphQL parses a JSON body as a GraphQL document when the path or
	// the body shape suggests one. On by default via New; set
	// DisableGraphQL to turn it off.
	DisableGraphQL bool

	// GraphQLPaths restricts GraphQL parsing to these exact paths. Empty
	// means "any path whose JSON body has a `query` string field", which is
	// the reliable signal in practice.
	GraphQLPaths []string
}

// DefaultMaxBodyBytes bounds a captured body. A policy that needs more than
// 64 KiB of body is doing content scanning, which belongs in a redaction
// engine, not an authorization decision.
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
		h[strings.ToLower(name)] = true
	}
	return &Inspector{opts: opts, headers: h}
}

func (i *Inspector) Protocol() hoopinspect.Protocol { return hoopinspect.HTTP }

// InspectRequest builds a Statement from a parsed request.
//
// body is the already-buffered body. Pass nil when there is none, or when the
// caller has not buffered it — this function never reads r.Body, so it cannot
// consume a body the caller still needs.
//
// This is the entry point for libhoop's ReverseProxy.inspectHandler, which
// already has both values in hand.
func (i *Inspector) InspectRequest(r *http.Request, body []byte) hoopinspect.Statement {
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
	}
	if q := r.URL.Query(); len(q) > 0 {
		d.Query = q
	}
	i.attachBody(d, body)

	if !i.opts.DisableGraphQL && i.graphQLCandidate(path, d.ContentType, body) {
		if g := ParseGraphQL(body); g != nil {
			d.GraphQL = g
		}
	}

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
// This is the entry point for libhoop's ReverseProxy.modifyResponse, which
// has resp.Request available.
func (i *Inspector) InspectResponse(resp *http.Response, req *http.Request, body []byte) hoopinspect.Statement {
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
	i.attachBody(d, body)

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
// It is the entry point for a caller holding a socket rather than a parsed
// message. HTTP/2 and HTTP/3 are not handled here: their framing belongs to
// whatever terminated the connection, which by then has a *http.Request —
// use InspectRequest.
func (i *Inspector) Decode(dir hoopinspect.Direction, data []byte) ([]hoopinspect.Statement, int, error) {
	var stmts []hoopinspect.Statement
	pos := 0

	for pos < len(data) {
		rest := data[pos:]

		// A complete head is the prerequisite for deciding anything.
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
			body, complete := drainBody(resp.Body, i.opts.MaxBodyBytes)
			resp.Body.Close()
			if !complete {
				return stmts, pos, nil // body still arriving
			}
			consumed := len(rest) - br.Buffered()
			stmts = append(stmts, i.InspectResponse(resp, nil, body))
			pos += consumed
			continue
		}

		req, err := http.ReadRequest(br)
		if err != nil {
			return stmts, pos, ErrMalformed
		}
		body, complete := drainBody(req.Body, i.opts.MaxBodyBytes)
		req.Body.Close()
		if !complete {
			return stmts, pos, nil
		}
		consumed := len(rest) - br.Buffered()
		stmts = append(stmts, i.InspectRequest(req, body))
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

// drainBody reads a body, capping the retained bytes at limit while still
// consuming the rest so the reader lands on the next message boundary.
// complete is false when the body was cut short, meaning more bytes are on
// the way.
func drainBody(rc io.ReadCloser, limit int) (body []byte, complete bool) {
	if rc == nil {
		return nil, true
	}
	var buf bytes.Buffer
	_, err := io.Copy(&buf, rc)
	if err != nil {
		// An unexpected EOF means the body is still in flight.
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, false
		}
		return nil, false
	}
	b := buf.Bytes()
	if len(b) > limit {
		b = b[:limit]
	}
	return b, true
}

func (i *Inspector) attachBody(d *hoopinspect.HTTPDetail, body []byte) {
	if !i.opts.CaptureBody || len(body) == 0 {
		return
	}
	if len(body) > i.opts.MaxBodyBytes {
		d.Body = string(body[:i.opts.MaxBodyBytes])
		d.BodyTruncated = true
		return
	}
	d.Body = string(body)
}

// pickHeaders returns only the allowlisted headers. Returns nil when the
// allowlist is empty, so the field is omitted from the policy input entirely
// rather than serialized as {}.
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

// graphQLCandidate decides whether to attempt GraphQL parsing.
func (i *Inspector) graphQLCandidate(path, ctype string, body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if len(i.opts.GraphQLPaths) > 0 {
		for _, p := range i.opts.GraphQLPaths {
			if p == path {
				return true
			}
		}
		return false
	}
	// No configured paths: rely on the body shape. Anything JSON-ish is a
	// candidate; ParseGraphQL returns nil when the `query` field is absent.
	return ctype == "" || strings.Contains(ctype, "json") || strings.Contains(ctype, "graphql")
}

// operationFor picks the Statement operation.
//
// GraphQL wins over the HTTP method when present, because `mutation` is the
// answer to "is this a write" and `post` is not.
func operationFor(d *hoopinspect.HTTPDetail) hoopinspect.Operation {
	if d.GraphQL != nil && d.GraphQL.OperationType != "" {
		return d.GraphQL.OperationType
	}
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
// a policy.MatchTable rule works uniformly across SQL and HTTP. For GraphQL
// the root fields are the resources being touched, which is far more precise
// than the /graphql path.
func resourceTables(d *hoopinspect.HTTPDetail) []string {
	if d.GraphQL != nil && len(d.GraphQL.RootFields) > 0 {
		out := make([]string, 0, len(d.GraphQL.RootFields))
		for _, f := range d.GraphQL.RootFields {
			out = append(out, strings.ToLower(f))
		}
		return out
	}
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
