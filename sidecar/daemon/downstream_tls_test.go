package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeKeyPair writes a self-signed server keypair and returns the paths,
// because Validate loads downstream_tls at startup.
func writeKeyPair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "relay.test"},
		DNSNames:     []string{"relay.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "relay.crt")
	keyFile = filepath.Join(dir, "relay.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// An http lane terminates its client's TLS (h2 and http/1.1 by ALPN), so
// downstream_tls loads there; a mysql lane still refuses it, because the
// relay never offers a certificate on that protocol.
func TestDownstreamTLSAcceptedOnHTTPRefusedOnMySQL(t *testing.T) {
	cert, key := writeKeyPair(t)
	tlsBlock := `"downstream_tls":{"cert_file":"` + cert + `","key_file":"` + key + `"}`

	p := writeConfig(t, `{"listeners":[{"name":"kube","protocol":"http","listen":":1","upstream":"h:1",`+tlsBlock+`}]}`)
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("downstream_tls refused on an http lane: %v", err)
	}

	p = writeConfig(t, `{"listeners":[{"name":"mydb","protocol":"mysql","listen":":1","upstream":"h:1",`+tlsBlock+`}]}`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "downstream_tls is only supported on") {
		t.Fatalf("mysql lane with downstream_tls: err = %v, want a refusal", err)
	}
}
