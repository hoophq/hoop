// Package services federation orchestration. Federation lives in services/
// rather than inside the transport layer because:
//
//  1. The same code path is reused by the SessionOpen hot path AND by the
//     /federation/test admin endpoint (dry-run), so it must be callable
//     outside the gRPC stream.
//  2. It bridges two subsystems (models, federation resolvers) and should not
//     leak either of them into the gateway/api handlers.
package services

import (
	"context"
	"fmt"

	"github.com/hoophq/hoop/gateway/federation"
	"github.com/hoophq/hoop/gateway/models"
)

// FederationInput is the per-session context the orchestration needs.
type FederationInput struct {
	OrgID        string
	ConnectionID string
	AgentID      string
	SessionID    string
	UserID       string
	UserEmail    string
}

// ResolveFederation runs the configured federation provider for a session.
// Returns a federation.Result whose EnvVars the caller must merge into the
// session's ConnectionSecret map (base64-encoding values to match the wire
// contract the agent's secretsmanager.Decode expects). Errors here are
// authoritative — the caller should apply the configured fallback policy
// rather than continuing.
func ResolveFederation(ctx context.Context, cfg *models.ConnectionFederationConfig, in FederationInput) (*federation.Result, error) {
	if cfg == nil {
		return nil, fmt.Errorf("missing federation config")
	}

	resolvedPrincipal, err := federation.ResolveIdentity(
		cfg.IdentitySourceAttribute,
		cfg.IdentityTargetTemplate,
		federation.IdentityContext{
			UserEmail: in.UserEmail,
			UserID:    in.UserID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("identity mapping failed: %w", err)
	}

	switch cfg.HookSource {
	case models.FederationHookSourceBuiltin:
		return resolveBuiltin(ctx, cfg, in, resolvedPrincipal)
	default:
		return nil, fmt.Errorf("unknown federation hook_source %q", cfg.HookSource)
	}
}

// ResolveFederationFallback runs the federation flow again with the
// configured readonly_principal forced into the target. Returns
// ErrNoFallbackConfigured when fallback_policy is anything other than
// "readonly".
func ResolveFederationFallback(ctx context.Context, cfg *models.ConnectionFederationConfig, in FederationInput) (*federation.Result, error) {
	if cfg == nil {
		return nil, fmt.Errorf("missing federation config")
	}
	if cfg.FallbackPolicy != models.FederationFallbackReadonly {
		return nil, ErrNoFallbackConfigured
	}
	if cfg.ReadonlyPrincipal == nil || *cfg.ReadonlyPrincipal == "" {
		return nil, fmt.Errorf("fallback_policy=readonly but readonly_principal is empty")
	}
	// Reuse the same provider branch logic but pre-resolve the principal to
	// the readonly one. We still pass IdentityContext through so providers
	// that want to log the original user can do so.
	principal := *cfg.ReadonlyPrincipal
	switch cfg.HookSource {
	case models.FederationHookSourceBuiltin:
		return resolveBuiltin(ctx, cfg, in, principal)
	default:
		return nil, fmt.Errorf("unknown federation hook_source %q", cfg.HookSource)
	}
}

// ErrNoFallbackConfigured signals that the caller asked for the fallback
// resolution path but the federation config is set to deny on failure.
var ErrNoFallbackConfigured = fmt.Errorf("no fallback configured (fallback_policy=deny)")

// resolveBuiltin dispatches to a built-in federation provider registered in
// the federation package's process-wide registry. The admin credentials are
// decrypted once here so the resolver never sees the ciphertext.
func resolveBuiltin(ctx context.Context, cfg *models.ConnectionFederationConfig, in FederationInput, principal string) (*federation.Result, error) {
	if cfg.BuiltinProvider == nil || *cfg.BuiltinProvider == "" {
		return nil, fmt.Errorf("builtin federation config missing builtin_provider")
	}
	resolver, err := federation.LookupResolver(*cfg.BuiltinProvider)
	if err != nil {
		return nil, err
	}

	var adminPlain []byte
	if len(cfg.AdminCredentialsEncrypted) > 0 {
		plain, derr := models.DecryptCredentialSecretKey(cfg.AdminCredentialsEncrypted)
		if derr != nil {
			return nil, fmt.Errorf("failed decrypting admin credentials: %w", derr)
		}
		adminPlain = []byte(plain)
	}

	res, err := resolver.Resolve(ctx, federation.ResolveRequest{
		OrgID:                 in.OrgID,
		ConnectionID:          in.ConnectionID,
		UserID:                in.UserID,
		UserEmail:             in.UserEmail,
		Config:                cfg,
		AdminCredentialsPlain: adminPlain,
		ResolvedPrincipal:     principal,
	})
	// Zero the plaintext credentials before returning so the buffer can't be
	// inspected via heap dumps post-resolve. This is best-effort — Go's
	// garbage collector may have already copied the slice — but it costs
	// nothing and reduces the residency window.
	for i := range adminPlain {
		adminPlain[i] = 0
	}
	return res, err
}
