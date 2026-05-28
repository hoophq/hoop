package models

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"

	"gorm.io/gorm"
)

// credentialEncryptionKey caches the symmetric key used to encrypt the plaintext
// secret keys stored in private.connection_credentials. It is loaded once on
// first use from private.serverconfig.credential_encryption_key and reused for
// the rest of the process lifetime.
var (
	credentialEncryptionKeyMu sync.Mutex
	credentialEncryptionKey   []byte
)

// loadOrGenerateCredentialEncryptionKey returns the decoded 32-byte AES key.
// On first call it reads from private.serverconfig; if the row is missing it
// generates a fresh key, persists it and caches the decoded bytes.
func loadOrGenerateCredentialEncryptionKey() ([]byte, error) {
	credentialEncryptionKeyMu.Lock()
	defer credentialEncryptionKeyMu.Unlock()

	if credentialEncryptionKey != nil {
		return credentialEncryptionKey, nil
	}

	var encoded string
	err := DB.Raw(`
		SELECT credential_encryption_key
		FROM private.serverconfig
		WHERE credential_encryption_key IS NOT NULL
		LIMIT 1
	`).Scan(&encoded).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to read credential encryption key: %v", err)
	}

	if encoded != "" {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("failed to decode credential encryption key: %v", err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("invalid credential encryption key length: %d (want 32)", len(raw))
		}
		credentialEncryptionKey = raw
		return raw, nil
	}

	// Generate a fresh 32-byte AES-256 key and persist it. The upsert pattern
	// mirrors CreateServerSharedSigningKey so concurrent first-start calls
	// cannot insert duplicate values.
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, fmt.Errorf("failed to generate credential encryption key: %v", err)
	}
	newEncoded := base64.StdEncoding.EncodeToString(raw)

	res := DB.Exec(`
		UPDATE private.serverconfig
		SET credential_encryption_key = ?
		WHERE credential_encryption_key IS NULL
	`, newEncoded)
	if res.Error != nil {
		return nil, fmt.Errorf("failed to persist credential encryption key: %v", res.Error)
	}

	// If the serverconfig row doesn't exist yet, insert it.
	if res.RowsAffected == 0 {
		res = DB.Exec(`
			INSERT INTO private.serverconfig (credential_encryption_key)
			SELECT ?
			WHERE NOT EXISTS (
				SELECT 1 FROM private.serverconfig
				WHERE credential_encryption_key IS NOT NULL
			)
		`, newEncoded)
		if res.Error != nil {
			return nil, fmt.Errorf("failed to bootstrap credential encryption key: %v", res.Error)
		}
	}

	// Re-read to guarantee we use whatever value was actually persisted (a
	// competing gateway instance might have inserted a different key).
	err = DB.Raw(`
		SELECT credential_encryption_key
		FROM private.serverconfig
		WHERE credential_encryption_key IS NOT NULL
		LIMIT 1
	`).Scan(&encoded).Error
	if err != nil {
		return nil, fmt.Errorf("failed to re-read credential encryption key: %v", err)
	}
	final, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode stored credential encryption key: %v", err)
	}
	if len(final) != 32 {
		return nil, fmt.Errorf("persisted credential encryption key has invalid length: %d", len(final))
	}
	credentialEncryptionKey = final
	return final, nil
}

// EncryptCredentialSecretKey returns the AES-256-GCM ciphertext of the plaintext
// secret key. The nonce is prepended to the ciphertext.
func EncryptCredentialSecretKey(plaintext string) ([]byte, error) {
	key, err := loadOrGenerateCredentialEncryptionKey()
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %v", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %v", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// DecryptCredentialSecretKey reverses EncryptCredentialSecretKey.
func DecryptCredentialSecretKey(ciphertext []byte) (string, error) {
	key, err := loadOrGenerateCredentialEncryptionKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %v", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", fmt.Errorf("credential ciphertext shorter than nonce")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	payload := ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credential secret key: %v", err)
	}
	return string(plaintext), nil
}
