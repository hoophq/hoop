// tokenrenewal.go — daemon-side half of DEP-24 silent token renewal.
//
// The gateway rotates access tokens through the X-New-Access-Token
// response header (see tunnel/client/token.go). This file owns the
// daemon's side of that contract:
//
//   - tokenState is the single in-memory owner of the current bearer
//     token, shared between the tunnel manager (snapshots it per flow,
//     reports rotations harvested during its own REST calls) and the
//     daemon service (persists rotations, swaps it on login/logout).
//     Rotations are epoch-guarded so a slow response from a dead
//     session can never resurrect its token after logout/auth-expiry.
//   - StartTokenRenewal is a background scheduler that wakes shortly
//     before the current token expires and performs a cheap
//     authenticated call so the gateway's pre-expiry rotation window
//     can hand the daemon a fresh token — no browser, no webapp.
//   - When the gateway answers 401 the session is beyond saving (the
//     server-side refresh token itself is expired or revoked): the
//     daemon tears the tunnel down with an explicit reason instead of
//     limping along with a dead credential.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/tunnel/client"
)

// renewalLead is how long before expiry the scheduler performs the
// renewal call. It must stay comfortably inside the gateway's
// proactive rotation window (10 minutes) so a single wake-up is
// normally enough.
const renewalLead = 5 * time.Minute

// renewalFlagName is the gateway feature flag that enables pre-expiry
// rotation. The scheduler reads it from /api/serverinfo responses to
// avoid probing a gateway that will never rotate proactively.
const renewalFlagName = "experimental.tunnel_token_renewal"

// tokenState is the daemon's single source of truth for the current
// gateway bearer token. Constructed in main before the tunnel manager
// and the daemon service, so both can reference it without a
// construction cycle. Implements tunnelmgr.TokenSource. Safe for
// concurrent use.
//
// The epoch counts token-generation swaps (SetLocal calls). Rotations
// carry the epoch of the snapshot their request was built from and are
// rejected on mismatch: without this, an in-flight request from a
// session that ended (logout, auth-expiry, re-login as someone else)
// could install its rotated token over the new state.
type tokenState struct {
	mu       sync.RWMutex
	token    string
	epoch    uint64
	onRotate func(newToken string)
}

func newTokenState() *tokenState { return &tokenState{} }

// Current returns the token new REST calls and gRPC flows must use.
func (t *tokenState) Current() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.token
}

// Snapshot implements tunnelmgr.TokenSource.
func (t *tokenState) Snapshot() (string, uint64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.token, t.epoch
}

// SetLocal replaces the token and starts a new epoch, invalidating any
// rotation still in flight from the previous generation. It never
// fires the rotation hook — it is for state the service persisted
// itself: the initial config load, a completed login, a logout, or an
// auth-expired teardown.
func (t *tokenState) SetLocal(token string) {
	t.mu.Lock()
	t.token = token
	t.epoch++
	t.mu.Unlock()
}

// SetRotationHook registers the callback fired by accepted rotations.
// Called once by the daemon service during construction.
func (t *tokenState) SetRotationHook(hook func(newToken string)) {
	t.mu.Lock()
	t.onRotate = hook
	t.mu.Unlock()
}

// Rotate implements tunnelmgr.TokenSource: it installs a token the
// gateway rotated via X-New-Access-Token for a request built at the
// given epoch, firing the persistence hook. Rejected when the epoch no
// longer matches (the session that made the request is gone) and
// no-oped on empty or unchanged tokens so repeated harvests of the
// same rotation don't rewrite the config file.
//
// Accepted rotations deliberately do NOT bump the epoch: a rotation is
// a continuation of the same login session, and concurrent requests
// from that session must still be able to deliver their (equal or
// newer) rotations.
func (t *tokenState) Rotate(newToken string, epoch uint64) {
	if newToken == "" {
		return
	}
	t.mu.Lock()
	if epoch != t.epoch || newToken == t.token {
		t.mu.Unlock()
		return
	}
	t.token = newToken
	hook := t.onRotate
	t.mu.Unlock()
	if hook != nil {
		hook(newToken)
	}
}

// renewalFlagCache remembers whether the gateway reported the renewal
// feature flag as disabled — scoped to a token epoch. A logout/login
// can land on a different gateway/org where the flag state differs, so
// the cache resets to the optimistic default (enabled) whenever the
// epoch changes and re-learns from the next serverinfo response.
type renewalFlagCache struct {
	disabled bool
	epoch    uint64
}

// disabledFor returns the cached flag state for the given epoch,
// resetting to the optimistic default when the epoch changed since the
// last observation.
func (c *renewalFlagCache) disabledFor(epoch uint64) bool {
	if epoch != c.epoch {
		c.disabled = false
		c.epoch = epoch
	}
	return c.disabled
}

// observe records the flag state reported by a serverinfo response for
// the given epoch. Returns true when the effective state flipped (for
// logging). Observations from a stale epoch are ignored.
func (c *renewalFlagCache) observe(enabled bool, epoch uint64) (flipped bool) {
	if epoch != c.epoch {
		return false
	}
	newDisabled := !enabled
	flipped = c.disabled != newDisabled
	c.disabled = newDisabled
	return flipped
}

// jwtExpiry extracts the exp claim from a JWT without verifying the
// signature — the daemon is the party that RECEIVED this token from
// the gateway; it only needs the timestamp for scheduling. Returns
// false for opaque tokens or tokens without exp; those cannot be
// scheduled and rely on passive harvesting alone.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

// StartTokenRenewal launches the background token-renewal scheduler.
// It runs for the whole daemon lifetime (exits when ctx is cancelled)
// and is a cheap no-op while the daemon is logged out.
//
// Each iteration sleeps until renewalLead before the current token's
// expiry, then performs a GET /api/serverinfo carrying the token: if
// the gateway's rotation window is active the response header hands
// back a fresh token, which is persisted via the tokenState rotation
// hook. Inside the window the cadence is one attempt per minute (with
// jitter so daemon fleets don't synchronize).
//
// The serverinfo response also reports the gateway's feature flags:
// when experimental.tunnel_token_renewal is reported disabled the
// scheduler stops pre-expiry probing and schedules a single probe just
// after expiry instead — on OIDC gateways that probe harvests the
// (ungated) reactive refresh; on local-auth it yields the 401 that
// triggers the clean teardown.
//
// A 401 means the token expired AND the gateway could not refresh it
// server-side (refresh token revoked/expired, or local-auth without
// renewal): the daemon performs a clean teardown with an explicit
// reason so `hsh tunnel status` tells the user to log in again.
func (s *daemonService) StartTokenRenewal(ctx context.Context, logf func(string, ...any)) {
	go func() {
		logf("token-renewal scheduler started (lead=%v)", renewalLead)
		// flags caches the renewal flag state the gateway reported,
		// scoped to the token epoch so a re-login (possibly against a
		// different gateway/org) re-learns instead of inheriting the
		// previous session's state.
		var flags renewalFlagCache
		for {
			_, currentEpoch := s.tokens.Snapshot()
			delay := s.nextRenewalDelay(flags.disabledFor(currentEpoch))
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}

			token, epoch := s.tokens.Snapshot()
			if token == "" {
				continue // logged out — idle poll
			}

			s.mu.RLock()
			apiURL := s.cfg.APIURL
			s.mu.RUnlock()
			if apiURL == "" {
				continue
			}

			rotated := false
			si, err := client.FetchServerInfo(ctx, client.FetchServerInfoOptions{
				APIBaseURL: apiURL,
				Token:      token,
				OnNewToken: func(newToken string) {
					rotated = true
					s.tokens.Rotate(newToken, epoch)
				},
			})
			switch {
			case errors.Is(err, client.ErrUnauthorized):
				logf("token renewal: session is no longer renewable — tearing the tunnel down")
				s.authExpired("session expired and could not be renewed; run 'hsh tunnel login' to re-authenticate")
			case err != nil:
				// Transient (gateway restart, network blip). The next
				// iteration recomputes the delay from the same token and
				// retries; the reactive refresh path keeps REST working
				// even slightly past expiry on OIDC gateways.
				logf("token renewal: probe failed (will retry): %v", err)
			default:
				if enabled, reported := si.FeatureFlags[renewalFlagName]; reported {
					if flags.observe(enabled, epoch) {
						logf("token renewal: gateway reports %s=%v", renewalFlagName, enabled)
					}
				}
				if rotated {
					if exp, ok := jwtExpiry(s.tokens.Current()); ok {
						logf("token renewed silently — next expiry %s", exp.UTC().Format(time.RFC3339))
					} else {
						logf("token renewed silently")
					}
				}
			}
		}
	}()
}

// nextRenewalDelay computes how long the scheduler sleeps before the
// next renewal attempt, based on the current token:
//
//   - logged out            → 30s idle poll (cheap, no I/O)
//   - opaque token (no exp) → hourly; passive harvesting still works
//   - renewal flag off      → just past expiry (reactive-refresh
//     harvest on OIDC, clean 401 on local auth)
//   - exp known             → until renewalLead before expiry, floored
//     at ~1 minute (jittered) so attempts inside
//     the window pace at 1/min without fleet
//     synchronization.
//
// A token rotated by other paths (connection-list refresh harvest)
// while the scheduler sleeps simply makes the next wake-up a cheap
// no-op probe; the iteration after that re-derives the schedule from
// the new token's expiry.
func (s *daemonService) nextRenewalDelay(renewalDisabled bool) time.Duration {
	token := s.tokens.Current()
	if token == "" {
		return 30 * time.Second
	}
	exp, ok := jwtExpiry(token)
	if !ok {
		return time.Hour
	}
	minCadence := time.Minute + time.Duration(rand.Int63n(int64(30*time.Second)))
	if renewalDisabled {
		// No pre-expiry window to hit: one probe shortly after expiry.
		d := time.Until(exp) + 30*time.Second
		if d < minCadence {
			return minCadence
		}
		return d
	}
	d := time.Until(exp) - renewalLead
	if d < minCadence {
		return minCadence
	}
	return d
}
