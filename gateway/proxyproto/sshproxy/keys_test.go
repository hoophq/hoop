package sshproxy

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestEd25519KeyEncodeDecode(t *testing.T) {
	// Step 1: Generate a new Ed25519 key pair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate Ed25519 key pair: %v", err)
	}

	// Log the original key info
	t.Logf("Generated public key length: %d bytes", len(publicKey))
	t.Logf("Generated private key length: %d bytes", len(privateKey))

	// Step 2: Encode the private key to OpenSSH format
	encodedKey, err := EncodePrivateKeyToOpenSSH(privateKey)
	if err != nil {
		t.Fatalf("Failed to encode private key to OpenSSH format: %v", err)
	}

	t.Logf("Encoded OpenSSH key length: %d bytes", len(encodedKey))

	// Verify it's a valid PEM block
	block, _ := pem.Decode(encodedKey)
	if block == nil {
		t.Fatal("Failed to decode PEM block from encoded key")
	}
	t.Logf("PEM block type: %s", block.Type)

	// Step 3: Decode the OpenSSH formatted key back to Ed25519 private key
	decoded, err := decodeOpenSSHPrivateKey(encodedKey)
	if err != nil {
		t.Fatalf("Failed to decode OpenSSH private key: %v", err)
	}

	decodedKey, ok := any(decoded).(*ed25519.PrivateKey)
	if !ok {
		t.Fatalf("Expected Ed25519 private key, got %T", decoded)
	}

	// Step 4: Verify the decoded key matches the original
	if !bytes.Equal(privateKey, *decodedKey) {
		t.Fatal("Decoded private key does not match the original")
	}

	// Step 5: Verify the decoded key can derive the same public key
	decodedPublicKey := decodedKey.Public().(ed25519.PublicKey)
	if !bytes.Equal(publicKey, decodedPublicKey) {
		t.Fatal("Public key derived from decoded private key does not match original")
	}

	// Step 6: Test signing and verification to ensure the key is functional
	message := []byte("Test message for signing")

	// Sign with original key
	originalSignature := ed25519.Sign(privateKey, message)

	// Sign with decoded key
	decodedSignature := ed25519.Sign(*decodedKey, message)

	// Verify both signatures with the public key
	if !ed25519.Verify(publicKey, message, originalSignature) {
		t.Fatal("Failed to verify signature from original key")
	}

	if !ed25519.Verify(publicKey, message, decodedSignature) {
		t.Fatal("Failed to verify signature from decoded key")
	}

	// Verify signatures are identical (they should be for Ed25519)
	if !bytes.Equal(originalSignature, decodedSignature) {
		t.Fatal("Signatures from original and decoded keys do not match")
	}

	t.Log("✓ Key generation successful")
	t.Log("✓ Encoding to OpenSSH format successful")
	t.Log("✓ Decoding from OpenSSH format successful")
	t.Log("✓ Keys match after round-trip")
	t.Log("✓ Signature verification successful")
}

func TestDecodeOpenSSHPrivateKeyAlgorithms(t *testing.T) {
	tests := []struct {
		name     string
		generate func() (any, error)
	}{
		{"Ed25519", func() (any, error) { _, key, err := ed25519.GenerateKey(rand.Reader); return key, err }},
		{"ECDSA-P256", func() (any, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }},
		{"ECDSA-P384", func() (any, error) { return ecdsa.GenerateKey(elliptic.P384(), rand.Reader) }},
		{"RSA", func() (any, error) { return rsa.GenerateKey(rand.Reader, 3072) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := tt.generate()
			if err != nil {
				t.Fatal(err)
			}
			block, err := ssh.MarshalPrivateKey(key, "")
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeOpenSSHPrivateKey(pem.EncodeToMemory(block))
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.NewSignerFromKey(decoded)
			if err != nil {
				t.Fatal(err)
			}
			original, err := ssh.NewSignerFromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original.PublicKey().Marshal(), signer.PublicKey().Marshal()) {
				t.Fatal("Decoded public key does not match original")
			}
			message := []byte("SSH host key regression")
			signature, err := signer.Sign(rand.Reader, message)
			if err != nil {
				t.Fatal(err)
			}
			if err := original.PublicKey().Verify(message, signature); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDecodeOpenSSHPrivateKeyInvalid(t *testing.T) {
	if _, err := decodeOpenSSHPrivateKey([]byte("invalid private key")); err == nil {
		t.Fatal("Expected invalid private key to be rejected")
	}
}
