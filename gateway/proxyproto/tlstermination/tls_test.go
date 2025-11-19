package tlstermination

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

func generateSelfSignedCert(t *testing.T) *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("Failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Test Co"},
		},
		NotBefore:             time.Now().UTC(),
		NotAfter:              time.Now().UTC().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("Failed to create certificate: %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  key,
		}},
		MinVersion: tls.VersionTLS12,
	}
}

func TestAcceptTLSConnection(t *testing.T) {
	// Create a TCP listener
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create TCP listener: %v", err)
	}
	defer tcpListener.Close()

	cert := generateSelfSignedCert(t)
	tlsListener := NewTLSTermination(tcpListener, cert, false)
	defer tlsListener.Close()

	done := make(chan struct{})
	closeConn := make(chan struct{})

	go func() {
		defer close(done)
		conn, err := tlsListener.Accept()
		if err != nil {
			t.Errorf("Failed to accept connection: %v", err)
			return
		}
		defer conn.Close()

		if _, ok := conn.(TLSConnectionMeta); !ok {
			t.Errorf("Connection was not upgraded to TLS")
		}
		_, _ = conn.Write([]byte("DEADBEEF"))
		<-closeConn
	}()

	// Connect as a TLS client
	clientConn, err := tls.Dial("tcp", tcpListener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("TLS client failed to connect: %v", err)
	}

	b := make([]byte, 8)
	n, err := clientConn.Read(b)
	if err != nil {
		t.Fatalf("Failed to read from TLS connection: %v", err)
	}
	if n != 8 || string(b) != "DEADBEEF" {
		t.Fatalf("Unexpected data from TLS connection: %s", string(b[:n]))
	}
	closeConn <- struct{}{}
	_ = clientConn.Close()

	<-done
}

func TestAcceptNonTLSConnectionWhenAllowed(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create TCP listener: %v", err)
	}
	defer tcpListener.Close()

	cert := generateSelfSignedCert(t)
	tlsListener := NewTLSTermination(tcpListener, cert, true)
	defer tlsListener.Close()

	done := make(chan struct{}, 1)
	closeConn := make(chan struct{}, 1)
	go func() {
		defer close(done)
		conn, err := tlsListener.Accept()
		if err != nil {
			t.Errorf("Failed to accept connection: %v", err)
			return
		}

		defer conn.Close()

		if _, ok := conn.(*tls.Conn); ok {
			t.Errorf("Non-TLS connection was incorrectly upgraded to TLS")
		}
		_, _ = conn.Write([]byte("DEADBEEF"))
		<-closeConn
	}()

	// Connect as a regular TCP client
	clientConn, err := net.Dial("tcp", tcpListener.Addr().String())
	if err != nil {
		t.Fatalf("TCP client failed to connect: %v", err)
	}
	_, err = clientConn.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	if err != nil {
		t.Fatalf("Failed to write to connection: %v", err)
	}

	b := make([]byte, 8)
	n, err := clientConn.Read(b)
	if err != nil {
		t.Fatalf("Failed to read from TLS connection: %v", err)
	}
	if n != 8 || string(b) != "DEADBEEF" {
		t.Fatalf("Unexpected data from TLS connection: %s", string(b[:n]))
	}

	closeConn <- struct{}{}
	_ = clientConn.Close()

	<-done
}

func TestRejectNonTLSConnectionWhenNotAllowed(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create TCP listener: %v", err)
	}
	defer tcpListener.Close()

	cert := generateSelfSignedCert(t)
	tlsListener := NewTLSTermination(tcpListener, cert, false)
	defer tlsListener.Close()

	// Connect as a regular TCP client
	clientConn, err := net.Dial("tcp", tcpListener.Addr().String())
	if err != nil {
		t.Fatalf("TCP client failed to connect: %v", err)
	}

	// Write some non-TLS data
	_, err = clientConn.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	if err != nil {
		t.Fatalf("Failed to write to connection: %v", err)
	}

	clientConn.Close()
}

func TestAcceptTLSConnectionWhenNonTLSAllowed(t *testing.T) {
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create TCP listener: %v", err)
	}
	defer tcpListener.Close()

	cert := generateSelfSignedCert(t)
	tlsListener := NewTLSTermination(tcpListener, cert, true)
	defer tlsListener.Close()

	done := make(chan struct{})
	closeConn := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := tlsListener.Accept()
		if err != nil {
			t.Errorf("Failed to accept connection: %v", err)
			return
		}
		defer conn.Close()

		if _, ok := conn.(TLSConnectionMeta); !ok {
			t.Errorf("TLS connection was not properly identified")
		}
		_, _ = conn.Write([]byte("DEADBEEF"))
		<-closeConn
	}()

	// Connect as a TLS client
	clientConn, err := tls.Dial("tcp", tcpListener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("TLS client failed to connect: %v", err)
	}
	closeConn <- struct{}{}
	clientConn.Close()

	<-done
}
