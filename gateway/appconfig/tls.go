package appconfig

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
)

// loadTlsConfigOnce ensures TLS config is loaded or generated only once.
var loadTlsConfigOnce = sync.OnceValues(loadOrGenerateTlsConfig)

// GetTLSConfig returns the TLS configuration, loading it from files or generating a self-signed certificate if necessary.
func (c Config) GetTLSConfig() (*tls.Config, error) {
	if c.GatewayUseTLS() {
		return loadTlsConfigOnce()
	}
	return nil, nil
}

// GatewayAllowPlaintext indicates if plaintext (non-TLS) connections are allowed.
func (c Config) GatewayAllowPlaintext() bool {
	return c.gatewayAllowPlainText
}

// loadOrGenerateTlsConfig loads TLS configuration from files or generates a self-signed certificate if files are not provided.
func loadOrGenerateTlsConfig() (tlsConfig *tls.Config, err error) {
	certData, keyData := Get().GatewayTLSCert(), Get().GatewayTLSKey()
	if certData != "" && keyData != "" {
		cert, err := tls.X509KeyPair([]byte(certData), []byte(keyData))
		if err != nil {
			return nil, err
		}
		log.Info("loaded TLS certificate from config")
		return buildTLSConfig(cert), nil
	}

	log.Warnf("no TLS certificate and/or key file provided, generating self-signed certificate")
	cert, err := generateSelfSignedCert()
	if err != nil {
		return nil, err
	}
	return buildTLSConfig(cert), nil
}

// generateSelfSignedCert creates a self-signed TLS certificate
func generateSelfSignedCert() (cert tls.Certificate, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return cert, fmt.Errorf("failed to generate private key: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return cert, fmt.Errorf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"Hoop Gateway"},
			Country:      []string{"US"},
		},
		NotBefore:             time.Now().UTC(),
		NotAfter:              time.Now().UTC().Add(time.Hour * 24 * 365), // valid for 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return cert, fmt.Errorf("failed to create certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

// buildTLSConfig constructs a tls.Config from the provided certificate and certificate pool.
func buildTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
}
