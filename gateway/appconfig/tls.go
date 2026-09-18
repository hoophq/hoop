package appconfig

import (
	"crypto/tls"
	"sync"

	"github.com/hoophq/hoop/common/log"
)

// loadTlsConfigOnce ensures the TLS config is built only once.
var loadTlsConfigOnce = sync.OnceValues(loadTLSConfig)

// GetTLSConfig returns the listener's TLS configuration, or nil to serve
// plaintext. Nil is a valid result and not an error: TLS is on when a
// certificate pair is configured and off when it is not. Nothing is generated.
func (c Config) GetTLSConfig() (*tls.Config, error) {
	return loadTlsConfigOnce()
}

// GatewayAllowPlaintext indicates if plaintext (non-TLS) connections are allowed.
func (c Config) GatewayAllowPlaintext() bool {
	return c.gatewayAllowPlainText
}

// loadTLSConfig builds the listener's TLS configuration from TLS_CERT and
// TLS_KEY, or returns nil to serve plaintext when they are not both set.
//
// The pair is the same condition appconfig.Load uses for gatewayUseTLS, which
// tells the in-process gRPC clients how to dial. Change one and change both.
func loadTLSConfig() (tlsConfig *tls.Config, err error) {
	certData, keyData := Get().GatewayTLSCert(), Get().GatewayTLSKey()
	if certData != "" && keyData != "" {
		cert, err := tls.X509KeyPair([]byte(certData), []byte(keyData))
		if err != nil {
			return nil, err
		}
		log.Info("loaded TLS certificate from config")
		return buildTLSConfig(cert), nil
	}
	return nil, nil
}

// buildTLSConfig constructs a tls.Config from the provided certificate and certificate pool.
func buildTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
}
