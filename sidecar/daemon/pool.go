package daemon

import (
	"slices"
	"strings"
)

// HasPool reports whether a lane terminates its login.
func (l ListenerConfig) HasPool() bool {
	return l.Credentials != nil && len(l.Credentials.Pool) > 0
}

// IdentityType is the verifier a lane runs: the configured one, or none on
// a unix socket with a pool and no identity block.
func (l ListenerConfig) IdentityType() string {
	if l.Identity != nil {
		return l.Identity.Type
	}
	if l.HasPool() && l.Network == "unix" {
		return IdentityNone
	}
	return ""
}

// network is the lane's transport, tcp when unset.
func (l ListenerConfig) network() string {
	if l.Network == "" {
		return "tcp"
	}
	return l.Network
}

// passthrough is the relay's Passthrough: a startup user the lane relays
// unchanged.
func passthrough(users []string) func(user string) bool {
	if len(users) == 0 {
		return nil
	}
	return func(user string) bool { return slices.Contains(users, user) }
}

// validateRuleKey refuses a When key the selector would never read.
func validateRuleKey(key string) bool {
	switch {
	case key == "identity.subject", key == "identity.email", key == "identity.groups":
		return true
	case strings.HasPrefix(key, "identity.attributes."):
		return len(key) > len("identity.attributes.")
	case strings.HasPrefix(key, "context."):
		return len(key) > len("context.")
	}
	return false
}
