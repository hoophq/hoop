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
	"errors"
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
		userPlain, googleEmail, uerr := loadUserCredentials(cfg, in)
		if uerr != nil {
			return nil, uerr
		}
		defer zeroBytes(userPlain)
		if googleEmail != "" {
			resolvedPrincipal = googleEmail
		}
		return resolveBuiltinCore(ctx, cfg, in, resolvedPrincipal, adminPlain, userPlain)
	default:
		return nil, fmt.Errorf("unknown federation hook_source %q", cfg.HookSource)
	}
}

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
		defer zeroBytes(adminPlain)

		userPlain, googleEmail, uerr := loadUserCredentials(cfg, in)
		if uerr != nil {
			return nil, uerr
		}
		defer zeroBytes(userPlain)
		if googleEmail != "" {
			principal = googleEmail
		}
		return resolveBuiltinCore(ctx, cfg, in, principal, adminPlain, userPlain)
	default:
		return nil, fmt.Errorf("unknown federation hook_source %q", cfg.HookSource)
	}
}

// loadUserCredentials fetches and decrypts the per-user credential a provider
// needs, keyed by (connection, user). It is a no-op for providers that do not
// use per-user credentials (e.g. gcp_iam), returning empty results so the
// gcp_iam path is unchanged.
//
// For gcp_oauth it loads the stored Google refresh token. A missing row
// (ErrNotFound) is NOT an error here: it returns empty so the resolver can
// surface the actionable "user has not connected an account" message. The
// returned googleEmail, when non-empty, is the consented Google identity the
// caller uses to override the resolved principal so audit metadata reflects
// the real human rather than an identity-template render.
//
// Ownership: the caller is responsible for zeroing the returned userPlain.
func loadUserCredentials(cfg *models.ConnectionFederationConfig, in FederationInput) (userPlain []byte, googleEmail string, err error) {
	if cfg.BuiltinProvider == nil || *cfg.BuiltinProvider != models.FederationProviderGCPOAuth {
		return nil, "", nil
	}
	cred, err := models.GetFederationUserCredential(models.DB, in.OrgID, cfg.ConnectionID, in.UserID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("failed loading user federation credential: %w", err)
	}
	plain, derr := models.DecryptCredentialSecretKey(cred.RefreshTokenEncrypted)
	if derr != nil {
		return nil, "", fmt.Errorf("failed decrypting user federation credential: %w", derr)
	}
	return []byte(plain), cred.GoogleEmail, nil
}

// zeroBytes best-effort wipes a plaintext credential buffer.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// resolveBuiltinCore looks up the configured builtin resolver and invokes it
// with the supplied principal and plaintext credentials. It is
// credential-agnostic: callers are responsible for sourcing adminPlain
// (either by decrypting cfg.AdminCredentialsEncrypted or by accepting it
// directly from an API caller) and userPlain (the per-user credential, e.g.
// the gcp_oauth refresh token) and for zeroing both buffers afterwards.
func resolveBuiltinCore(ctx context.Context, cfg *models.ConnectionFederationConfig, in FederationInput, principal string, adminPlain, userPlain []byte) (*federation.Result, error) {
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
		UserCredentialsPlain:  userPlain,
		ResolvedPrincipal:     principal,
	})
}
