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

// FederationInput is the per-session context the orchestration needs. Only
// the fields the resolvers actually consume are carried here; AgentID and
// SessionID intentionally do not appear because federation.ResolveRequest
// has no place to thread them and no resolver reads them. If a future
// resolver needs session context for audit, add the field to ResolveRequest
// first so the wiring is honest end-to-end.
type FederationInput struct {
	OrgID        string
	ConnectionID string
	UserID       string
	UserEmail    string
}

// ResolveFederation runs the configured federation provider for a session.
// Returns a federation.Result whose EnvVars the caller must merge into the
// session's ConnectionSecret map (base64-encoding values to match the wire
// contract the agent's secretsmanager.Decode expects). Errors here are
// authoritative, and the caller should apply the configured fallback policy
// rather than continuing.
//
// Admin credentials are sourced from cfg.AdminCredentialsEncrypted (decrypted
// here); callers that already hold plaintext credentials (e.g. the
// /federation/test handler validating a wizard draft) should use
// ResolveFederationDryRun instead.
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
	return dispatchWithStoredCreds(ctx, cfg, in, resolvedPrincipal)
}

// ResolveFederationDryRun is the stateless variant of ResolveFederation:
// the caller supplies plaintext admin credentials directly and the function
// never touches DB-stored ciphertext. This is the path the /federation/test
// endpoint uses when a wizard submits a draft config for validation before
// any row has been persisted.
//
// Ownership: the caller retains ownership of adminPlain and is responsible
// for zeroing it after this returns.
func ResolveFederationDryRun(ctx context.Context, cfg *models.ConnectionFederationConfig, adminPlain []byte, in FederationInput) (*federation.Result, error) {
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
		return resolveBuiltinCore(ctx, cfg, in, resolvedPrincipal, adminPlain)
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
	return dispatchWithStoredCreds(ctx, cfg, in, *cfg.ReadonlyPrincipal)
}

// ErrNoFallbackConfigured signals that the caller asked for the fallback
// resolution path but the federation config is set to deny on failure.
var ErrNoFallbackConfigured = fmt.Errorf("no fallback configured (fallback_policy=deny)")

// dispatchWithStoredCreds dispatches to the configured hook source after
// decrypting admin credentials from the persisted row. This is the common
// path for session-open and post-save diagnostics; the dry-run variant
// (ResolveFederationDryRun) calls resolveBuiltinCore directly with caller-
// supplied plaintext credentials and skips this decrypt step.
func dispatchWithStoredCreds(ctx context.Context, cfg *models.ConnectionFederationConfig, in FederationInput, principal string) (*federation.Result, error) {
	switch cfg.HookSource {
	case models.FederationHookSourceBuiltin:
		var adminPlain []byte
		if len(cfg.AdminCredentialsEncrypted) > 0 {
			plain, derr := models.DecryptCredentialSecretKey(cfg.AdminCredentialsEncrypted)
			if derr != nil {
				return nil, fmt.Errorf("failed decrypting admin credentials: %w", derr)
			}
			adminPlain = []byte(plain)
		}
		// Zero the plaintext credentials before returning so the buffer
		// can't be inspected via heap dumps post-resolve. Best-effort
		// (Go's GC may have already copied the slice) but cheap.
		defer func() {
			for i := range adminPlain {
				adminPlain[i] = 0
			}
		}()
		return resolveBuiltinCore(ctx, cfg, in, principal, adminPlain)
	default:
		return nil, fmt.Errorf("unknown federation hook_source %q", cfg.HookSource)
	}
}

// resolveBuiltinCore looks up the configured builtin resolver and invokes it
// with the supplied principal and plaintext admin credentials. It is
// credential-agnostic: callers are responsible for sourcing adminPlain
// (either by decrypting cfg.AdminCredentialsEncrypted or by accepting it
// directly from an API caller) and for zeroing the buffer afterwards.
func resolveBuiltinCore(ctx context.Context, cfg *models.ConnectionFederationConfig, in FederationInput, principal string, adminPlain []byte) (*federation.Result, error) {
	if cfg.BuiltinProvider == nil || *cfg.BuiltinProvider == "" {
		return nil, fmt.Errorf("builtin federation config missing builtin_provider")
	}
	resolver, err := federation.LookupResolver(*cfg.BuiltinProvider)
	if err != nil {
		return nil, err
	}
	return resolver.Resolve(ctx, federation.ResolveRequest{
		OrgID:                 in.OrgID,
		ConnectionID:          in.ConnectionID,
		UserID:                in.UserID,
		UserEmail:             in.UserEmail,
		Config:                cfg,
		AdminCredentialsPlain: adminPlain,
		ResolvedPrincipal:     principal,
	})
}
