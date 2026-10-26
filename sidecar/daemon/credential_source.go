package daemon

import (
	"context"
	"crypto/x509"
	"fmt"
	"slices"
	"sync"

	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
)

// The source vocabulary a pool entry may name. The built-in sources register
// from init below; spiffe and kerberos register from the nested
// sidecar/identity modules, so a build without them knows the name and
// refuses the lane on the host (checkCredentialSources), while the control
// plane accepts it.
const (
	SourceSecretFiles      = "secret_files"
	SourceGCPImpersonation = "gcp_impersonation"
	SourceX509             = "x509"
	SourceVault            = "vault"
	SourceGCPSecretManager = "gcp_secret_manager"
	SourceSPIFFE           = "spiffe"
	SourceKerberos         = "kerberos"
)

var knownCredentialSources = []string{SourceSecretFiles, SourceGCPImpersonation, SourceX509, SourceVault, SourceGCPSecretManager, SourceSPIFFE, SourceKerberos}

// Credential is what a source materializes for one login: the relay's own
// type, so a source fills what the driver presents upstream. A source fills
// everything but Name; the daemon sets Name from the pool entry.
type Credential = proxy.Credential

// CredentialSource materializes a pool entry for a verified identity. It is
// called per login, so a source that reads a file sees a rotated secret and
// a source that mints a token caches it itself.
type CredentialSource interface {
	Credential(ctx context.Context, entry CredentialEntry, id session.Identity) (Credential, error)
}

// CredentialSourceBuilder builds one source for the process from the
// top-level credentials block. It is called once, for a source whose block
// is present, and must not dial: Validate builds it on the control plane
// too.
type CredentialSourceBuilder func(cfg *CredentialSourcesConfig) (CredentialSource, error)

var (
	sourceMu       sync.Mutex
	sourceBuilders = map[string]CredentialSourceBuilder{}
)

// RegisterCredentialSource links a credential source into this binary. A
// nested module calls it from init; a duplicate panics so the choice cannot
// depend on import order.
func RegisterCredentialSource(name string, build CredentialSourceBuilder) {
	if name == "" || build == nil {
		panic("sidecar/daemon: RegisterCredentialSource needs a name and a builder")
	}
	sourceMu.Lock()
	defer sourceMu.Unlock()
	if _, dup := sourceBuilders[name]; dup {
		panic("sidecar/daemon: duplicate credential source " + name)
	}
	sourceBuilders[name] = build
}

func registeredCredentialSource(name string) (CredentialSourceBuilder, bool) {
	sourceMu.Lock()
	defer sourceMu.Unlock()
	b, ok := sourceBuilders[name]
	return b, ok
}

// credentialSourceNames lists the registered sources, sorted, for the
// capabilities header.
func credentialSourceNames() []string {
	sourceMu.Lock()
	defer sourceMu.Unlock()
	out := make([]string, 0, len(sourceBuilders))
	for n := range sourceBuilders {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// configured reports whether the block for a source is present. Presence is
// what configures a source; the block's own fields may all be optional.
func (c *CredentialSourcesConfig) configured(name string) bool {
	if c == nil {
		return false
	}
	switch name {
	case SourceSecretFiles:
		return c.SecretFiles != nil
	case SourceGCPImpersonation:
		return c.GCPImpersonation != nil
	case SourceX509:
		return c.X509 != nil
	case SourceVault:
		return c.Vault != nil
	case SourceGCPSecretManager:
		return c.GCPSecretManager != nil
	case SourceSPIFFE:
		return c.SPIFFE != nil
	case SourceKerberos:
		return c.Kerberos != nil
	}
	return false
}

// validate checks the top-level block's own fields.
func (c *CredentialSourcesConfig) validate() []string {
	if c == nil {
		return nil
	}
	var problems []string
	if c.SecretFiles != nil && c.SecretFiles.Dir == "" {
		problems = append(problems, "credentials.secret_files.dir is required: the directory the user and password files are under")
	}
	if k := c.Kerberos; k != nil && k.Cache != "" && k.Keytab != "" {
		problems = append(problems, "credentials.kerberos names both a cache and a keytab; keep one")
	}
	if v := c.Vault; v != nil {
		for _, p := range v.validate() {
			problems = append(problems, "credentials.vault: "+p)
		}
	}
	return problems
}

// sourcesInUse lists the sources the listeners' pools name, each once.
func (c *Config) sourcesInUse() []string {
	var out []string
	for _, l := range c.Listeners {
		if l.Credentials == nil {
			continue
		}
		for _, e := range l.Credentials.Pool {
			if !slices.Contains(out, e.Source) {
				out = append(out, e.Source)
			}
		}
	}
	slices.Sort(out)
	return out
}

// checkCredentialSources refuses a configured source block, or a pool entry
// naming one, that this build did not link. Run and Validate call it;
// Config.Validate does not, because the control plane validates with the
// same decoder and links no module.
func checkCredentialSources(cfg *Config) error {
	for _, name := range knownCredentialSources {
		if _, ok := registeredCredentialSource(name); ok {
			continue
		}
		if cfg.Credentials.configured(name) || slices.Contains(cfg.sourcesInUse(), name) {
			return fmt.Errorf("credential source %q is not linked into this build; "+
				"use the hoop-inspect binary, which imports sidecar/identity/%s", name, moduleFor(name))
		}
	}
	return nil
}

// moduleFor names the nested module that registers a source.
func moduleFor(source string) string {
	if source == SourceKerberos {
		return "krb5"
	}
	return source
}

// buildCredentialSources builds every source the config's pools name, once
// per process. The builders are registered; a pool entry naming an
// unregistered or unconfigured source was refused before this runs. roots
// is the process trust pool, handed to a source that dials through it.
func buildCredentialSources(cfg *Config, roots *x509.CertPool) (map[string]CredentialSource, error) {
	out := map[string]CredentialSource{}
	for _, name := range cfg.sourcesInUse() {
		build, ok := registeredCredentialSource(name)
		if !ok {
			return nil, fmt.Errorf("credential source %q is not linked into this build", name)
		}
		if !cfg.Credentials.configured(name) {
			return nil, fmt.Errorf("credential source %q has no credentials.%s block", name, name)
		}
		src, err := build(cfg.Credentials)
		if err != nil {
			return nil, fmt.Errorf("credentials.%s: %w", name, err)
		}
		if r, ok := src.(interface{ useRoots(*x509.CertPool) }); ok {
			r.useRoots(roots)
		}
		out[name] = src
	}
	return out, nil
}

func init() {
	RegisterCredentialSource(SourceSecretFiles, buildSecretFilesSource)
	RegisterCredentialSource(SourceGCPImpersonation, buildGCPImpersonationSource)
	RegisterCredentialSource(SourceX509, buildX509Source)
	RegisterCredentialSource(SourceVault, buildVaultSource)
	RegisterCredentialSource(SourceGCPSecretManager, buildGCPSecretManagerSource)
}
