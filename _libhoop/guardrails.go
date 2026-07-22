package libhoop

import (
	"fmt"
	"strings"
)

// ErrGuardRailsUnsupported indicates guardrail rules are configured for a
// proxy that has no guardrail evaluation path (DEP-48). Callers branch on it
// with errors.As.
type ErrGuardRailsUnsupported struct {
	// ProxyName is the native proxy that cannot enforce the rules.
	ProxyName string
}

func (e *ErrGuardRailsUnsupported) Error() string {
	return fmt.Sprintf("connection has guardrail rules configured, but the native %s proxy does not support guardrail enforcement; "+
		"remove the guardrails from this connection or use exec instead", e.ProxyName)
}

// CheckGuardRailEnforcement returns *ErrGuardRailsUnsupported when guardrail
// rules are configured for a proxy that cannot enforce them. The OSS build
// has no guardrail evaluation path at all, so any non-empty rule payload
// fails closed (DEP-48).
func CheckGuardRailEnforcement(guardRailRules, proxyName string) error {
	if strings.TrimSpace(guardRailRules) == "" {
		return nil
	}
	return &ErrGuardRailsUnsupported{ProxyName: proxyName}
}
