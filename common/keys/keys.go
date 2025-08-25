package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	defaultKeySize   uint16 = 32
	defaultPrefixKey string = "xagt"
)

// GenerateSecureRandomKey generates a secure random key of the specified size.
// If size is 0, it defaults to defaultKeySize (32 bytes).
// It defaults the prefix to defaultPrefixKey if not provided.
func GenerateSecureRandomKey(prefixKey string, size uint16) (secretKey, secretKeyHash string, err error) {
	if size <= 0 {
		size = defaultKeySize
	}
	secretRandomBytes := make([]byte, size)
	_, err = rand.Read(secretRandomBytes)
	if err != nil {
		return "", "", fmt.Errorf("failed generating entropy, err=%v", err)
	}
	secretKey = base64.RawURLEncoding.EncodeToString(secretRandomBytes)
	if prefixKey == "" {
		prefixKey = defaultPrefixKey
	}
	secretKey = prefixKey + "-" + secretKey
	secretKeyHash, err = Hash256Key(secretKey)
	if err != nil {
		return "", "", fmt.Errorf("failed generating secret hash, err=%v", err)
	}
	return secretKey, secretKeyHash, err
}

// NewJwtToken generates a new JWT token signed with HMAC using the provided secret key.
func NewJwtToken(privKey ed25519.PrivateKey, subject, email string, tokenDuration time.Duration) (string, error) {
	now := time.Now().UTC()
	var claims = struct {
		Email string `json:"email"`
		jwt.RegisteredClaims
	}{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenDuration)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	return token.SignedString(privKey)
}

// VerifyAccessToken verifies the access token and returns the subject if valid.
// It expects the token to be signed with HMAC using the provided secret key.
func VerifyAccessToken(tokenString string, pubKey ed25519.PublicKey) (subject string, err error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return pubKey, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to parse access token: %v", err)
	}
	if !token.Valid {
		return "", fmt.Errorf("access token is invalid")
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		subject, ok = claims["sub"].(string)
		if !ok || subject == "" {
			return "", fmt.Errorf("'sub' not found or has an empty value")
		}
		return subject, nil
	}
	return "", fmt.Errorf("failed type casting token.Claims (%T) to jwt.MapClaims", token.Claims)
}

func GenerateEd25519KeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	// Generate a new ED25519 key pair
	return ed25519.GenerateKey(rand.Reader)
}

// Base64DecodeEd25519PrivateKey decodes a base64 encoded ED25519 private key.
// It returns an error if the decoding fails or if the key size is invalid.
func Base64DecodeEd25519PrivateKey(encodedKey string) (ed25519.PrivateKey, error) {
	privKeyBytes, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode private key: %v", err)
	}
	if len(privKeyBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: expected %d bytes, got %d bytes", ed25519.PrivateKeySize, len(privKeyBytes))
	}
	return ed25519.PrivateKey(privKeyBytes), nil
}

// Base64DecodeEd25519PublicKey decodes a base64 encoded ED25519 public key.
// It returns an error if the decoding fails or if the key size is invalid.
func Base64DecodeEd25519PublicKey(encodedKey string) (ed25519.PublicKey, error) {
	pubKeyBytes, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode public key: %v", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: expected %d bytes, got %d bytes", ed25519.PublicKeySize, len(pubKeyBytes))
	}
	return ed25519.PublicKey(pubKeyBytes), nil
}

func Hash256Key(secretKey string) (secret256Hash string, err error) {
	h := sha256.New()
	if _, err := h.Write([]byte(secretKey)); err != nil {
		return "", fmt.Errorf("failed hashing secret key, err=%v", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
