// Package clickhouse registers the ClickHouse native-protocol codec with the
// inspect registry.
//
// The decoder lives in github.com/hoophq/libhoop/v2/codec/clickhouse. This
// package is the dependency seam: it injects sidecar's SQL classifier and its
// ClickHouse lexer without making libhoop import the main project.
package clickhouse

import (
	"errors"
	"strconv"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	"github.com/hoophq/hoop/sidecar/session"
	codecclickhouse "github.com/hoophq/libhoop/v2/codec/clickhouse"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// Options configures one connection-scoped codec.
type Options struct {
	// MaxFrameBytes and MaxBlockBytes cap one compressed frame and one
	// decompressed block. Zero selects libhoop's bounded default.
	MaxFrameBytes, MaxBlockBytes int

	// Authenticate terminates the login carried by the client Hello and
	// names the upstream credential. nil keeps the lane pass-through, byte
	// for byte.
	Authenticate codectypes.Authenticator

	// Passthrough reports a login user that keeps its own credential on a
	// lane with Authenticate. nil lets no user through.
	Passthrough func(user string) bool
}

// New builds one connection-scoped codec with conservative memory defaults.
// A listener that needs larger legitimate blocks uses NewWithLimits; neither
// path permits an unbounded decompression size.
func New() inspect.Codec {
	return NewWithLimits(0, 0)
}

// NewWithLimits builds one connection-scoped codec with explicit compressed
// frame and decompressed block caps.
func NewWithLimits(maxFrameBytes, maxBlockBytes int) inspect.Codec {
	return NewWithOptions(Options{MaxFrameBytes: maxFrameBytes, MaxBlockBytes: maxBlockBytes})
}

// NewWithOptions builds one connection-scoped codec. With Authenticate set
// the codec also implements LoginIdentity, which the gate reads after the
// first client Filter call that returned the Hello.
func NewWithOptions(o Options) inspect.Codec {
	c := codecclickhouse.New(codecclickhouse.Options{
		Analyze:       inspect.AnalyzeSQL,
		Split:         split,
		MaxFrameBytes: o.MaxFrameBytes,
		MaxBlockBytes: o.MaxBlockBytes,
		Authenticate:  o.Authenticate,
		Passthrough:   o.Passthrough,
	})
	if o.Authenticate == nil {
		return c
	}
	return &terminating{Codec: c}
}

// terminating is the codec of a lane that terminates the login. It turns
// libhoop's refusal into the gate's identity refusal and the terminated
// login into a session identity.
type terminating struct {
	*codecclickhouse.Codec
}

// Filter runs libhoop's Hello filter. A refused login comes back as the
// CredentialRefusal the gate records under rule "identity"; the relay then
// writes the ClickHouse exception frame and closes.
func (c *terminating) Filter(dir inspect.Direction, data []byte) ([]byte, error) {
	out, err := c.Codec.Filter(dir, data)
	var login *codecclickhouse.LoginError
	if errors.As(err, &login) && errors.Is(err, codectypes.ErrLoginRefused) {
		return nil, &inspect.CredentialRefusal{Request: "Hello " + login.User, User: login.User, Reason: err}
	}
	return out, err
}

// LoginIdentity reports the identity the Hello proved and the session
// metadata that describes the swap. ok is false until the first client
// Filter call returned the Hello.
func (c *terminating) LoginIdentity() (session.Identity, map[string]string, bool) {
	login, ok := c.Codec.LoginIdentity()
	if !ok {
		return session.Identity{}, nil, false
	}
	id := session.Identity{
		Subject:    login.Identity.Subject,
		Email:      login.Identity.Email,
		Groups:     login.Identity.Groups,
		Attributes: login.Identity.Attributes,
	}
	md := map[string]string{
		"upstream_user": login.UpstreamUser,
		"verified":      strconv.FormatBool(!login.Passthrough),
	}
	return id, md, true
}

func split(sql string, _ codectypes.Protocol) []string {
	return lexer.Split(sql, lexer.ClickHouse)
}

func init() {
	inspect.Register(func() inspect.Codec { return New() })
}
