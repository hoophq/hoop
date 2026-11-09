package daemon

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// ICADeployTokenEnv enables only ICA admission drain, independently of licensing.
// Keep its secret in the host environment, never in a committed sidecar config.
const ICADeployTokenEnv = "HOOP_ICA_DEPLOY_TOKEN"

// ICAAdmissionDrainer stops admissions without closing existing desktop sessions.
type ICAAdmissionDrainer interface{ BeginICADrain() bool }

func icaDrainHandler(secret string, endpoints []endpointServer, proxyCount int) http.HandlerFunc {
	secret = strings.TrimSpace(secret)
	want := sha256.Sum256([]byte(secret))
	return func(w http.ResponseWriter, req *http.Request) {
		if len(secret) < 32 {
			writeAdminJSON(w, http.StatusForbidden, map[string]any{"error": "ICA deployment drain is disabled"})
			return
		}
		got := sha256.Sum256([]byte(bearerToken(req)))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="hoop-ica-deploy"`)
			writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing or wrong deployment token"})
			return
		}
		if req.ContentLength != 0 {
			writeAdminJSON(w, http.StatusBadRequest, map[string]any{"error": "drain takes no request body"})
			return
		}
		if len(endpoints) == 0 || proxyCount != 0 {
			writeAdminJSON(w, http.StatusConflict, map[string]any{"error": "ICA-only process required"})
			return
		}
		drainers := make([]ICAAdmissionDrainer, 0, len(endpoints))
		for _, endpoint := range endpoints {
			drainer, ok := endpoint.(ICAAdmissionDrainer)
			if !ok {
				writeAdminJSON(w, http.StatusConflict, map[string]any{"error": "endpoint does not support admission drain"})
				return
			}
			drainers = append(drainers, drainer)
		}
		for _, drainer := range drainers {
			if !drainer.BeginICADrain() {
				writeAdminJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ICA manager is not initialized"})
				return
			}
		}
		writeAdminJSON(w, http.StatusOK, map[string]any{"status": "draining"})
	}
}
