package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/hoophq/hoop/sidecar/inspect"
	codechttp "github.com/hoophq/libhoop/v2/codec/http"
)

// metadataCredential names the statement metadata key carrying a lifted
// credential's handle. The handle is a counter, never the value: metadata
// reaches policy input and the audit trail, and the value is a bearer token.
const metadataCredential = "hoop.credential"

// maxHeldCredentials bounds the credentials lifted and not yet taken.
//
// The gate takes each request's credential while it evaluates that request,
// so on a healthy connection the table holds a handful. A path that decodes
// requests and never takes them would otherwise grow it for the life of the
// connection, one bearer token per request; past the bound Decode refuses
// the stream instead.
const maxHeldCredentials = 1024

// credentials lifts one header out of request statements and holds its
// values until the gate trades each handle back.
//
// The codec is Duplex: the client pump decodes requests and the server pump
// decodes responses through the same instance, so the table has its own
// lock rather than leaning on the Inspector's, which is released by the time
// the statements come back.
type credentials struct {
	header string // lowercased; "" lifts nothing
	keep   bool   // the operator's own Options.Headers named header

	// authenticates is set on a lane that IS the authentication
	// (Options.AuthenticatesCaller). The request filter checks each head's
	// credential with check before forwarding any of it, then removes the
	// header; the gate decodes what the filter forwards, so the value is
	// handed over in request order (admit) and lift takes it in the same
	// order.
	authenticates bool

	mu     sync.Mutex
	check  func(credential string) error // installed by the gate; nil refuses every request
	next   uint64
	held   map[string]string
	handed []string // authenticates: values the filter admitted, oldest first
}

// admit checks the credential of one request head before the filter
// forwards it, and hands the value on for lift. The value is the header
// joined with ", " when it was sent more than once, as libhoop renders a
// repeated header, so the check sees what the gate's own check would.
func (c *credentials) admit(req *http.Request) error {
	value := c.presented(req)
	c.mu.Lock()
	check := c.check
	c.mu.Unlock()
	if check == nil {
		// The gate installs the check when it builds the codec; a codec
		// without one cannot tell a caller from a stranger.
		return errors.New("this listener authenticates its caller, and no caller check is installed; the connection is closed")
	}
	if err := check(value); err != nil {
		// The path normalized: the lane does not know this caller's token,
		// so cannot redact one a vault filled into the path, but it can
		// collapse the segments shaped like one.
		return &inspect.CredentialRefusal{Request: strings.TrimSpace(req.Method + " " + codechttp.NormalizePath(req.URL.Path)), Reason: err}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.handed) >= maxHeldCredentials {
		return fmt.Errorf("%w: %d request credentials were admitted and never decoded; refusing to hold more",
			inspect.ErrStreamUnsafe, maxHeldCredentials)
	}
	c.handed = append(c.handed, value)
	return nil
}

// presented is the credential one request head carries, as admit checks
// it.
func (c *credentials) presented(req *http.Request) string {
	return strings.Join(req.Header.Values(c.header), ", ")
}

// lift moves the credential header of one request statement into the table
// and leaves only its handle in stmt.Metadata.
//
// Every request gets a handle, including one that sent no such header: that
// stores "", so the gate can tell "a request without a credential" from "a
// statement that is not a request". A header sent twice arrives as libhoop
// joins it, "a, b", and is passed on unchanged for the resolver to refuse.
// Statements that are not requests (responses, WebSocket messages) get no
// handle, only the scrub.
func (c *credentials) lift(stmt *inspect.Statement) error {
	if c.header == "" || stmt.HTTP == nil {
		return nil
	}
	value := stmt.HTTP.Headers[c.header]
	c.scrub(stmt)
	d := stmt.HTTP
	if stmt.Direction != inspect.FromClient || d.WebSocket != nil || d.StatusCode != 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.authenticates {
		// One handed value per request head the filter admitted, and the
		// codec decodes exactly those heads. A request with none waiting
		// did not pass the filter, so nothing checked it before the
		// upstream saw it: refuse the stream rather than guess a value.
		if len(c.handed) == 0 {
			return fmt.Errorf("%w: a request reached the codec without passing the filter that removes its %s header",
				inspect.ErrStreamUnsafe, c.header)
		}
		value, c.handed = c.handed[0], c.handed[1:]
	}
	if len(c.held) >= maxHeldCredentials {
		return fmt.Errorf("%w: %d request credentials were lifted and never taken; refusing to hold more",
			inspect.ErrStreamUnsafe, maxHeldCredentials)
	}
	if c.held == nil {
		c.held = make(map[string]string)
	}
	c.next++
	handle := strconv.FormatUint(c.next, 10)
	c.held[handle] = value
	if stmt.Metadata == nil {
		stmt.Metadata = map[string]string{}
	}
	stmt.Metadata[metadataCredential] = handle
	return nil
}

// scrub removes the credential header from stmt's captured headers unless
// the operator allowlisted it: when only the lifting put it on libhoop's
// allowlist, nothing else may see it. An emptied map becomes nil, as libhoop
// reports no captured headers.
func (c *credentials) scrub(stmt *inspect.Statement) {
	d := stmt.HTTP
	if c.header == "" || c.keep || d == nil {
		return
	}
	delete(d.Headers, c.header)
	if len(d.Headers) == 0 {
		d.Headers = nil
	}
}

// take trades stmt's handle for the credential it stands for, removing both
// so the value is handed out once.
func (c *credentials) take(stmt *inspect.Statement) (string, bool) {
	if stmt == nil {
		return "", false
	}
	handle, ok := stmt.Metadata[metadataCredential]
	if !ok {
		return "", false
	}
	delete(stmt.Metadata, metadataCredential)

	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.held[handle]
	delete(c.held, handle)
	return value, ok
}
