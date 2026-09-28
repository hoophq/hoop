package http

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/hoophq/hoop/sidecar/inspect"
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

	mu   sync.Mutex
	next uint64
	held map[string]string
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
