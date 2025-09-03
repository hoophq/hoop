package sshproxy

import (
	"crypto/ed25519"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

func EncodePrivateKeyToOpenSSH(privateKey ed25519.PrivateKey) ([]byte, error) {
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
