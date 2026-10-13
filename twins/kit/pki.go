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
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// CA is a throwaway test CA. Code under test trusts it through
// SSL_CERT_FILE (Linux only: darwin ignores the variable).
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
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
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
	cert, _ := x509.ParseCertificate(der)
	return &CA{cert: cert, key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

// Leaf signs a server certificate for hosts (DNS names or IPs).
func (ca *CA) Leaf(hosts ...string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
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

// WritePKI writes ca.crt and <name>.crt/<name>.key for each leaf into dir.
// The CA key is never written: nothing signs after this step. A dir that
// already holds ca.crt is kept, so a second `compose up` does not swap the
// CA under twins that already loaded their certificates.
func WritePKI(dir string, leaves map[string][]string) error {
	if _, err := os.Stat(filepath.Join(dir, "ca.crt")); err == nil {
		log.Printf("[pki] %s already holds a CA, kept", dir)
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
		// 0644: the twins run as another uid than the one minting.
		if err := os.WriteFile(filepath.Join(dir, name+".key"), k, 0o644); err != nil {
			return err
		}
	}
	// Last, so a dir with ca.crt always holds every leaf too.
	return os.WriteFile(filepath.Join(dir, "ca.crt"), ca.PEM, 0o644)
}

// Serve runs the twin: TLS on tlsAddr (the surface hoop calls) and plain
// HTTP on adminAddr (where a scenario seeds and asserts). Both serve the
// same handler. It blocks.
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
	log.Printf("[%s] tls on %s, admin on %s", t.Name(), tlsAddr, adminAddr)
	return <-errc
}
