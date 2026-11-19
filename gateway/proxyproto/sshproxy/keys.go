package sshproxy

import (
	"crypto/ed25519"
	"encoding/pem"

	"golang.org/x/crypto/ssh"
)

func EncodePrivateKeyToOpenSSH(privateKey ed25519.PrivateKey) ([]byte, error) {
	block, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return nil, err
	}

	return pem.EncodeToMemory(block), nil
}

func decodeOpenSSHPrivateKey(pemBytes []byte) (any, error) {
	return ssh.ParseRawPrivateKey(pemBytes)
}
