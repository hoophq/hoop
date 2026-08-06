package services

import (
	"errors"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/appconfig"
)

const AlcatrazDLPFlagName = "experimental.alcatraz_dlp"

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
		"DLP_PROVIDER=gcp with GOOGLE_APPLICATION_CREDENTIALS_JSON, " +
		"or DLP_PROVIDER=alcatraz which needs no credentials); " +
		"rules configured without one are not enforced")

// DLPProviderForOrg returns the effective provider for an organization.
// The feature flag intentionally takes precedence over DLP_PROVIDER so
// Alcatraz can be enabled per organization without changing the deployment.
func DLPProviderForOrg(orgID string) string {
	if featureflag.IsEnabled(orgID, AlcatrazDLPFlagName) {
		return "alcatraz"
	}
	return appconfig.Get().DlpProvider()
}

// CheckRedactProvider returns ErrRedactProviderMissing when the server has
// no DLP provider configured (Presidio, GCP and alcatraz all qualify for
// masking). It is retained for callers without organization context.
func CheckRedactProvider() error {
	if appconfig.Get().HasRedactCredentials() {
		return nil
	}
	return ErrRedactProviderMissing
}

// CheckRedactProviderForOrg applies the organization-scoped provider flag
// before checking whether data masking can be enforced.
func CheckRedactProviderForOrg(orgID string) error {
	if DLPProviderForOrg(orgID) == "alcatraz" || appconfig.Get().HasRedactCredentials() {
		return nil
	}
	return ErrRedactProviderMissing
}
