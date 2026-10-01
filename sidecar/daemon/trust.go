package daemon

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

// TrustConfig names extra certificate authorities every outbound TLS client
// of this process trusts: the analyzer's model calls, the descriptor fetches,
// the identity resolver, and each lane's upstream_tls that sets no ca_file.
//
// It exists for a network that re-signs egress TLS with its own CA, a
// transparent MITM proxy in front of Google's APIs being the case that asked
// for it. The bundle is APPENDED to the host trust store, never substituted
// for it. Go's own knob, SSL_CERT_FILE, replaces the system roots: an operator
// who adds the proxy's CA that way loses every public root, and each endpoint
// the proxy does not intercept then fails verification.
type TrustConfig struct {
	// CAFile is a PEM bundle of one or more CA certificates.
	CAFile string `json:"ca_file,omitempty"`
}

// loadTrustRoots resolves the trust section into the pool outbound clients
// verify against: the system roots plus every certificate in CAFile.
//
// The (nil, nil) result is a real value, not a missing one: no section, or
// no ca_file, means "the host trust store as Go loads it", which is what a
// nil RootCAs selects in crypto/tls. Returning a copy of the system pool
// instead would read the same and pin a snapshot of the store taken at
// startup, where nil re-reads nothing and changes nothing from a build that
// predates the key.
//
// A missing file, or one with no certificate in it, is an error. A bundle the
// operator named and this process could not use would otherwise leave every
// intercepted endpoint failing verification at first use, far from the typo.
func loadTrustRoots(t *TrustConfig) (*x509.CertPool, error) {
	if t == nil || t.CAFile == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(t.CAFile)
	if err != nil {
		return nil, fmt.Errorf("trust.ca_file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		// Appending to an empty pool would quietly turn "add this CA" into
		// "trust only this CA", which is the SSL_CERT_FILE trap this
		// section exists to avoid.
		return nil, fmt.Errorf("trust.ca_file %q: the host trust store is unavailable, "+
			"so the bundle cannot be added to it: %w", t.CAFile, err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("trust.ca_file %q contains no usable PEM certificate", t.CAFile)
	}
	return pool, nil
}

// outboundHTTPClient is the client every outbound HTTPS call of this process
// sends through, so the trust section reaches all of them.
//
// Nil roots is the host trust store and returns a client with no settings of
// its own, which behaves exactly as http.DefaultClient: its nil Transport is
// http.DefaultTransport. Otherwise the transport is a clone of the default
// one, keeping its proxy-from-environment, dial and idle settings, with only
// RootCAs replaced. A timeout is not set here: each caller's context owns the
// deadline of its own call.
func outboundHTTPClient(roots *x509.CertPool) *http.Client {
	if roots == nil {
		return &http.Client{}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{}
	}
	tr.TLSClientConfig.RootCAs = roots
	return &http.Client{Transport: tr}
}
