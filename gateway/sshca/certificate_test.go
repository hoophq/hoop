package sshca

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func keysEqual(a, b ssh.PublicKey) bool {
	return bytes.Equal(a.Marshal(), b.Marshal())
}

func newTestCASigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, caPrivKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(caPrivKey)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	return signer
}

func TestIssueCertificate(t *testing.T) {
	caSigner := newTestCASigner(t)

	certBytes, privKeyPEM, err := IssueCertificate(caSigner, []string{"testuser"}, time.Hour)
	if err != nil {
		t.Fatalf("IssueCertificate failed: %v", err)
	}
	if len(certBytes) == 0 {
		t.Fatal("certificate bytes are empty")
	}
	if len(privKeyPEM) == 0 {
		t.Fatal("private key PEM is empty")
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(certBytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	cert, ok := pubKey.(*ssh.Certificate)
	if !ok {
		t.Fatal("parsed key is not a certificate")
	}

	if cert.CertType != ssh.UserCert {
		t.Errorf("expected UserCert type, got %d", cert.CertType)
	}

	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != "testuser" {
		t.Errorf("expected principals [testuser], got %v", cert.ValidPrincipals)
	}

	if _, ok := cert.Permissions.Extensions["permit-pty"]; !ok {
		t.Error("missing permit-pty extension")
	}
	if _, ok := cert.Permissions.Extensions["permit-agent-forwarding"]; !ok {
		t.Error("missing permit-agent-forwarding extension")
	}
	if _, ok := cert.Permissions.Extensions["permit-port-forwarding"]; !ok {
		t.Error("missing permit-port-forwarding extension")
	}

	now := time.Now()
	validAfter := time.Unix(int64(cert.ValidAfter), 0)
	validBefore := time.Unix(int64(cert.ValidBefore), 0)

	if validAfter.After(now) {
		t.Errorf("ValidAfter (%v) is after now (%v)", validAfter, now)
	}
	if validBefore.Before(now) {
		t.Errorf("ValidBefore (%v) is before now (%v)", validBefore, now)
	}
	expectedBefore := now.Add(time.Hour)
	if validBefore.Before(expectedBefore.Add(-time.Minute)) || validBefore.After(expectedBefore.Add(time.Minute)) {
		t.Errorf("ValidBefore (%v) is not within expected range around %v", validBefore, expectedBefore)
	}
}

func TestIssueCertificateMultiplePrincipals(t *testing.T) {
	caSigner := newTestCASigner(t)

	principals := []string{"admin", "deploy", "ubuntu"}
	certBytes, _, err := IssueCertificate(caSigner, principals, time.Hour)
	if err != nil {
		t.Fatalf("IssueCertificate failed: %v", err)
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(certBytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	cert := pubKey.(*ssh.Certificate)
	if len(cert.ValidPrincipals) != 3 {
		t.Errorf("expected 3 principals, got %d", len(cert.ValidPrincipals))
	}
	for i, p := range principals {
		if cert.ValidPrincipals[i] != p {
			t.Errorf("principal[%d]: expected %q, got %q", i, p, cert.ValidPrincipals[i])
		}
	}
}

func TestIssueCertificateVerifiesWithCA(t *testing.T) {
	caSigner := newTestCASigner(t)

	certBytes, privKeyPEM, err := IssueCertificate(caSigner, []string{"testuser"}, time.Hour)
	if err != nil {
		t.Fatalf("IssueCertificate failed: %v", err)
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(certBytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}
	cert := pubKey.(*ssh.Certificate)

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return keysEqual(auth, caSigner.PublicKey())
		},
	}
	if err := checker.CheckCert("testuser", cert); err != nil {
		t.Fatalf("certificate verification failed: %v", err)
	}

	privKey, err := ssh.ParseRawPrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("failed to parse private key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	if _, err := ssh.NewCertSigner(cert, signer); err != nil {
		t.Fatalf("failed to create cert signer: %v", err)
	}
}

func TestIssueCertificateWrongPrincipalFails(t *testing.T) {
	caSigner := newTestCASigner(t)

	certBytes, _, err := IssueCertificate(caSigner, []string{"testuser"}, time.Hour)
	if err != nil {
		t.Fatalf("IssueCertificate failed: %v", err)
	}

	pubKey, _, _, _, _ := ssh.ParseAuthorizedKey(certBytes)
	cert := pubKey.(*ssh.Certificate)

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return keysEqual(auth, caSigner.PublicKey())
		},
	}
	if err := checker.CheckCert("wronguser", cert); err == nil {
		t.Fatal("expected certificate check to fail for wrong principal")
	}
}

func TestIssueCertificateWrongCAFails(t *testing.T) {
	caSigner := newTestCASigner(t)
	otherCASigner := newTestCASigner(t)

	certBytes, _, err := IssueCertificate(caSigner, []string{"testuser"}, time.Hour)
	if err != nil {
		t.Fatalf("IssueCertificate failed: %v", err)
	}

	pubKey, _, _, _, _ := ssh.ParseAuthorizedKey(certBytes)
	cert := pubKey.(*ssh.Certificate)

	// Verify the certificate's SignatureKey does NOT match the other CA
	if keysEqual(cert.SignatureKey, otherCASigner.PublicKey()) {
		t.Fatal("certificate signature key should not match other CA")
	}

	// Verify it DOES match the actual CA
	if !keysEqual(cert.SignatureKey, caSigner.PublicKey()) {
		t.Fatal("certificate signature key should match the signing CA")
	}
}

func TestIssueCertificateExpired(t *testing.T) {
	caSigner := newTestCASigner(t)

	certBytes, _, err := IssueCertificate(caSigner, []string{"testuser"}, -time.Minute)
	if err != nil {
		t.Fatalf("IssueCertificate failed: %v", err)
	}

	pubKey, _, _, _, _ := ssh.ParseAuthorizedKey(certBytes)
	cert := pubKey.(*ssh.Certificate)

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return keysEqual(auth, caSigner.PublicKey())
		},
	}
	if err := checker.CheckCert("testuser", cert); err == nil {
		t.Fatal("expected certificate check to fail for expired certificate")
	}
}
