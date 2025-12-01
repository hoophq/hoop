package ssmproxy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

var (
	tokenSecret []byte // Any runtime random value so we can encrypt tokens
)

type ssmProxyToken struct {
	ConnID    string    `json:"conn_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func init() {
	if tokenSecret == nil {
		tokenSecret = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, tokenSecret); err != nil {
			panic(fmt.Sprintf("failed to generate token secret: %v", err))
		}
	}
}

func createTokenForConnection(connID string, duration time.Duration) (string, error) {
	payload := &ssmProxyToken{
		ConnID:    connID,
		ExpiresAt: time.Now().UTC().Add(duration),
	}

	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token payload: %v", err)
	}

	block, err := aes.NewCipher(tokenSecret)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %v", err)
	}

	// Create a new GCM mode cipher
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %v", err)
	}

	// Generate a random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %v", err)
	}

	// Encrypt and authenticate data
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decodeToken(token string) (string, time.Time, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to decode token: %v", err)
	}

	block, err := aes.NewCipher([]byte(tokenSecret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create cipher: %v", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to create GCM: %v", err)
	}

	if len(ciphertext) < gcm.NonceSize() {
		return "", time.Time{}, fmt.Errorf("invalid token")
	}

	nonce := ciphertext[:gcm.NonceSize()]
	ciphertext = ciphertext[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to decrypt token: %v", err)
	}

	var payload ssmProxyToken
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to unmarshal token payload: %v", err)
	}

	if time.Now().UTC().After(payload.ExpiresAt) {
		return "", time.Time{}, fmt.Errorf("token expired")
	}

	return payload.ConnID, payload.ExpiresAt, nil
}
