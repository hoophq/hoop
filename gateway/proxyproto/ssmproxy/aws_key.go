package ssmproxy

import (
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Converts a UUID to an AWS Access Key format
func uuidToAccessKey(id string) (string, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return "", err
	}
	// Base32 encode the 16 UUID bytes
	encoded := base32.StdEncoding.EncodeToString(uid[:])

	// Remove padding
	cleaned := strings.TrimRight(encoded, "=")

	return "AKIA" + cleaned, nil
}

// Reverse: Access Key back to UUID
func accessKeyToUUID(accessKey string) (string, error) {
	// Remove AKIA prefix
	base32Str := accessKey[4:]

	// Add padding back (base32 requires length divisible by 8)
	padding := (8 - len(base32Str)%8) % 8
	base32Str += strings.Repeat("=", padding)

	// Decode base32
	decoded, err := base32.StdEncoding.DecodeString(base32Str)
	if err != nil {
		return "", fmt.Errorf("invalid access key format: %w", err)
	}

	// Convert 16 bytes back to UUID
	id, err := uuid.FromBytes(decoded)
	if err != nil {
		return "", fmt.Errorf("invalid UUID bytes: %w", err)
	}

	return id.String(), nil
}
