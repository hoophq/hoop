package spiffe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
)

// source is the `spiffe` credential source: the X509-SVIDs the Workload API
// issues this workload, one of which a pool entry names by SPIFFE ID.
type source struct {
	conn *conn
}

func buildSource(cfg *daemon.CredentialSourcesConfig) (daemon.CredentialSource, error) {
	if cfg == nil || cfg.SPIFFE == nil {
		return nil, errors.New("credentials.spiffe is not configured")
	}
	return &source{conn: connFor(socketAddr(cfg.SPIFFE.Socket))}, nil
}

// Credential returns the SVID whose ID is entry.SPIFFEID as a TLS client
// certificate, with entry.User as the database user the certificate maps
// to. The agent decides which SVIDs this workload holds; an entry naming
// one it does not issue is a configuration error, not a fallback to the
// default SVID.
func (s *source) Credential(ctx context.Context, entry daemon.CredentialEntry, _ session.Identity) (daemon.Credential, error) {
	if entry.SPIFFEID == "" {
		return daemon.Credential{}, fmt.Errorf("credential %q: spiffe_id is required", entry.Name)
	}
	want, err := spiffeid.FromString(entry.SPIFFEID)
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: spiffe_id: %w", entry.Name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	svids, err := s.conn.x509SVIDs(ctx)
	if err != nil {
		return daemon.Credential{}, err
	}
	svid := find(svids, want)
	if svid == nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: the workload api issues no x509-svid for %s", entry.Name, want)
	}
	return daemon.Credential{User: entry.User, Cert: asTLS(svid)}, nil
}

func find(svids []*x509svid.SVID, id spiffeid.ID) *x509svid.SVID {
	for _, svid := range svids {
		if svid.ID == id {
			return svid
		}
	}
	return nil
}

// asTLS shapes an SVID as crypto/tls wants a client certificate: the chain
// as raw DER, leaf first, and the key the agent delivered with it.
func asTLS(svid *x509svid.SVID) *tls.Certificate {
	cert := &tls.Certificate{
		Certificate: make([][]byte, 0, len(svid.Certificates)),
		PrivateKey:  svid.PrivateKey,
		Leaf:        svid.Certificates[0],
	}
	for _, c := range svid.Certificates {
		cert.Certificate = append(cert.Certificate, c.Raw)
	}
	return cert
}
