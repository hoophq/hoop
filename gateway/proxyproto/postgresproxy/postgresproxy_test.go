package postgresproxy

import (
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pgtypes "github.com/hoophq/hoop/common/pgtypes"
)

func TestConnectionSetupErrorStaysInsideTLS(t *testing.T) {
	tlsServer := httptest.NewTLSServer(nil)
	tlsConfig := tlsServer.TLS.Clone()
	tlsServer.Close()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	deadline := time.Now().Add(5 * time.Second)
	_ = serverConn.SetDeadline(deadline)
	_ = clientConn.SetDeadline(deadline)

	done := make(chan error, 1)
	go func() {
		_, err := newPostgresConnection("session", "connection", serverConn, tlsConfig)
		if err != nil {
			writeConnectionError(serverConn, err)
		}
		_ = serverConn.Close()
		done <- err
	}()

	if _, err := clientConn.Write([]byte{0, 0, 0, 8, 4, 210, 22, 47}); err != nil {
		t.Fatal(err)
	}
	sslResponse := make([]byte, 1)
	if _, err := io.ReadFull(clientConn, sslResponse); err != nil {
		t.Fatal(err)
	}
	if string(sslResponse) != "S" {
		t.Fatalf("SSL response = %q, want S", sslResponse)
	}

	tlsClient := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec -- test certificate
	if err := tlsClient.Handshake(); err != nil {
		t.Fatal(err)
	}
	parameters := []byte("database\x00postgres\x00\x00")
	startup := make([]byte, len(parameters)+8)
	binary.BigEndian.PutUint32(startup, uint32(len(startup)))
	binary.BigEndian.PutUint32(startup[4:], 196608)
	copy(startup[8:], parameters)
	if _, err := tlsClient.Write(startup); err != nil {
		t.Fatal(err)
	}

	responseType := make([]byte, 1)
	if _, err := io.ReadFull(tlsClient, responseType); err != nil {
		t.Fatalf("read TLS-framed PostgreSQL error: %v", err)
	}
	if responseType[0] != pgtypes.ServerErrorResponse.Byte() {
		t.Fatalf("response type = %q, want %q", responseType[0], pgtypes.ServerErrorResponse.Byte())
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "failed obtaining secret key") {
		t.Fatalf("setup error = %v, want missing secret key", err)
	}
}
