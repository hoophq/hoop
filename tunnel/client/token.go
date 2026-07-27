// token.go — shared token plumbing for the REST helpers.
//
// The gateway's HTTP auth middleware rotates access tokens via the
// X-New-Access-Token response header (reactively after expiry, and —
// behind the experimental.tunnel_token_renewal flag — proactively within
// a pre-expiry window). Every REST helper in this package harvests that
// header so the daemon can persist the fresh token and keep long-lived
// tunnel sessions alive without the user ever reopening a browser
// (DEP-24).
package client

import (
	"errors"
	"net/http"
)

// ErrUnauthorized is returned (wrapped) when the gateway rejects the
// bearer token with 401. It means the server-side refresh also failed —
// the session is dead and only a fresh login can recover it. Callers
// use errors.Is to distinguish this terminal state from transient
// fetch failures.
var ErrUnauthorized = errors.New("gateway rejected the access token (401 unauthorized)")

// newAccessTokenHeader is the rotation header emitted by the gateway's
// auth middleware and consumed by every hoop client (webapp, hsh CLI,
// and this daemon).
const newAccessTokenHeader = "X-New-Access-Token"

// harvestRotatedToken invokes onNewToken when the response carries a
// rotated access token. No-op when the header is absent or no callback
// was provided.
func harvestRotatedToken(resp *http.Response, onNewToken func(string)) {
	if onNewToken == nil {
		return
	}
	if newToken := resp.Header.Get(newAccessTokenHeader); newToken != "" {
		onNewToken(newToken)
	}
}
