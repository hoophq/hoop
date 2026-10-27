package daemon

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// authModule is the nested module that carries the credential-swap runtime:
// the built-in verifiers and sources, the credential set and the lane's
// login termination. The root module keeps the config, its validation and
// the registries, so the control plane decodes and checks a pooled lane
// without linking the runtime; a sidecar that serves one links the module.
const authModule = "github.com/hoophq/hoop/sidecar/auth"

// LaneAuth is one lane's login termination: the verifier of the client leg,
// the pool, the rule that picks an entry, and the sources that materialize
// it. The engine builds it for every lane with a pool; buildServer and the
// endpoint lanes compose the relay's hooks from it.
type LaneAuth interface {
	// Authenticate is the relay's Authenticate: verify, select, materialize.
	// A refusal wraps codectypes.ErrLoginRefused; a verifier or source
	// failure is wrapped too, and logged with its cause, which never
	// reaches the client.
	Authenticate(ctx context.Context, user, secret string) (session.Identity, Credential, error)
	// AuthenticateProven is Authenticate for a login a driver already
	// proved by SCRAM against the material SCRAM served: hash names the
	// credential proved, the key never crossed the wire. Only a driver
	// that ran the proof calls it; a secret shaped like a hash through
	// Authenticate is refused.
	AuthenticateProven(ctx context.Context, user, hash string) (session.Identity, Credential, error)
	// Authenticator is the libhoop form of Authenticate, for a codec that
	// terminates the login itself (ClickHouse).
	Authenticator() codectypes.Authenticator
	// Swap is the http codec's Swap: the request's Authorization, verified
	// and exchanged for the upstream's.
	Swap(ctx context.Context, credential string) (codechttp.SwapResult, error)
	// RequestIdentity is the gate's per-request resolver on a pooled http
	// lane: the verifier alone.
	RequestIdentity() gate.RequestIdentity
	// SCRAM is the material lookup a hoop_credential lane offers a driver
	// acting as the SCRAM server on the client leg; nil on every other
	// verifier.
	SCRAM() func(user string) (codectypes.SCRAMMaterial, bool)
	// UpstreamAuthorization is the gRPC server's per-RPC hook: the header
	// value the upstream sees for a credential already resolved.
	UpstreamAuthorization(cred Credential) string
	// IdentityType and PoolSize feed the log line a pooled lane prints.
	IdentityType() string
	PoolSize() int
}

// CredentialSet is the hoop credentials the plane minted for this sidecar,
// as the reloader sees them: one per process, swapped whole on every
// heartbeat that carries one. The hoop_credential verifier reads it through
// IdentityDeps.
type CredentialSet interface {
	Swap(entries []CredentialSetEntry)
	// Size is the number of entries in force, for a log line.
	Size() int
}

// AuthDeps is what the engine draws on for one lane, resolved by buildLanes:
// the process trust pool (nil is the host store), what a verifier may know
// about the connection, the lane's decision endpoint (nil without one) and
// its logger.
type AuthDeps struct {
	Roots *x509.CertPool
	Peer  PeerInfo
	OPA   *policy.OPAClient
	Log   *slog.Logger
}

// AuthEngine is the credential-swap runtime. sidecar/auth registers one from
// init; a binary gets it by importing that module.
type AuthEngine interface {
	// NewCredentialSet indexes the plane's entries for the verifiers.
	NewCredentialSet(entries []CredentialSetEntry) CredentialSet
	// BuildLaneAuth builds a pooled lane's login termination. Every error is
	// a config fact: an unlinked type, a source the pool names and the
	// block does not configure, a selector with no endpoint.
	BuildLaneAuth(lc ListenerConfig, name string, deps AuthDeps, set CredentialSet, sources map[string]CredentialSource) (LaneAuth, error)
	// CredentialBoundary prepends the guardrail every pooled SQL lane runs
	// ahead of its own policy, outside any observe wrapper.
	CredentialBoundary(pol policy.Evaluator, proto inspect.Protocol) policy.Evaluator
}

var (
	authMu     sync.Mutex
	authEngine AuthEngine
)

// RegisterAuthEngine links the credential-swap runtime into this binary. It
// panics on a second registration, so the engine cannot depend on import
// order.
func RegisterAuthEngine(e AuthEngine) {
	if e == nil {
		panic("sidecar/daemon: RegisterAuthEngine called with a nil engine")
	}
	authMu.Lock()
	defer authMu.Unlock()
	if authEngine != nil {
		panic("sidecar/daemon: duplicate auth engine registration")
	}
	authEngine = e
}

func registeredAuthEngine() AuthEngine {
	authMu.Lock()
	defer authMu.Unlock()
	return authEngine
}

// authModuleError names the module a lane needs and the binary that links it.
func authModuleError(where, key string) error {
	return fmt.Errorf("%s: %s needs the credential-swap runtime, and this build did not link it; "+
		"use the hoop-inspect binary or hoop start sidecar, which import %s", where, key, authModule)
}

// checkAuthEngine refuses a lane that terminates its login when this build
// links no engine. Run and Validate call it; Config.Validate does not,
// because the control plane validates sidecar documents with the same
// decoder and links no module.
func checkAuthEngine(cfg *Config) error {
	if registeredAuthEngine() != nil {
		return nil
	}
	for i, l := range cfg.Listeners {
		for _, k := range []struct {
			set bool
			key string
		}{{l.Identity != nil, "identity"}, {l.Credentials != nil, "credentials"},
			{len(l.PassthroughUsers) > 0, "passthrough_users"}, {l.Propagate != nil, "propagate"}} {
			if k.set {
				return authModuleError(l.displayName(i), k.key)
			}
		}
	}
	return nil
}

// SplitAuthorization reads a credential lifted from Authorization: a bearer
// is the secret alone, basic is user and password, anything else is taken
// as the secret, so a verifier sees the key whichever way a client sent it.
func SplitAuthorization(value string) (user, secret string) {
	value = strings.TrimSpace(value)
	scheme, rest, _ := strings.Cut(value, " ")
	rest = strings.TrimSpace(rest)
	switch {
	case strings.EqualFold(scheme, "Bearer"):
		return "", rest
	case strings.EqualFold(scheme, "Basic"):
		raw, err := base64.StdEncoding.DecodeString(rest)
		if err != nil {
			return "", ""
		}
		u, p, _ := strings.Cut(string(raw), ":")
		return u, p
	}
	return "", value
}
