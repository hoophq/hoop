package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCA is a certificate authority no host trusts, standing in for the CA
// an egress proxy re-signs intercepted TLS with.
type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	pemFile string
}

func newTestCA(t *testing.T, name string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name+".pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return testCA{cert: cert, key: key, pemFile: path}
}

// leaf issues a server certificate for 127.0.0.1.
func (ca testCA) leaf(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// server is an HTTPS endpoint presenting a certificate ca issued, the shape
// of an intercepted Google API behind the proxy.
func (ca testCA) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leaf(t)}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func mustTrustRoots(t *testing.T, caFile string) *x509.CertPool {
	t.Helper()
	roots, err := loadTrustRoots(&TrustConfig{CAFile: caFile})
	if err != nil {
		t.Fatalf("loadTrustRoots: %v", err)
	}
	return roots
}

// The bundle is ADDED to the host store. A pool holding only the proxy's CA
// is the SSL_CERT_FILE trap: every endpoint the proxy does not intercept then
// fails verification.
//
// Two proofs, because hosts differ. CertPool.Equal compares the system-pool
// flag (macOS, Windows: the platform verifier) and every certificate added
// (a file-backed store, Linux), so equality with "system pool plus bundle"
// and inequality with "bundle alone" hold on every host that has a store.
// Where the host's store is the Go verifier over a file, a public chain is
// also verified end to end: www.google.com as served in February 2023,
// copied from Go's crypto/x509 verify_test.go (googleLeaf, gtsIntermediate),
// checked at that date against GTS Root R1. macOS reports GTS CA 1C3 revoked
// now, so there the chain is not a usable witness and only Equal speaks.
func TestLoadTrustRootsAppendsToTheHostStore(t *testing.T) {
	ca := newTestCA(t, "egress-proxy-ca")
	roots := mustTrustRoots(t, ca.pemFile)

	leaf, err := x509.ParseCertificate(ca.leaf(t).Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		t.Errorf("a certificate the bundle's CA issued does not verify: %v", err)
	}

	raw, err := os.ReadFile("testdata/google-2023-chain.pem")
	if err != nil {
		t.Fatal(err)
	}
	var chain []*x509.Certificate
	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, c)
	}
	intermediates := x509.NewCertPool()
	intermediates.AddCert(chain[1])
	public := func(pool *x509.CertPool) error {
		_, err := chain[0].Verify(x509.VerifyOptions{
			Roots:         pool,
			Intermediates: intermediates,
			DNSName:       "www.google.com",
			CurrentTime:   time.Unix(1677615892, 0),
		})
		return err
	}
	system, err := x509.SystemCertPool()
	if err != nil {
		t.Fatalf("SystemCertPool: %v", err)
	}
	bundle, err := os.ReadFile(ca.pemFile)
	if err != nil {
		t.Fatal(err)
	}
	want, bundleOnly := system.Clone(), x509.NewCertPool()
	want.AppendCertsFromPEM(bundle)
	bundleOnly.AppendCertsFromPEM(bundle)
	if !roots.Equal(want) {
		t.Error("the pool is not the host store plus the bundle")
	}
	if want.Equal(bundleOnly) {
		t.Skip("this host has no system roots to append to")
	}
	if roots.Equal(bundleOnly) {
		t.Error("the pool is the bundle alone; the host store was replaced")
	}
	if public(system) == nil {
		if err := public(roots); err != nil {
			t.Errorf("a public chain the host store verifies no longer verifies with the bundle added: %v", err)
		}
	}
}

// No section and an empty ca_file are the host store, spelled nil: crypto/tls
// reads a nil RootCAs as the system roots.
func TestLoadTrustRootsWithNoBundleIsTheHostStore(t *testing.T) {
	for _, tc := range []*TrustConfig{nil, {}} {
		roots, err := loadTrustRoots(tc)
		if roots != nil || err != nil {
			t.Errorf("loadTrustRoots(%+v) = %v, %v; want nil, nil", tc, roots, err)
		}
	}
}

// A bundle the operator named and this process cannot use is a config error
// at load, naming the file. Deferred to first use, it would be every
// intercepted call failing verification with nothing pointing at the typo.
func TestAnUnusableTrustBundleFailsValidateNamingThePath(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.pem")
	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(garbage, []byte("-----BEGIN CERTIFICATE-----\nnot base64\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing": filepath.Join(dir, "absent.pem"),
		"empty":   empty,
		"garbage": garbage,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{Trust: &TrustConfig{CAFile: path}}
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate accepted an unusable trust bundle")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name %s", err, path)
			}
		})
	}
}

func handshake(t *testing.T, srv *httptest.Server, conf *tls.Config) error {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", srv.Listener.Addr().String(), conf)
	if err != nil {
		return err
	}
	return conn.Close()
}

// An upstream_tls with no ca_file verifies against the trust roots, so a
// lane dialing through the proxy needs no per-lane copy of its CA. An
// explicit ca_file still pins the upstream to that bundle alone.
func TestBuildTLSUsesTheTrustRootsOnlyWithoutACAFile(t *testing.T) {
	ca := newTestCA(t, "egress-proxy-ca")
	srv := ca.server(t)
	roots := mustTrustRoots(t, ca.pemFile)

	conf, err := (&TLSConfig{}).BuildTLS(roots)
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, srv, conf); err != nil {
		t.Errorf("empty ca_file with the proxy CA in the trust roots: %v", err)
	}

	conf, err = (&TLSConfig{}).BuildTLS(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, srv, conf); err == nil {
		t.Error("empty ca_file with no trust roots accepted a certificate the host does not trust")
	}

	other := newTestCA(t, "backend-ca")
	conf, err = (&TLSConfig{CAFile: other.pemFile}).BuildTLS(roots)
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake(t, srv, conf); err == nil {
		t.Error("an explicit ca_file was widened by the trust roots")
	}
}

// Every outbound HTTPS client of the process is built here, so this is the
// one place the trust roots have to land for the analyzer, the descriptor
// fetch and the identity resolver to verify through the proxy.
func TestOutboundHTTPClientVerifiesAgainstTheTrustRoots(t *testing.T) {
	ca := newTestCA(t, "egress-proxy-ca")
	srv := ca.server(t)

	resp, err := outboundHTTPClient(mustTrustRoots(t, ca.pemFile), "test").Get(srv.URL)
	if err != nil {
		t.Fatalf("GET through the trust roots: %v", err)
	}
	_ = resp.Body.Close()

	if resp, err := outboundHTTPClient(nil, "test").Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Error("a client with no trust roots accepted a certificate the host does not trust")
	}
}
