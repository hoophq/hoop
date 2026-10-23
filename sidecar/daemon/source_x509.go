package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/hoophq/hoop/sidecar/session"
)

// x509Source presents a client certificate named per pool entry. The pair is
// loaded per login, so a certificate the platform renewed in place is what
// the next connection offers, and the key file has to be unreadable by
// group and other, as ssh(1) demands of a key.
type x509Source struct{}

func buildX509Source(cfg *CredentialSourcesConfig) (CredentialSource, error) {
	if cfg == nil || cfg.X509 == nil {
		return nil, errors.New("credentials.x509 is not configured")
	}
	return x509Source{}, nil
}

func (x509Source) validateEntry(e CredentialEntry) error {
	if e.CertFile == "" || e.KeyFile == "" {
		return errors.New("cert_file and key_file are required")
	}
	return nil
}

func (s x509Source) Credential(_ context.Context, e CredentialEntry, _ session.Identity) (Credential, error) {
	if err := s.validateEntry(e); err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	cert, err := loadClientCertificate(e.CertFile, e.KeyFile)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	user := e.User
	if user == "" && cert.Leaf != nil {
		user = cert.Leaf.Subject.CommonName
	}
	return Credential{User: user, Cert: cert}, nil
}

// loadClientCertificate reads a keypair with the key mode-checked, and keeps
// the parsed leaf so the user can default to its common name.
func loadClientCertificate(certFile, keyFile string) (*tls.Certificate, error) {
	info, err := os.Stat(keyFile)
	if err != nil {
		return nil, err
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("%s is readable by group or other (mode %04o); mount it 0400", keyFile, mode)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	if cert.Leaf == nil && len(cert.Certificate) > 0 {
		if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
			cert.Leaf = leaf
		}
	}
	return &cert, nil
}
