package tlstermination

import (
	"bytes"
	"crypto/tls"
	"github.com/hoophq/hoop/common/log"
	"github.com/pkg/errors"
	"net"
	"time"
)

var _ net.Listener = (*tlsTermination)(nil)

type tlsTermination struct {
	net.Listener
	tlsConfig       *tls.Config
	acceptPlainText bool
}

// NewTLSTermination wraps a net.Listener and terminates TLS using the provided certificate.
func NewTLSTermination(inner net.Listener, tlsConfig *tls.Config, acceptPlainText bool) net.Listener {
	return &tlsTermination{
		Listener:        inner,
		tlsConfig:       tlsConfig,
		acceptPlainText: acceptPlainText,
	}
}

func (t *tlsTermination) Accept() (net.Conn, error) {
	c, err := t.Listener.Accept()
	if err != nil {
		return nil, err
	}
	bconn := NewBufferedConnection(c)

	// Postgres has a special check before TLS
	isPostgresTLS, err := isPostgresTLSCheck(bconn)
	if isPostgresTLS {
		return t.toTLSConn(bconn), nil
	}
	log.Debugf("isPostgresTLS=%v, err=%v", isPostgresTLS, err)

	if !t.acceptPlainText { // force TLS
		return t.toTLSConn(bconn), nil
	}

	isTLS, err := isTLSConn(bconn)
	if err != nil {
		_ = c.Close()
		return nil, errors.Wrap(err, "failed to determine if connection is TLS")
	}

	log.Debugf("connection isTLS=%v", isTLS)

	if isTLS {
		return t.toTLSConn(bconn), nil
	}

	return bconn, nil
}

// toTLSConn converts a tcp connection to a tls connection
func (t *tlsTermination) toTLSConn(conn net.Conn) *tls.Conn {
	return tls.Server(conn, t.tlsConfig)
}

// isTLSConn checks if the connection handshake is currently sent by the connector
// Parses as specified by https://www.rfc-editor.org/rfc/rfc5246#section-6.2.1
// QUIC connections are not supported
func isTLSConn(conn BufferedConnection) (bool, error) {
	data, err := conn.Peek(3)
	if err != nil {
		return false, err
	}

	// See https://www.rfc-editor.org/rfc/rfc5246#section-6.2.1
	contentType := data[0]
	protoVersionMajor := data[1]
	protoVersionMinor := data[2]

	// Protocol versions are based on SSL version so
	// SSL 2.0 => 2 0
	// SSL 3.0 => 2 0
	// TLS 1.0 => 3 1
	// TLS 1.1 => 3 2
	// TLS 1.2 => 3 3
	// TLS 1.3 => 3 4
	// To avoid false positives we check for all those
	// Technically go TLS only supports 1.2 and up, but returning false
	// here would make it treat as plain text. So we allow all versions
	// and the go server rejects with a proper TLS message
	// go does have the consts for those versions, but only from TLS1.0 and up
	// that's why we use the raw numbers here

	contentIsHandshake := contentType == 22 // 22 == handshake
	majorSupported := protoVersionMajor == 3 || protoVersionMajor == 2
	minorSupported := protoVersionMinor <= 4

	return contentIsHandshake && majorSupported && minorSupported, nil
}

func isPostgresTLSCheck(conn BufferedConnection) (bool, error) {
	// See https://www.postgresql.org/docs/current/protocol-message-formats.html#PROTOCOL-MESSAGE-FORMATS-SSLREQUEST
	expected := []byte{0x00, 0x00, 0x00, 0x08, 0x04, 0xd2, 0x16, 0x2f}
	data, err := conn.Peek(8)
	if err != nil {
		return false, err
	}

	if bytes.Equal(data, expected) {
		log.Debugf("receive request to TLS on postgres")
		// Set deadlines because we don't want to wait forever here
		_ = conn.SetDeadline(time.Now().Add(peekWaitDuration))

		_, _ = conn.Read(expected)     // Just read the header
		_, _ = conn.Write([]byte("S")) // Send back S to accept TLS

		// Restore deadlines
		_ = conn.SetDeadline(time.Time{})

		return true, nil
	}

	return false, nil
}
