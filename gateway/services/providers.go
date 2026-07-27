package services

import (
	"errors"

	"github.com/hoophq/hoop/gateway/appconfig"
)

// The provider-availability invariant: rules the server cannot enforce must
// not be configurable. Data-masking rules without a DLP provider are
// silently not applied, giving false confidence that data is masked.
//
// This check is the single source of truth for that invariant: every
// mutation path (REST handlers, MCP tools, future admin APIs) must call it
// before persisting data-masking configuration. The error text is
// operator-facing remediation and is surfaced verbatim by all boundaries.
//
// Guardrails are deliberately NOT gated on any provider: they are enforced
// by the agent's built-in pattern-matching engine (deny-word / regex, see
// gateway/guardrails) and work without Presidio or any DLP provider.
var ErrRedactProviderMissing = errors.New(
	"no data masking provider is configured: data masking requires a DLP provider " +
		"(set DLP_PROVIDER=mspresidio with MSPRESIDIO_ANALYZER_URL and MSPRESIDIO_ANONYMIZER_URL, " +
		"or DLP_PROVIDER=gcp with GOOGLE_APPLICATION_CREDENTIALS_JSON); " +
		"rules configured without one are not enforced")

// CheckRedactProvider returns ErrRedactProviderMissing when the server has
// no DLP provider configured (Presidio or GCP both qualify for masking).
func CheckRedactProvider() error {
	if appconfig.Get().HasRedactCredentials() {
		return nil
	}
	return ErrRedactProviderMissing
}
