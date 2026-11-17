package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
)

// removedAdminSecretEnv held the secret of PUT /license, which is gone. Run
// warns when an operator still sets it, so the missing endpoint is not a
// silent change.
const removedAdminSecretEnv = "HOOP_SIDECAR_ADMIN_SECRET"

// bearerToken is the credential of an `Authorization: Bearer` header, empty
// when the request carries none.
func bearerToken(req *http.Request) string {
	const prefix = "Bearer "
	h := req.Header.Get("Authorization")
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
