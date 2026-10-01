package daemon

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/gate"
	googleidentity "github.com/hoophq/hoop/sidecar/identity/google"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/proxy"
)

// GoogleIdentityConfig is the google_identity block on an http listener.
type GoogleIdentityConfig struct {
	// TokenInfoURL overrides Google's tokeninfo endpoint. Empty uses
	// https://oauth2.googleapis.com/tokeninfo. It exists for a network that
	// reaches Google APIs through Private Service Connect or a
	// private.googleapis.com VIP under another name; it must be https,
	// because the request body is the caller's bearer.
	TokenInfoURL string `json:"tokeninfo_url,omitempty"`
}

// googleTokenInfoTimeout bounds one tokeninfo call. The call sits on the
// request path, in front of a user's kubectl, so a Google outage must turn
// into a refused request in seconds rather than a hung one.
const googleTokenInfoTimeout = 10 * time.Second

func (g *GoogleIdentityConfig) validate() error {
	if g.TokenInfoURL == "" {
		return nil
	}
	u, err := url.Parse(g.TokenInfoURL)
	if err != nil {
		return fmt.Errorf("tokeninfo_url: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("tokeninfo_url %q must be an absolute https URL: the request carries the caller's bearer", g.TokenInfoURL)
	}
	if u.User != nil || u.RawQuery != "" {
		return errors.New("tokeninfo_url must carry no userinfo and no query string")
	}
	return nil
}

// credentialHeader is the request header an http lane lifts the caller's
// credential from, "" when the lane names no caller per request. The relay's
// first-request peek and the codec both read this one value, which is how
// proxy.NewServer can prove they agree.
//
// grpc and spanner lanes read identity_header themselves, per RPC, and never
// reach this.
func (l ListenerConfig) credentialHeader() string {
	if inspect.Protocol(l.Protocol) != inspect.HTTP || isGRPCTransport(l) || isSSH(l) {
		return ""
	}
	switch {
	case l.GoogleIdentity != nil:
		return "authorization"
	case l.IdentityHeader != "":
		return strings.ToLower(strings.TrimSpace(l.IdentityHeader))
	}
	return ""
}

// buildRequestIdentity returns the resolver for credentialHeader's value,
// nil when the lane has none. roots is the process trust pool, so a Google
// API reached through an intercepting proxy verifies against its CA.
func buildRequestIdentity(l ListenerConfig, roots *x509.CertPool) (gate.RequestIdentity, error) {
	switch {
	case l.credentialHeader() == "":
		return nil, nil
	case l.GoogleIdentity != nil:
		client := *outboundHTTPClient(roots)
		client.Timeout = googleTokenInfoTimeout
		r, err := googleidentity.New(googleidentity.Options{
			TokenInfoURL: l.GoogleIdentity.TokenInfoURL,
			Client:       &client,
		})
		if err != nil {
			return nil, fmt.Errorf("google_identity: %w", err)
		}
		return r, nil
	}
	return proxy.HeaderIdentity{}, nil
}
