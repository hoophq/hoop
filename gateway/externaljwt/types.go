// Package externaljwt validates JWT tokens issued by external identity
// providers (currently only SPIFFE JWT-SVIDs, but shaped to admit other
// issuer types such as generic OIDC pass-through in the future).
//
// The package exposes a Provider interface and a singleton manager used by
// the auth interceptors. Validation returns a ValidatedIdentity which the
// caller is expected to map onto a Hoop identity (e.g. via the
// agent_spiffe_mappings table for SPIFFE tokens).
package externaljwt

import (
	"context"
	"errors"
	"time"
)

// IssuerType identifies the kind of external issuer a Provider represents.
// Today only SPIFFE is implemented.
type IssuerType string

const (
	IssuerSPIFFE IssuerType = "spiffe"
)

// ValidatedIdentity is the normalized result of a successful token
// validation. It carries enough metadata for audit logs and for downstream
// identity resolution. The caller decides how to map it onto a Hoop agent
// or user.
type ValidatedIdentity struct {
	// IssuerType identifies which provider validated this token.
	IssuerType IssuerType

	// Subject is the identity the token asserts. For SPIFFE this is the
	// full SPIFFE ID (spiffe://trust-domain/path). For other issuer types
	// it may be an email, a service-account identifier, etc.
	Subject string

	// TrustDomain is the SPIFFE trust domain for SPIFFE tokens. Empty for
	// non-SPIFFE tokens.
	TrustDomain string

	// Audience is the audience claim that was validated.
	Audience string

	// ExpiresAt is the expiry of the underlying token.
	ExpiresAt time.Time

	// IssuedAt is the issuance time of the underlying token, if set.
	IssuedAt time.Time
}

// Provider validates and refreshes credentials for a single external issuer
// configuration. Implementations must be safe for concurrent use.
type Provider interface {
	// Type returns the issuer type this provider handles.
	Type() IssuerType

	// Validate verifies the signature, expiry, audience, and issuer shape
	// of the provided token. On success it returns a ValidatedIdentity.
	// On failure it returns an error; callers should not attempt to fall
	// back to other authentication mechanisms based on validation failure.
	Validate(ctx context.Context, token string) (*ValidatedIdentity, error)

	// Refresh forces a refresh of the provider's trust material (trust
	// bundle, JWKS, etc). It is normally called periodically by the
	// manager but exposed here for test and debug paths.
	Refresh(ctx context.Context) error

	// Close releases any resources held by the provider (HTTP clients,
	// file watchers, etc).
	Close() error
}

// ErrNotConfigured is returned by the manager accessor when no provider is
// configured for a given issuer type. Callers should treat this as "do not
// attempt external-JWT validation" rather than as an auth failure.
var ErrNotConfigured = errors.New("external jwt provider not configured")

// ErrValidationFailed is returned when a token does not validate. The
// concrete error is wrapped to avoid leaking sensitive details to callers
// while keeping diagnostic info in the log line from the provider
// implementation.
var ErrValidationFailed = errors.New("external jwt validation failed")
