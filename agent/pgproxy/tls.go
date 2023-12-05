package pgproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
)

var ErrSSLNotSupported = errors.New("SSL is not enabled on the postgres server")

type sslModeType string

const (
	sslModeDisable    sslModeType = "disable"
	sslModePrefer     sslModeType = "prefer"
	sslModeRequire    sslModeType = "require"
	sslModeVerifyFull sslModeType = "verify-full"
)

type tlsConfig struct {
	sslMode      sslModeType
	serverName   string
	rootCertPath string
}

// tlsClientHandshake performs the TLS handshake with the postgres server if it's required following
// the logic of https://www.postgresql.org/docs/current/libpq-ssl.html#LIBPQ-SSL-PROTECTION
func (p *proxy) tlsClientHandshake(conn net.Conn, serverSupportsTLS bool) (*tls.Conn, error) {
	var config tls.Config
	switch p.tlsConfig.sslMode {
	case sslModeDisable:
		if serverSupportsTLS {
			return nil, clientErrorF("server supports ssl but SSLMODE is set to disable")
		}
		return nil, nil
	case sslModePrefer:
		if !serverSupportsTLS {
			return nil, nil
		}
		config.InsecureSkipVerify = true
	case sslModeRequire:
		config.InsecureSkipVerify = true
	case sslModeVerifyFull:
		config.ServerName = p.tlsConfig.serverName
	default:
		return nil, clientErrorF("ssl mode [%v] not supported, supported values=%v", p.tlsConfig.sslMode,
			[]sslModeType{sslModeDisable, sslModePrefer, sslModeRequire, sslModeVerifyFull})
	}
	if err := p.sslCertificateAuthority(&config); err != nil {
		return nil, err
	}
	if !serverSupportsTLS {
		return nil, clientErrorF(ErrSSLNotSupported.Error())
	}
	tlsConn := tls.Client(conn, &config)
	if err := tlsConn.Handshake(); err != nil {
		if verr, ok := err.(tls.RecordHeaderError); ok {
			return nil, fmt.Errorf("tls handshake error=%v, message=%v, record-header=%X",
				verr.Msg, verr.Error(), verr.RecordHeader[:])
		}
		return nil, fmt.Errorf("handshake error: %v", err)
	}
	return tlsConn, nil
}

// sslCertificateAuthority adds the RootCA specified in the "sslrootcert" connection string.
func (p *proxy) sslCertificateAuthority(tlsConf *tls.Config) error {
	// In libpq, the root certificate is only loaded if the setting is not blank.
	//
	// https://github.com/postgres/postgres/blob/REL9_6_2/src/interfaces/libpq/fe-secure-openssl.c#L950-L951
	if p.tlsConfig.rootCertPath != "" {
		tlsConf.RootCAs = x509.NewCertPool()
		cert, err := os.ReadFile(p.tlsConfig.rootCertPath)
		if err != nil {
			return err
		}
		if !tlsConf.RootCAs.AppendCertsFromPEM(cert) {
			return fmt.Errorf("couldn't parse pem in sslrootcert")
		}
	}

	return nil
}
