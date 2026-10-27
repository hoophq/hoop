package services

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

// SidecarCredentialKeyPrefix opens every key a user mints for a sidecar lane.
// It is not "hsc_": that is a sidecar's own token, and a lane key pasted in
// its place must read as the wrong thing.
const SidecarCredentialKeyPrefix = "hlc_"

// Bounds of a sidecar lane credential's life. The default applies when the
// request names none; the maximum caps what it names.
const (
	SidecarCredentialDefaultTTL = 24 * time.Hour
	SidecarCredentialMaxTTL     = 30 * 24 * time.Hour
)

// GenerateSidecarCredentialKey returns the key a user presents to a sidecar
// lane as its password.
func GenerateSidecarCredentialKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	return SidecarCredentialKeyPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// scramIterations is the PBKDF2 round count of a minted verifier, the
// minimum RFC 7677 names.
const scramIterations = 4096

// NewSCRAMMaterial computes the RFC 7677 server-side verifier of a key, so a
// sidecar can run SCRAM-SHA-256 against a client without the key.
func NewSCRAMMaterial(key string) (*daemon.SCRAMMaterial, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}
	return scramMaterial(key, salt, scramIterations)
}

func scramMaterial(key string, salt []byte, iterations int) (*daemon.SCRAMMaterial, error) {
	salted, err := pbkdf2.Key(sha256.New, key, salt, iterations, sha256.Size)
	if err != nil {
		return nil, err
	}
	clientKey := scramHMAC(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := scramHMAC(salted, "Server Key")
	return &daemon.SCRAMMaterial{
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Iterations: iterations,
		StoredKey:  base64.StdEncoding.EncodeToString(storedKey[:]),
		ServerKey:  base64.StdEncoding.EncodeToString(serverKey),
	}, nil
}

func scramHMAC(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// MintSidecarCredential stores a new credential for one lane, under the
// minting user's subject, and returns the row with the raw key, the only time
// the key exists in the clear.
func MintSidecarCredential(db *gorm.DB, sc *models.Sidecar, lane, userSubject string, ttl time.Duration) (*models.ConnectionCredentials, string, error) {
	key, err := GenerateSidecarCredentialKey()
	if err != nil {
		return nil, "", err
	}
	scram, err := NewSCRAMMaterial(key)
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	cred := &models.ConnectionCredentials{
		ID:             uuid.NewString(),
		OrgID:          sc.OrgID,
		UserSubject:    userSubject,
		ConnectionName: models.SidecarCredentialConnectionName(sc.Name, lane),
		ConnectionType: models.SidecarCredentialType,
		SecretKeyHash:  models.HashAPIKey(key),
		CreatedAt:      now,
		ExpireAt:       now.Add(ttl),
		ScramSHA256:    scram,
	}
	if err := models.InsertConnectionCredentials(db, cred); err != nil {
		return nil, "", fmt.Errorf("failed storing the credential: %w", err)
	}
	return cred, key, nil
}

// sidecarCredentialSet is the served form of a sidecar's valid credentials,
// or nil when it has none, so the key stays off a document that has nothing
// to say.
func sidecarCredentialSet(db *gorm.DB, sc *models.Sidecar) ([]daemon.CredentialSetEntry, error) {
	rows, err := models.ListValidSidecarCredentials(db, sc.OrgID, sc)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	set := make([]daemon.CredentialSetEntry, 0, len(rows))
	for _, r := range rows {
		set = append(set, daemon.CredentialSetEntry{
			Hash:      r.SecretKeyHash,
			Subject:   r.UserSubject,
			Email:     r.UserEmail,
			Groups:    []string(r.UserGroups),
			Lane:      r.Lane(),
			ExpiresAt: r.ExpireAt,
			SCRAM:     r.ScramSHA256,
		})
	}
	return set, nil
}
