package analyzer

import (
	"fmt"
	"sort"
	"sync"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// Providers and builders register themselves the way codecs do, and for the
// same reason: a provider that needs an SDK (Vertex needs GCP OAuth2) lives
// in its own module, and this package must not import it. The binary decides
// what it can speak by what it links; the config decides what it does speak.
var (
	providerMu sync.RWMutex
	providers  = map[string]func(Options) (Provider, error){}

	builderMu sync.RWMutex
	builders  = map[inspect.Protocol]Builder{}
)

// Options carries the provider-independent settings from the config's
// analyzer section. A provider reads what applies to it and ignores the rest.
type Options struct {
	// Model names the model to call. Provider-specific format.
	Model string

	// Endpoint overrides the provider's default base URL. Empty uses the
	// provider default.
	Endpoint string

	// Credential is the secret material, already read from disk by the
	// caller. Empty is legitimate for a provider that resolves ambient
	// credentials (Vertex under Workload Identity).
	Credential Secret

	// Extra carries provider-specific settings the common schema cannot
	// express, such as a GCP project and region.
	Extra map[string]string

	// MaxOutputTokens bounds the model's reply. Zero uses the provider
	// default.
	MaxOutputTokens int
}

// Register makes a provider available to NewProvider.
//
// Call it from a package init. It panics on a duplicate name, because two
// providers claiming one name can only be a build mistake and picking a
// winner would make behavior depend on import order.
func Register(name string, build func(Options) (Provider, error)) {
	if name == "" {
		panic("sidecar/analyzer: Register called with an empty name")
	}
	if build == nil {
		panic("sidecar/analyzer: Register called with a nil builder")
	}
	providerMu.Lock()
	defer providerMu.Unlock()
	if _, dup := providers[name]; dup {
		panic("sidecar/analyzer: duplicate provider registration for " + name)
	}
	providers[name] = build
}

// NewProvider builds the named provider.
//
// The error names what IS linked, because the common failure is a config
// asking for a provider this build does not carry, and "unknown provider
// vertex" without that list sends an operator to the wrong file.
func NewProvider(name string, opts Options) (Provider, error) {
	providerMu.RLock()
	build, ok := providers[name]
	providerMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w %q (%s)", ErrUnknownProvider, name,
			describeProviders(RegisteredProviders()))
	}
	return build(opts)
}

// RegisteredProviders lists the provider names linked into this binary,
// sorted so a startup log line and an error message are stable.
func RegisteredProviders() []string {
	providerMu.RLock()
	defer providerMu.RUnlock()
	out := make([]string, 0, len(providers))
	for name := range providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RegisterBuilder makes a content builder available for a protocol. Call it
// from a package init. It panics on a duplicate protocol.
func RegisterBuilder(b Builder) {
	if b == nil {
		panic("sidecar/analyzer: RegisterBuilder called with nil")
	}
	p := b.Protocol()
	builderMu.Lock()
	defer builderMu.Unlock()
	if _, dup := builders[p]; dup {
		panic("sidecar/analyzer: duplicate builder registration for protocol " + string(p))
	}
	builders[p] = b
}

// BuilderFor returns the content builder for a protocol.
func BuilderFor(p inspect.Protocol) (Builder, bool) {
	builderMu.RLock()
	defer builderMu.RUnlock()
	b, ok := builders[p]
	return b, ok
}
