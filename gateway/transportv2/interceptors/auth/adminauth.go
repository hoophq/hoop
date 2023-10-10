package authinterceptor

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/memory"
)

const adminApiPrefixAuthKey string = "x-admin-auth-key:"

var adminApiAuthStore = memory.New()

// authRequest returns an random uuid that is used
// to authenticate request across the transport grpc layer.
// It allows reusing the gRPC protocol authenticating
// requests securely without opening breaches to external
// clients.
func AuthAdminApiRequest() (string, context.CancelFunc) {
	authKey := fmt.Sprintf("%s%s", adminApiPrefixAuthKey, uuid.NewString())
	adminApiAuthStore.Set(authKey, authKey)
	return authKey, func() { adminApiAuthStore.Del(authKey) }
}

// Authenticate validates if the key is in the memory store
// validating that the request is authenticated internally.
func authenticateAdminApi(authKey string) bool {
	key := adminApiAuthStore.Pop(authKey)
	return key != nil
}
