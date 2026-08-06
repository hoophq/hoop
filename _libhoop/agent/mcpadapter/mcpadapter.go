// Package mcpadapter is the OSS stub of the enterprise MCP adapter.
//
// It exists so `make libhoop-map` (which symlinks _libhoop -> libhoop) keeps
// the agent compiling without the enterprise library. Hooks is the real type —
// a struct of plain function types — because the agent constructs and inspects
// it; the stub simply never populates it, so the MCP gateway runs with
// guardrails and masking absent rather than with fake ones.
package mcpadapter

import "context"

// Hooks mirrors the enterprise type. Nil members mean the capability is
// absent, which the MCP gateway handles by omitting those pipeline stages.
type Hooks struct {
	GuardInput func(ctx context.Context, direction, text string) error
	Redact     func(ctx context.Context, text string) (string, int, error)
}

// GuardrailViolation always reports false in OSS builds: with no redaction
// engine there are no guardrail rules to violate.
func GuardrailViolation(error) bool { return false }
