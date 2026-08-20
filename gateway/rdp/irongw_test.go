package rdp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestHandshakeAgentTLSClearsDeadline(t *testing.T) {
	serverConfig := newAgentHandshakeTestTLSConfig(t)
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	deadlineDelay := 500 * time.Millisecond
	if err := clientConn.SetDeadline(time.Now().Add(deadlineDelay)); err != nil {
		t.Fatalf("set handshake deadline: %v", err)
	}

	serverDone := make(chan error, 1)
	go func() {
		tlsServer := tls.Server(serverConn, serverConfig)
		if err := tlsServer.Handshake(); err != nil {
			serverDone <- err
			return
		}
		time.Sleep(deadlineDelay + 100*time.Millisecond)
		_, err := tlsServer.Write([]byte("ok"))
		serverDone <- err
	}()

	tlsClient, err := handshakeAgentTLS(clientConn)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer tlsClient.Close()

	got := make([]byte, 2)
	if _, err := io.ReadFull(tlsClient, got); err != nil {
		t.Fatalf("read after handshake deadline elapsed: %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("read %q, want %q", got, "ok")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func newAgentHandshakeTestTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Minute),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der},
			PrivateKey:  key,
		}},
	}
}
