// Package http registers the HTTP codec with the inspect registry.
//
// The decoder itself lives in github.com/hoophq/libhoop/v2/codec/http. This
// package is the seam between the two: libhoop may not import sidecar, so
// it cannot register itself, and something on this side has to do it.
//
// Import it for its side effect when a binary should speak HTTP:
//
//	import _ "github.com/hoophq/hoop/sidecar/codec/http"
//
// Unlike the SQL codecs this one injects nothing into the decoder. HTTP
// operations come from the request method, which the codec reads off the
// wire itself; there is no classifier to hand it. What it adds instead sits
// AROUND libhoop's Inspector, because each piece is a relay concern libhoop
// has no reason to know about:
//
//   - Connect Gateway resource normalization (connectgateway.go): a request
//     through GKE Connect Gateway carries its cluster's Kubernetes path under
//     a project/location/membership prefix, which no Kubernetes rule would
//     otherwise match.
//   - Credential lifting (credential.go): the relay resolves an identity
//     from one request header, and that header's value must reach the
//     resolver without ever reaching policy input or the audit trail.
//   - The Via loop marker (via.go): RFC 9110 requires a proxy to announce
//     itself, and the same field lets the sidecar refuse its own traffic
//     when a transparent MITM routes it back in. The same head rewrite
//     removes a credential meant for the sidecar alone (a caller token).
//   - Credential placement (placement.go): the same filter refuses a
//     request that carries a credential the lane knows outside its header,
//     because a vault fills a token into a URL or a body too.
package http

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	codechttp "github.com/hoophq/libhoop/v2/codec/http"
)

// Options configures one lane's HTTP codec.
//
// Every field but CredentialHeader, AuthenticatesCaller, Origin and Seen is
// forwarded to libhoop's codechttp.Options unchanged; see there for their
// defaults.
type Options struct {
	CaptureBody bool

	// CaptureRequestBody is set by a lane that holds for review: the
	// reviewer must read the body the approval releases.
	CaptureRequestBody bool

	MaxBodyBytes         int
	Headers              []string
	SensitiveQueryParams []string
	MaxMessageBytes      int

	// CredentialHeader names the request header lifted out of every
	// request as its credential, matched case-insensitively. "" lifts
	// nothing. The value leaves the statement for a handle the gate trades
	// back through TakeCredential; it stays in HTTP.Headers only when
	// Headers names it too, because then the operator asked to see it.
	CredentialHeader string

	// AuthenticatesCaller makes the lane the authentication, for a
	// credential that is the sidecar's own (a caller token) rather than one
	// the upstream also reads (Authorization under google_identity). The
	// request filter checks CredentialHeader with the check the gate
	// installs (AuthenticateCallers) before it forwards any byte of a
	// request, then removes the header, so the upstream never sees it. It
	// also makes the lane strict about what it forwards; see
	// viaFilter.strict. It needs CredentialHeader.
	AuthenticatesCaller bool

	// Origin, when set, makes the lane a reverse proxy in front of one
	// origin server: every request names it in Host. Nil forwards each
	// request's Host as the client wrote it. See origin.go.
	Origin *Origin

	// Seen is the lane's memory of the Authorization credentials its
	// upstream accepted, which the request filter keeps out of every
	// request's URL, body and other headers (placement.go). Every codec of
	// one lane must share one, or a credential learned on one connection
	// is not watched on the next. Nil gives the codec a memory of its own.
	Seen *SeenCredentials

	// SQL, when set, reads the SQL out of JSON request bodies into
	// statements of their own. It captures the bodies of requests to its
	// routes; other requests keep theirs only under CaptureBody or
	// CaptureRequestBody. See sqlbody.go.
	SQL *SQLOptions
}

// Codec is libhoop's HTTP Inspector with the relay's additions.
//
// The Inspector is EMBEDDED, not wrapped behind an interface, so everything
// the gate discovers by type assertion keeps being found: Duplex (one
// instance for both directions), Protocol, and the Reframer surface
// (Rewrite, Flush, EnableRewrite) masking needs. Decode, InspectRequest and
// InspectResponse are shadowed so every statement, whichever entry point
// built it, gets the same normalization and the same credential handling.
type Codec struct {
	*codechttp.Inspector

	creds credentials
	via   viaFilter
	sql   *sqlReader
	// keepBodies is whether the operator asked for request bodies. The
	// Inspector captures every request body on a lane that reads SQL, and
	// withSQL drops those that reach no SQL route unless this is set.
	keepBodies bool
}

// New builds an HTTP codec.
//
// It returns the concrete type rather than inspect.Codec because the gate
// finds its optional capabilities (Filter, TakeCredential, Reframer) by type
// assertion, and a caller holding a parsed *http.Request wants
// InspectRequest; narrowing to the interface here would hide all of them.
func New(o Options) *Codec {
	header := strings.ToLower(o.CredentialHeader)
	if o.AuthenticatesCaller && header == "" {
		// A programmer error, not a config one: daemon validates the
		// header before building a codec. A lane with nothing to check
		// would forward every caller.
		panic("sidecar/codec/http: AuthenticatesCaller needs a CredentialHeader")
	}
	headers := o.Headers
	keep := false
	if header != "" {
		listed := slices.ContainsFunc(o.Headers, func(h string) bool { return strings.EqualFold(h, header) })
		// A caller token is the sidecar's own secret, so it never stays in
		// the captured headers, whoever allowlisted the name.
		keep = listed && !o.AuthenticatesCaller
		if !listed {
			// libhoop only reports allowlisted headers, so the credential
			// has to be allowlisted to be lifted at all. Clip first: an
			// append into the caller's spare capacity would change a
			// slice the caller still owns.
			headers = append(slices.Clip(o.Headers), header)
		}
	}
	c := &Codec{
		Inspector: codechttp.New(codechttp.Options{
			CaptureBody:          o.CaptureBody,
			CaptureRequestBody:   o.CaptureRequestBody || o.SQL != nil,
			MaxBodyBytes:         o.MaxBodyBytes,
			Headers:              headers,
			SensitiveQueryParams: o.SensitiveQueryParams,
			MaxMessageBytes:      o.MaxMessageBytes,
			// A lane with a mask rule promises the operator masked
			// responses. A body too large to mask, held or inflated, is
			// refused rather than forwarded as it came: a gzip body is small
			// on the wire, so the server, not the limit, decides whether it
			// reaches the client unmasked.
			RefuseUnmaskable: true,
		}),
		creds: credentials{header: header, keep: keep, authenticates: o.AuthenticatesCaller},
		via:   viaFilter{origin: o.Origin},
		sql:   newSQLReader(o.SQL),

		keepBodies: o.CaptureBody || o.CaptureRequestBody,
	}
	c.via.guard.seen = o.Seen
	if c.via.guard.seen == nil {
		c.via.guard.seen = NewSeenCredentials()
	}
	if o.AuthenticatesCaller {
		c.via.auth = &c.creds
	}
	return c
}

// AuthenticateCallers installs the check the request filter runs on every
// request's credential before forwarding it: the gate's own resolver, so the
// filter and the gate admit the same callers. gate.New calls it once, before
// the first byte. It refuses a codec built without AuthenticatesCaller,
// whose filter would never run the check.
func (c *Codec) AuthenticateCallers(check func(credential string) error) error {
	if !c.creds.authenticates {
		return errors.New("sidecar/codec/http: this codec does not authenticate its caller; build it with AuthenticatesCaller")
	}
	c.creds.mu.Lock()
	defer c.creds.mu.Unlock()
	c.creds.check = check
	return nil
}

// CredentialHeader is the lowercased header this codec lifts from every
// request, "" when it lifts none. The relay compares it with its own
// configuration at startup, so the two cannot disagree about which header
// carries the credential.
func (c *Codec) CredentialHeader() string { return c.creds.header }

// Decode is libhoop's Decode followed by the relay's statement fixups.
//
// A lifting failure is reported even when the decoder itself also failed,
// joined rather than replaced: it wraps inspect.ErrStreamUnsafe, which is
// what makes the gate refuse the connection instead of forwarding under the
// honest default it applies to a merely malformed stream.
//
// Every response's status also reaches the request filter, which on an
// origin lane passes a connection through only once the server grants the
// switch a request asked for. The gate decodes a response before it
// forwards it, so the filter knows before the client can act on it.
//
// On a lane that reads SQL bodies, each client request is followed by the
// SQL statements its body carries (sqlbody.go). A request the lane refuses
// for its body is reported as an error wrapping inspect.ErrStreamUnsafe, the
// same way a lifting failure is.
func (c *Codec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	stmts, n, err := c.Inspector.Decode(dir, data)
	var liftErr error
	for i := range stmts {
		if dir == inspect.FromServer && stmts[i].HTTP != nil && stmts[i].HTTP.StatusCode != 0 {
			c.via.answer(stmts[i].HTTP.StatusCode)
		}
		cleanResource(&stmts[i])
		stripConnectGatewayPrefix(&stmts[i])
		if e := c.creds.lift(&stmts[i]); e != nil && liftErr == nil {
			liftErr = e
		}
	}
	if liftErr != nil {
		err = errors.Join(liftErr, err)
	}
	if c.sql != nil && dir == inspect.FromClient {
		var sqlErr error
		stmts, sqlErr = c.withSQL(stmts)
		if sqlErr != nil {
			err = errors.Join(sqlErr, err)
		}
	}
	return stmts, n, err
}

// withSQL places each request's SQL statements right after it. The order is
// load-bearing: the gate resolves the caller and rotates the session on the
// request statement, so the SQL that follows is judged under that caller.
//
// A request to no SQL route drops the body the lane captured only to look
// for SQL, so a secret posted to another API reaches neither the audit trail
// nor an analyzer unless the operator asked for bodies.
func (c *Codec) withSQL(stmts []inspect.Statement) ([]inspect.Statement, error) {
	var out []inspect.Statement
	for i := range stmts {
		out = append(out, stmts[i])
		d := stmts[i].HTTP
		if d == nil || d.Method == "" || d.StatusCode != 0 {
			continue // not a request: a WebSocket message, a response
		}
		field, ok := c.sql.route(d)
		if !ok {
			if !c.keepBodies {
				d.Body, d.BodyTruncated = "", false
			}
			continue
		}
		sql, err := c.sql.extract(d, field)
		if err != nil {
			return out, err
		}
		if len(sql) == 0 {
			continue
		}
		req := &out[len(out)-1]
		if req.Metadata == nil {
			req.Metadata = map[string]string{}
		}
		req.Metadata[MetadataSQLStatements] = strconv.Itoa(len(sql))
		out = append(out, sql...)
	}
	return out, nil
}

// InspectRequest is libhoop's InspectRequest with the Connect Gateway
// normalization applied.
//
// It does NOT lift the credential into a handle: this entry point has no
// error return to fail closed through when the handle table is full, and a
// caller holding the *http.Request already has the header. The credential is
// still removed from HTTP.Headers when only the lifting put it on the
// allowlist, so it cannot reach policy input this way either.
func (c *Codec) InspectRequest(r *http.Request, body []byte) inspect.Statement {
	stmt := c.Inspector.InspectRequest(r, body)
	cleanResource(&stmt)
	stripConnectGatewayPrefix(&stmt)
	c.creds.scrub(&stmt)
	return stmt
}

// InspectResponse is libhoop's InspectResponse with the same fixups as
// InspectRequest.
func (c *Codec) InspectResponse(resp *http.Response, req *http.Request, body []byte) inspect.Statement {
	stmt := c.Inspector.InspectResponse(resp, req, body)
	cleanResource(&stmt)
	stripConnectGatewayPrefix(&stmt)
	c.creds.scrub(&stmt)
	return stmt
}

// Filter is the gate's StreamFilter: it marks client requests with Via and
// refuses one that already carries this process's mark, and with an Origin
// points each request at it. The server stream is returned unchanged. See
// via.go and origin.go.
func (c *Codec) Filter(dir inspect.Direction, data []byte) ([]byte, error) {
	if dir != inspect.FromClient {
		return data, nil
	}
	return c.via.filter(data)
}

// HoldsRequests is the gate's RequestHolder: a request leaves for the
// upstream only once it is whole and judged. libhoop decodes a request only
// with its body, and its head alone is enough for a route that acts on the
// method and path, so forwarding reads as they arrive would let a denied
// request run. A client WebSocket message is held the same way: libhoop
// consumes none of its frames before the final one arrives.
func (*Codec) HoldsRequests() {}

// TakeCredential returns the credential lifted from stmt and forgets it, so
// a second call for the same statement reports none. ok is false when stmt
// carries no handle from this codec. A request that had no credential header
// still carries a handle, for the empty value: ("", true) means "a request
// without a credential", which is not the same as "not a request".
func (c *Codec) TakeCredential(stmt *inspect.Statement) (string, bool) {
	return c.creds.take(stmt)
}

// The gate finds these by type assertion, so losing one is silent at
// runtime: without Duplex the relay would split the connection over two
// instances and never see a WebSocket message the client sends, and without
// HoldsRequests a denied request would reach the upstream in part. Pin them
// at compile time instead, against an upstream change to the embedded type.
var _ interface {
	inspect.Codec
	Duplex()
	EnableRewrite()
	HoldsRequests()
	Filter(inspect.Direction, []byte) ([]byte, error)
	TakeCredential(*inspect.Statement) (string, bool)
	AuthenticateCallers(func(string) error) error
} = (*Codec)(nil)

func init() {
	inspect.Register(func() inspect.Codec { return New(Options{}) })
}
