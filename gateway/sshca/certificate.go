package sshca

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/gateway/models"
	"golang.org/x/crypto/ssh"
)

// LoadCASignerFromConfig loads the SSH CA private key from the server
// configuration and returns an ssh.Signer for signing certificates.
func LoadCASignerFromConfig() (ssh.Signer, error) {
	config, err := models.GetServerMiscConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server config: %v", err)
	}
	if config.SSHServerConfig == nil || config.SSHServerConfig.CAKey == "" {
		return nil, fmt.Errorf("SSH CA key is not configured")
	}

	privKey, err := decodeCAPrivateKey(config.SSHServerConfig.CAKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode CA key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create signer from CA key: %v", err)
	}
	return signer, nil
}

// IssueCertificate generates an ephemeral Ed25519 key pair, signs it with the
// CA to produce a short-lived SSH user certificate, and returns both the
// certificate (in authorized_key format) and the ephemeral private key (in
// OpenSSH PEM format). Both are returned as raw bytes (not base64-encoded).
func IssueCertificate(caSigner ssh.Signer, principals []string, validity time.Duration) (certBytes []byte, privKeyPEM []byte, err error) {
	_, ephemeralPrivKey, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate ephemeral key pair: %v", err)
	}

	ephemeralPubKey, err := ssh.NewPublicKey(ephemeralPrivKey.Public())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create SSH public key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate certificate serial: %v", err)
	}

	now := time.Now()
	cert := &ssh.Certificate{
		Key:             ephemeralPubKey,
		CertType:        ssh.UserCert,
		KeyId:           fmt.Sprintf("hoop-%s", uuid.NewString()),
		ValidPrincipals: principals,
		ValidAfter:      uint64(now.Add(-30 * time.Second).Unix()),
		ValidBefore:     uint64(now.Add(validity).Unix()),
		Serial:          serial.Uint64(),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{
				"permit-pty":              "",
				"permit-agent-forwarding": "",
				"permit-port-forwarding":  "",
			},
		},
	}

	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		return nil, nil, fmt.Errorf("failed to sign certificate: %v", err)
	}

	certBytes = ssh.MarshalAuthorizedKey(cert)

	privKeyPEM, err = encodePrivateKeyToOpenSSH(ephemeralPrivKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encode ephemeral private key: %v", err)
	}

	return certBytes, privKeyPEM, nil
}

// CAPublicKeyFromConfig loads the CA private key from config and returns
// the corresponding public key in authorized_keys format.
func CAPublicKeyFromConfig() ([]byte, error) {
	config, err := models.GetServerMiscConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server config: %v", err)
	}
	if config.SSHServerConfig == nil || config.SSHServerConfig.CAKey == "" {
		return nil, fmt.Errorf("SSH CA key is not configured")
	}

	privKey, err := decodeCAPrivateKey(config.SSHServerConfig.CAKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode CA key: %v", err)
	}

	sshPubKey, err := ssh.NewPublicKey(privKey.Public())
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH public key: %v", err)
	}

	return ssh.MarshalAuthorizedKey(sshPubKey), nil
}

func decodeCAPrivateKey(caKeyB64 string) (ed25519.PrivateKey, error) {
	pemBytes, err := base64.StdEncoding.DecodeString(caKeyB64)
	if err != nil {
		return nil, fmt.Errorf("failed to base64 decode CA key: %v", err)
	}
	return decodeOpenSSHPrivateKey(pemBytes)
}

func encodePrivateKeyToOpenSSH(privateKey ed25519.PrivateKey) ([]byte, error) {
	block, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(block), nil
}

func decodeOpenSSHPrivateKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	privateKey, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		return nil, err
	}
	ed25519Key, ok := privateKey.(*ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not Ed25519, found=%T", privateKey)
	}
	return *ed25519Key, nil
}
