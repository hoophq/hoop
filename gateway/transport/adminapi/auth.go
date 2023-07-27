package adminapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/memory"
)

const PrefixAuthKey string = "x-admin-auth-key:"

var authStore = memory.New()

// authRequest returns an random uuid that is used
// to authenticate request across the transport grpc layer.
// It allows reusing the gRPC protocol authenticating
// requests securely without opening breaches to external
// clients.
func authRequest() (string, context.CancelFunc) {
	authKey := fmt.Sprintf("%s%s", PrefixAuthKey, uuid.NewString())
	authStore.Set(authKey, authKey)
	return authKey, func() { authStore.Del(authKey) }
}

// Authenticate validates if the key is in the memory store
// validating that the request is authenticated internally.
func Authenticate(authKey string) bool {
	key := authStore.Pop(authKey)
	return key != nil
}
