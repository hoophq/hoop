package grpckey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
)

// ImpersonateSecretKey is a key to impersonate users in the gateway securely by this package
var ImpersonateSecretKey string = generateSecureRandomKeyOrDie()

const (
	ImpersonateAuthKeyHeaderKey     = "impersonate-auth-key"
	ImpersonateUserSubjectHeaderKey = "impersonate-user-subject"

	// MachineIdentityFlagHeaderKey marks an impersonation request as a
	// machine-identity authentication. Set to "true" by protocol proxies
	// when the caller's credential resolves to a machine identity.
	MachineIdentityFlagHeaderKey = "is-machine-credential"
	// MachineIdentityOrgIDHeaderKey carries the org id of the machine
	// identity so the auth interceptor can load the MI without an extra
	// DB round-trip through a credential-session row.
	MachineIdentityOrgIDHeaderKey = "machine-identity-org-id"
)

func generateSecureRandomKeyOrDie() string {
	secretRandomBytes := make([]byte, 32)
	if _, err := rand.Read(secretRandomBytes); err != nil {
		log.Fatalf("failed generating entropy, err=%v", err)
	}
	secretKey := base64.RawURLEncoding.EncodeToString(secretRandomBytes)
	h := sha256.New()
	if _, err := h.Write([]byte(secretKey)); err != nil {
		log.Fatalf("failed hashing secret key, err=%v", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
