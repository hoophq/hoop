//go:build integration

package testutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"
)

// GeneratedSSHKey is a fresh RSA SSH keypair plus the helpers tests
// need to wire it into both the openssh-server container and the
// agent's libhoop SSH client.
type GeneratedSSHKey struct {
	// AuthorizedKey is the public key in OpenSSH authorized_keys
	// format ("ssh-rsa AAAA..."). Pass this to StartSSHWithPublicKey.
	AuthorizedKey string

	// PrivateKeyPEM is the private key in PEM format. Pass this as
	// the AUTHORIZED_SERVER_KEYS env var in OpenSSHSessionWithKey so
	// libhoop authenticates against the upstream sshd using this key.
	PrivateKeyPEM string

	// Signer is the corresponding ssh.Signer for use in
	// ssh.PublicKeys() on the test's local SSH client config. The
	// bridge accepts any auth, so this is for protocol completeness
	// rather than real authentication.
	Signer ssh.Signer
}

// GenerateSSHKey returns a fresh RSA-2048 SSH keypair for tests.
// Generating each time keeps tests independent and avoids any need
// to manage fixture key files in the repo.
func GenerateSSHKey(t *testing.T) *GeneratedSSHKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	pubKeyBytes := ssh.MarshalAuthorizedKey(signer.PublicKey())
	// MarshalAuthorizedKey ends with a newline; the container env
	// expects a trimmed single-line value.
	for len(pubKeyBytes) > 0 && pubKeyBytes[len(pubKeyBytes)-1] == '\n' {
		pubKeyBytes = pubKeyBytes[:len(pubKeyBytes)-1]
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})

	return &GeneratedSSHKey{
		AuthorizedKey: string(pubKeyBytes),
		PrivateKeyPEM: string(privPEM),
		Signer:        signer,
	}
}

// BuildSSHEnvVarsWithKey constructs SSH env vars using public-key
// authentication. The agent forwards AUTHORIZED_SERVER_KEYS to libhoop,
// which decodes the PEM and uses it as the ssh.PublicKeys auth method.
func BuildSSHEnvVarsWithKey(host, port, user, privateKeyPEM string) map[string]any {
	return map[string]any{
		"envvar:HOST":                    b64(host),
		"envvar:USER":                    b64(user),
		"envvar:PORT":                    b64(port),
		"envvar:AUTHORIZED_SERVER_KEYS":  b64(privateKeyPEM),
	}
}
