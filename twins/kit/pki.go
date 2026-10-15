package kit

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// CA is a test CA minted per run. The code under test trusts it through
// SSL_CERT_FILE, which replaces the system roots, so no real service can
// answer. Linux only: darwin ignores SSL_CERT_FILE.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	PEM  []byte
}

// NewCA mints a CA valid for one day.
func NewCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "hoop twins test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

// Leaf signs a server certificate for hosts (DNS names or IPs).
func (ca *CA) Leaf(hosts ...string) (certPEM, keyPEM []byte, err error) {
	if len(hosts) == 0 {
		return nil, nil, fmt.Errorf("a leaf needs at least one host")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0]},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, nil, err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), nil
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

// WritePKI writes ca.crt and <name>.crt/<name>.key for each leaf into dir.
// It never writes the CA key: nothing signs after this step. It keeps a dir
// that already holds ca.crt, so a second `compose up` does not change the CA
// under twins that already loaded their certificates.
func WritePKI(dir string, leaves map[string][]string) error {
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); err == nil {
		slog.Info("pki kept: the dir already holds a CA", "dir", dir)
		return nil
	}
	ca, err := NewCA()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, hosts := range leaves {
		c, k, err := ca.Leaf(hosts...)
		if err != nil {
			return fmt.Errorf("leaf %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".crt"), c, 0o644); err != nil {
			return err
		}
		// 0644: under compose, the twins run as another uid than the one that mints.
		if err := os.WriteFile(filepath.Join(dir, name+".key"), k, 0o644); err != nil {
			return err
		}
	}
	// Last, so a dir with ca.crt always holds every leaf too.
	return os.WriteFile(filepath.Join(dir, "ca.crt"), ca.PEM, 0o644)
}

// Serve runs the twin: TLS on tlsAddr (the surface hoop calls) and plain
// HTTP on adminAddr (where a scenario seeds and asserts). Both serve the
// same handler. It blocks until one listener fails.
func Serve(t Twin, tlsAddr, adminAddr, certFile, keyFile string) error {
	h := Handler(t)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	errc := make(chan error, 2)
	go func() {
		srv := &http.Server{Addr: tlsAddr, Handler: h, ReadHeaderTimeout: 10 * time.Second,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
		errc <- srv.ListenAndServeTLS("", "")
	}()
	go func() {
		srv := &http.Server{Addr: adminAddr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		errc <- srv.ListenAndServe()
	}()
	slog.Info("twin listening", "twin", t.Name(), "tls", tlsAddr, "admin", adminAddr)
	return <-errc
}
