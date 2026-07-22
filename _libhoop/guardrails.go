package libhoop

import (
	"fmt"
	"strings"
)

// CheckGuardRailEnforcement returns an error when guardrail rules are
// configured for a proxy that cannot enforce them. The OSS build has no
// guardrail evaluation path at all, so any non-empty rule payload fails
// closed (DEP-48).
func CheckGuardRailEnforcement(guardRailRules, proxyName string) error {
	if strings.TrimSpace(guardRailRules) == "" {
		return nil
	}
	return fmt.Errorf("connection has guardrail rules configured, but the native %s proxy does not support guardrail enforcement; "+
		"remove the guardrails from this connection or use exec instead", proxyName)
}
