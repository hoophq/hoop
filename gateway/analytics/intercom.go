package analytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// https://www.intercom.com/help/en/articles/183-set-up-identity-verification-for-web-and-mobile
func GenerateIntercomHmacDigest(email string) (string, error) {
	key := []byte(intercomHmacKey)
	message := []byte(email)
	hash := hmac.New(sha256.New, key)
	if _, err := hash.Write(message); err != nil {
		return "", fmt.Errorf("failed generating hmac signature for %v, hmac-key-length=%v, reason=%v",
			email, len(intercomHmacKey), err)
	}
	sha := hash.Sum(nil)
	return hex.EncodeToString(sha), nil
}
