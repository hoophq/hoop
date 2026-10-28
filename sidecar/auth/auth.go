// Package auth links the credential-swap runtime into a sidecar binary:
// the built-in identity verifiers (none, kubernetes, jwt), the built-in
// credential sources (secret_files, gcp_impersonation, x509, vault,
// gcp_secret_manager), the lane's login termination and the login drivers.
// daemon/auth.go defines the contract; the config, its validation and the
// registries stay in the root module, so the control plane checks a pooled
// lane without linking any of this. A binary gets it by importing this
// package.
package auth

import (
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	// The login drivers, one per database protocol, register with the
	// relay from their own init.
	_ "github.com/hoophq/hoop/sidecar/auth/login"
)

func init() {
	daemon.RegisterIdentitySource(daemon.IdentityNone, buildNoneIdentity)
	daemon.RegisterIdentitySource(daemon.IdentityKubernetes, buildKubernetesIdentity)
	daemon.RegisterIdentitySource(daemon.IdentityJWT, buildJWTIdentity)

	daemon.RegisterCredentialSource(daemon.SourceSecretFiles, buildSecretFilesSource)
	daemon.RegisterCredentialSource(daemon.SourceGCPImpersonation, buildGCPImpersonationSource)
	daemon.RegisterCredentialSource(daemon.SourceX509, buildX509Source)
	daemon.RegisterCredentialSource(daemon.SourceVault, buildVaultSource)
	daemon.RegisterCredentialSource(daemon.SourceGCPSecretManager, buildGCPSecretManagerSource)

	daemon.RegisterAuthEngine(engine{})
}

// engine implements daemon.AuthEngine.
type engine struct{}

func (engine) BuildLaneAuth(lc daemon.ListenerConfig, name string, deps daemon.AuthDeps,
	sources map[string]daemon.CredentialSource) (daemon.LaneAuth, error) {
	return buildLaneAuth(lc, name, deps, sources)
}

func (engine) CredentialBoundary(pol policy.Evaluator, proto inspect.Protocol) policy.Evaluator {
	return credentialBoundary(pol, proto)
}
