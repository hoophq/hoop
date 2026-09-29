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
//     when a transparent MITM routes it back in.
package http

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	codechttp "github.com/hoophq/libhoop/v2/codec/http"
)

// Options configures one lane's HTTP codec.
//
// Every field but CredentialHeader is forwarded to libhoop's
// codechttp.Options unchanged; see there for their defaults.
type Options struct {
	CaptureBody          bool
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
}

// New builds an HTTP codec.
//
// It returns the concrete type rather than inspect.Codec because the gate
// finds its optional capabilities (Filter, TakeCredential, Reframer) by type
// assertion, and a caller holding a parsed *http.Request wants
// InspectRequest; narrowing to the interface here would hide all of them.
func New(o Options) *Codec {
	header := strings.ToLower(o.CredentialHeader)
	headers := o.Headers
	keep := false
	if header != "" {
		keep = slices.ContainsFunc(o.Headers, func(h string) bool { return strings.EqualFold(h, header) })
		if !keep {
			// libhoop only reports allowlisted headers, so the credential
			// has to be allowlisted to be lifted at all. Clip first: an
			// append into the caller's spare capacity would change a
			// slice the caller still owns.
			headers = append(slices.Clip(o.Headers), header)
		}
	}
	return &Codec{
		Inspector: codechttp.New(codechttp.Options{
			CaptureBody:          o.CaptureBody,
			MaxBodyBytes:         o.MaxBodyBytes,
			Headers:              headers,
			SensitiveQueryParams: o.SensitiveQueryParams,
			MaxMessageBytes:      o.MaxMessageBytes,
		}),
		creds: credentials{header: header, keep: keep},
	}
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
func (c *Codec) Decode(dir inspect.Direction, data []byte) ([]inspect.Statement, int, error) {
	stmts, n, err := c.Inspector.Decode(dir, data)
	var liftErr error
	for i := range stmts {
		stripConnectGatewayPrefix(&stmts[i])
		if e := c.creds.lift(&stmts[i]); e != nil && liftErr == nil {
			liftErr = e
		}
	}
	if liftErr != nil {
		err = errors.Join(liftErr, err)
	}
	return stmts, n, err
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
	stripConnectGatewayPrefix(&stmt)
	c.creds.scrub(&stmt)
	return stmt
}

// InspectResponse is libhoop's InspectResponse with the same fixups as
// InspectRequest.
func (c *Codec) InspectResponse(resp *http.Response, req *http.Request, body []byte) inspect.Statement {
	stmt := c.Inspector.InspectResponse(resp, req, body)
	stripConnectGatewayPrefix(&stmt)
	c.creds.scrub(&stmt)
	return stmt
}

// Filter is the gate's StreamFilter: it marks client requests with Via and
// refuses one that already carries this process's mark. The server stream
// is returned unchanged. See via.go.
func (c *Codec) Filter(dir inspect.Direction, data []byte) ([]byte, error) {
	if dir != inspect.FromClient {
		return data, nil
	}
	return c.via.filter(data)
}

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
// instances and never see a WebSocket message the client sends. Pin them at
// compile time instead, against an upstream change to the embedded type.
var _ interface {
	inspect.Codec
	Duplex()
	EnableRewrite()
	Filter(inspect.Direction, []byte) ([]byte, error)
	TakeCredential(*inspect.Statement) (string, bool)
} = (*Codec)(nil)

func init() {
	inspect.Register(func() inspect.Codec { return New(Options{}) })
}
