package appconfig

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"github.com/hoophq/hoop/common/log"
	"math/big"
	"os"
	"sync"
	"time"
)

// loadTlsConfigOnce ensures TLS config is loaded or generated only once.
var loadTlsConfigOnce = sync.OnceValues(loadOrGenerateTlsConfig)

// GetTLSConfig returns the TLS configuration, loading it from files or generating a self-signed certificate if necessary.
func (c Config) GetTLSConfig() (*tls.Config, error) {
	return loadTlsConfigOnce()
}

// GatewayAllowPlaintext indicates if plaintext (non-TLS) connections are allowed.
func (c Config) GatewayAllowPlaintext() bool {
	return c.gatewayAllowPlainText
}

// loadOrGenerateTlsConfig loads TLS configuration from files or generates a self-signed certificate if files are not provided.
func loadOrGenerateTlsConfig() (tlsConfig *tls.Config, err error) {

	if os.Getenv("GENERATE_SELF_SIGNED_TLS") == "true" {
		log.Infof("GENERATE_SELF_SIGNED_TLS is set to true, generating self-signed certificate")
		cert, err := generateSelfSignedCert()
		if err != nil {
			return tlsConfig, err
		}
		return buildTLSConfig(cert, nil), nil
	}

	var certPool *x509.CertPool
	caFile, certFile, keyFile := Get().GatewayTLSCa(), Get().GatewayTLSCert(), Get().GatewayTLSKey()

	if caFile != "" {
		certPool = x509.NewCertPool()
		if !certPool.AppendCertsFromPEM([]byte(caFile)) {
			return tlsConfig, fmt.Errorf("failed creating cert pool for TLS_CA")
		}
		log.Infof("loaded TLS CA certificate from caFile=%s", caFile)
	}

	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return tlsConfig, err
		}
		log.Infof("loaded TLS certificate from certFile=%s and keyFile=%s", certFile, keyFile)
		return buildTLSConfig(cert, certPool), nil
	}

	return nil, nil
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
			CommonName:   "Hoop Gateway",
			Organization: []string{"Hoop Gateway"},
			Country:      []string{"US"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365), // valid for 1 year
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
func buildTLSConfig(cert tls.Certificate, certPool *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      certPool,
		MinVersion:   tls.VersionTLS12,
	}
}
