// Package krb5 links gokrb5 into a sidecar binary as the `kerberos`
// credential source: a pool entry names a principal, and the upstream leg
// authenticates to SQL Server (SSPI) or Postgres (GSSAPI) with a Kerberos
// ticket for it instead of a password.
//
// The source reads its material on every Credential call. A ccache is the
// platform's to rotate (a kinit cron, a sidecar such as kerberos-sidecar or
// the GKE Kerberos integration), so the file is loaded per login and never
// cached here; a keytab is small and read the same way so a rotated key
// takes effect without a restart. The constructor reads nothing, because
// Validate builds the source on the control plane where the files do not
// exist.
package krb5

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/jcmturner/gokrb5/v8/client"
	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/keytab"
)

// SourceName is the pool `source` value this package registers.
const SourceName = "kerberos"

// DefaultConfig is the krb5.conf read when `credentials.kerberos.config`
// is empty.
const DefaultConfig = "/etc/krb5.conf"

func init() {
	daemon.RegisterCredentialSource(SourceName, build)
}

// source is the `kerberos` credential source for one process.
type source struct {
	cache  string
	keytab string
	config string
}

func build(cfg *daemon.CredentialSourcesConfig) (daemon.CredentialSource, error) {
	if cfg == nil || cfg.Kerberos == nil {
		return nil, errors.New("credentials.kerberos is not configured")
	}
	k := cfg.Kerberos
	switch {
	case k.Cache == "" && k.Keytab == "":
		return nil, errors.New("credentials.kerberos: one of cache or keytab is required")
	case k.Cache != "" && k.Keytab != "":
		return nil, errors.New("credentials.kerberos: cache and keytab are exclusive")
	}
	s := &source{cache: k.Cache, keytab: k.Keytab, config: k.Config}
	if s.config == "" {
		s.config = DefaultConfig
	}
	return s, nil
}

// Credential builds a Kerberos client for the entry's principal from the
// configured material and returns it as a GSS initiator. Release destroys
// the client, which stops the TGT renewal gokrb5 starts after a keytab
// login; without it every login would leave a goroutine behind.
func (s *source) Credential(_ context.Context, entry daemon.CredentialEntry, _ session.Identity) (daemon.Credential, error) {
	conf, err := config.Load(s.config)
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: krb5.conf %s: %w", entry.Name, s.config, err)
	}
	var cl *client.Client
	var user string
	if s.cache != "" {
		cl, user, err = s.fromCache(conf, entry)
	} else {
		cl, user, err = s.fromKeytab(conf, entry)
	}
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: %w", entry.Name, err)
	}
	return daemon.Credential{
		User: user,
		GSS:  &initiator{client: cl},
		Release: func(context.Context) error {
			cl.Destroy()
			return nil
		},
	}, nil
}

// fromCache loads the ccache. The ccache names its own principal; an entry
// naming a different one is a misconfiguration, and an entry naming none
// takes the ccache's.
func (s *source) fromCache(conf *config.Config, entry daemon.CredentialEntry) (*client.Client, string, error) {
	cc, err := credentials.LoadCCache(s.cache)
	if err != nil {
		return nil, "", fmt.Errorf("ccache %s: %w", s.cache, err)
	}
	held := cc.GetClientPrincipalName().PrincipalNameString() + "@" + cc.GetClientRealm()
	if entry.Principal != "" && !samePrincipal(entry.Principal, held, conf.LibDefaults.DefaultRealm) {
		return nil, "", fmt.Errorf("ccache %s holds %s, the entry names %s", s.cache, held, entry.Principal)
	}
	cl, err := client.NewFromCCache(cc, conf, clientSettings...)
	if err != nil {
		return nil, "", fmt.Errorf("ccache %s: %w", s.cache, err)
	}
	return cl, held, nil
}

// fromKeytab loads the keytab and prepares a client for entry.Principal.
// The TGT is fetched at Initiate, so this touches no KDC.
func (s *source) fromKeytab(conf *config.Config, entry daemon.CredentialEntry) (*client.Client, string, error) {
	if entry.Principal == "" {
		return nil, "", errors.New("principal is required with a keytab")
	}
	kt, err := keytab.Load(s.keytab)
	if err != nil {
		return nil, "", fmt.Errorf("keytab %s: %w", s.keytab, err)
	}
	name, realm := splitPrincipal(entry.Principal)
	if realm == "" {
		realm = conf.LibDefaults.DefaultRealm
	}
	if realm == "" {
		return nil, "", fmt.Errorf("principal %s names no realm and krb5.conf has no default_realm", entry.Principal)
	}
	if !holdsPrincipal(kt, name, realm) {
		return nil, "", fmt.Errorf("keytab %s has no entry for %s@%s", s.keytab, name, realm)
	}
	return client.NewWithKeytab(name, realm, kt, conf, clientSettings...), name + "@" + realm, nil
}

// clientSettings apply to every client. FAST pre-authentication is off
// because Active Directory KDCs refuse it from gokrb5, and every Kerberos
// upstream this source exists for (SQL Server, Postgres on a Windows
// domain) sits behind one.
var clientSettings = []func(*client.Settings){client.DisablePAFXFAST(true)}

// splitPrincipal splits user@REALM. A principal with no @ has no realm.
func splitPrincipal(p string) (name, realm string) {
	if i := strings.LastIndexByte(p, '@'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

// samePrincipal compares two principals, completing a missing realm with
// the default so `svc` and `svc@REALM` agree when REALM is the default.
func samePrincipal(a, b, defaultRealm string) bool {
	an, ar := splitPrincipal(a)
	bn, br := splitPrincipal(b)
	if ar == "" {
		ar = defaultRealm
	}
	if br == "" {
		br = defaultRealm
	}
	return an == bn && strings.EqualFold(ar, br)
}

// holdsPrincipal reports whether the keytab has a key for name@realm, so a
// typo fails at Credential with the file named instead of at the KDC with
// a pre-authentication error.
func holdsPrincipal(kt *keytab.Keytab, name, realm string) bool {
	for _, e := range kt.Entries {
		if strings.EqualFold(e.Principal.Realm, realm) && strings.Join(e.Principal.Components, "/") == name {
			return true
		}
	}
	return false
}
