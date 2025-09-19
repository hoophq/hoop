package sshproxy

import (
	"bytes"
	"crypto/ed25519"
	"encoding/pem"
	"testing"

	"github.com/hoophq/hoop/common/keys"
)

func TestEd25519KeyEncodeDecode(t *testing.T) {
	// Step 1: Generate a new Ed25519 key pair
	publicKey, privateKey, err := keys.GenerateEd25519KeyPair()
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
	decodedKey, err := decodeOpenSSHPrivateKey(encodedKey)
	if err != nil {
		t.Fatalf("Failed to decode OpenSSH private key: %v", err)
	}

	// Step 4: Verify the decoded key matches the original
	if !bytes.Equal(privateKey, decodedKey) {
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
	decodedSignature := ed25519.Sign(decodedKey, message)

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
