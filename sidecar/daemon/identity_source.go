package daemon

import (
	"crypto/x509"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"context"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// The identity vocabulary a config may name. The built-in types register
// from sidecar/auth's init and spiffe from sidecar/identity/spiffe, so a
// build without a module knows the name and refuses the lane on the host
// (checkAuthEngine, checkIdentitySources), while the control plane accepts
// it.
const (
	IdentityNone           = "none"
	IdentityKubernetes     = "kubernetes"
	IdentityHoopCredential = "hoop_credential"
	IdentityJWT            = "jwt"
	IdentitySPIFFE         = "spiffe"

	// DefaultIdentityAudience is the audience a kubernetes, jwt or spiffe
	// token must carry when the lane names none.
	DefaultIdentityAudience = "hoop-sidecar"

	// IdentityTypeAttribute is the Identity attribute every verifier sets
	// to its type; the relay copies it to session metadata identity_type.
	IdentityTypeAttribute = "identity_type"

	// KubernetesTokenFile is the pod's projected service account token,
	// what a verifier or source presents when it logs in as the pod.
	KubernetesTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

// knownIdentityTypes is the vocabulary Config.validate accepts, built-in or
// nested, so a typo is refused everywhere and a missing module only on the
// host.
var knownIdentityTypes = []string{IdentityNone, IdentityKubernetes, IdentityHoopCredential, IdentityJWT, IdentitySPIFFE}

// PeerInfo is what a verifier may know about the connection: the lane's
// transport, the peer address when the relay has it, and whether the client
// leg is TLS.
type PeerInfo struct {
	Network string
	Addr    string
	TLS     bool
}

// IdentitySource verifies the client leg of a login. user is the login user
// the protocol carried ("" when it carries none), secret the password
// field, bearer or key. A refusal wraps codectypes.ErrLoginRefused; any
// other error is a verifier failure, which also refuses the login but is
// logged as one.
type IdentitySource interface {
	Verify(ctx context.Context, user, secret string, peer PeerInfo) (session.Identity, error)
}

// IdentityDeps is what a verifier builder may draw on. Roots is the process
// trust pool (nil is the host store); Log is the lane's logger. Lane and
// CredentialSet back the hoop_credential verifier: the set the plane
// serves, read on every login, scoped to this listener.
type IdentityDeps struct {
	Roots         *x509.CertPool
	Log           *slog.Logger
	Lane          string
	CredentialSet CredentialSet
}

// IdentityBuilder builds one lane's verifier. It must not dial or read a
// file: Validate constructs it on the control plane too.
type IdentityBuilder func(cfg IdentityConfig, deps IdentityDeps) (IdentitySource, error)

var (
	identityMu       sync.Mutex
	identityBuilders = map[string]IdentityBuilder{}
)

// RegisterIdentitySource links a verifier type into this binary. A nested
// module calls it from init; a duplicate panics so the choice cannot depend
// on import order.
func RegisterIdentitySource(typ string, build IdentityBuilder) {
	if typ == "" || build == nil {
		panic("sidecar/daemon: RegisterIdentitySource needs a type and a builder")
	}
	identityMu.Lock()
	defer identityMu.Unlock()
	if _, dup := identityBuilders[typ]; dup {
		panic("sidecar/daemon: duplicate identity source " + typ)
	}
	identityBuilders[typ] = build
}

// RegisteredIdentitySource is the builder linked for a type, if any.
func RegisteredIdentitySource(typ string) (IdentityBuilder, bool) {
	identityMu.Lock()
	defer identityMu.Unlock()
	b, ok := identityBuilders[typ]
	return b, ok
}

// identityTypes lists the registered verifier types, sorted, for the
// capabilities header.
func identityTypes() []string {
	identityMu.Lock()
	defer identityMu.Unlock()
	out := make([]string, 0, len(identityBuilders))
	for t := range identityBuilders {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// Refused builds a login refusal. The text reaches the log and, through the
// driver's error frame, the client; it names what was wrong and never the
// secret.
func Refused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", codectypes.ErrLoginRefused, fmt.Sprintf(format, args...))
}

// checkIdentitySources refuses a lane whose identity type this build did
// not link. Run and Validate call it; Config.Validate does not, because the
// control plane validates with the same decoder and links no module.
func checkIdentitySources(cfg *Config) error {
	for i, l := range cfg.Listeners {
		if l.Identity == nil {
			continue
		}
		if _, ok := RegisteredIdentitySource(l.Identity.Type); !ok {
			return fmt.Errorf("%s: identity type %q is not linked into this build; "+
				"use the hoop-inspect binary, which imports %s",
				l.displayName(i), l.Identity.Type, identityModuleFor(l.Identity.Type))
		}
	}
	return nil
}

// identityModuleFor names the nested module that registers a verifier type.
func identityModuleFor(typ string) string {
	if typ == IdentitySPIFFE {
		return "sidecar/identity/spiffe"
	}
	return "sidecar/auth"
}
