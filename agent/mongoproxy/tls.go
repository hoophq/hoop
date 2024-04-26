package mongoproxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"strings"
)

type tlsProxyConfig struct {
	tlsInsecure           bool
	serverName            string
	tlsCAFile             string
	tlsCertificateKeyFile string
}

// tlsClientHandshake loads all certificates and performs the tls
// handshake with the server. If the proxy config is not set
// returns a nil connection
func (p *proxy) tlsClientHandshake() (*tls.Conn, error) {
	if p.tlsProxyConfig == nil {
		return nil, nil
	}

	conn, ok := p.serverRW.(net.Conn)
	if !ok {
		return nil, fmt.Errorf("server is not a net.Conn type")
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: p.tlsProxyConfig.tlsInsecure,
		ServerName:         p.tlsProxyConfig.serverName,
		RootCAs:            nil,
		Certificates:       nil,
	}
	if err := p.loadCertificates(tlsConfig); err != nil {
		return nil, err
	}

	tlsConn := tls.Client(conn, tlsConfig)
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
func (p *proxy) loadCertificates(tlsConf *tls.Config) error {
	if p.tlsProxyConfig == nil || p.tlsProxyConfig.tlsCAFile == "" {
		return nil
	}
	// set root CA certificate
	tlsConf.RootCAs = x509.NewCertPool()
	cert, err := os.ReadFile(p.tlsProxyConfig.tlsCAFile)
	if err != nil {
		return err
	}
	if !tlsConf.RootCAs.AppendCertsFromPEM(cert) {
		return fmt.Errorf("couldn't parse pem in tlsCaFile")
	}

	if p.tlsProxyConfig.tlsCertificateKeyFile == "" {
		return nil
	}
	// set client certificates
	cert, err = os.ReadFile(p.tlsProxyConfig.tlsCertificateKeyFile)
	if err != nil {
		return err
	}
	return addClientCertFromBytes(tlsConf, cert)
}

// addClientCertFromBytes adds client certificates to the configuration given a path to the
// containing file and returns the subject name in the first certificate.
func addClientCertFromBytes(cfg *tls.Config, data []byte) error {
	var currentBlock *pem.Block
	var certDecodedBlock []byte
	var certBlocks, keyBlocks [][]byte

	remaining := data
	start := 0
	for {
		currentBlock, remaining = pem.Decode(remaining)
		if currentBlock == nil {
			break
		}

		if currentBlock.Type == "CERTIFICATE" {
			certBlock := data[start : len(data)-len(remaining)]
			certBlocks = append(certBlocks, certBlock)
			// Assign the certDecodedBlock when it is never set,
			// so only the first certificate is honored in a file with multiple certs.
			if certDecodedBlock == nil {
				certDecodedBlock = currentBlock.Bytes
			}
			start += len(certBlock)
		} else if strings.HasSuffix(currentBlock.Type, "PRIVATE KEY") {
			keyBlock := data[start : len(data)-len(remaining)]
			keyBlocks = append(keyBlocks, keyBlock)
			start += len(keyBlock)
		}
	}
	if len(certBlocks) == 0 {
		return fmt.Errorf("failed to find CERTIFICATE")
	}
	if len(keyBlocks) == 0 {
		return fmt.Errorf("failed to find PRIVATE KEY")
	}

	cert, err := tls.X509KeyPair(bytes.Join(certBlocks, []byte("\n")), bytes.Join(keyBlocks, []byte("\n")))
	if err != nil {
		return err
	}

	cfg.Certificates = append(cfg.Certificates, cert)
	return nil
}
