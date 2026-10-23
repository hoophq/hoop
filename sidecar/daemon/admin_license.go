package daemon

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// AdminSecretEnv holds the secret PUT /license on the admin listener asks
// for, as `Authorization: Bearer <secret>`. The operator chooses it. Unset,
// the endpoint refuses every request, so the admin listener stays read-only
// unless someone turns this on.
//
// An environment variable and never a config key, for the reason the
// control-plane token is one: a bearer secret does not belong in a file that
// gets committed, or in a document a control plane serves.
const AdminSecretEnv = "HOOP_SIDECAR_ADMIN_SECRET"

// maxLicenseBody bounds a license request. A document Hoop issues is a few
// hundred bytes; the bound only keeps a caller from making the process
// buffer an arbitrary body.
const maxLicenseBody = 64 << 10

// licenseHandler serves PUT /license: the body is the license document, and
// a current license that covers the running rules goes into force at once.
// See installLicense for what is refused.
func licenseHandler(secret string, rl *reloader, log *slog.Logger) http.HandlerFunc {
	secret = strings.TrimSpace(secret)
	// Compared as digests, so the time the compare takes says nothing
	// about the secret's length.
	want := sha256.Sum256([]byte(secret))
	return func(w http.ResponseWriter, req *http.Request) {
		if secret == "" {
			writeAdminJSON(w, http.StatusForbidden, map[string]any{
				"error": "license installs through the admin API are off; set " + AdminSecretEnv + " to turn them on",
			})
			return
		}
		got := sha256.Sum256([]byte(bearerToken(req)))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			log.Warn("admin license request refused: missing or wrong secret", "remote", req.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="hoop-sidecar-admin"`)
			writeAdminJSON(w, http.StatusUnauthorized, map[string]any{"error": "missing or wrong admin secret"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxLicenseBody))
		if err != nil {
			status := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			writeAdminJSON(w, status, map[string]any{"error": "cannot read the request body: " + err.Error()})
			return
		}
		lic, installed, err := rl.installLicense(log, string(body))
		switch {
		case errors.Is(err, errLicenseManaged):
			writeAdminJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		case err != nil:
			log.Warn("admin license install refused", "remote", req.RemoteAddr, "error", err)
			writeAdminJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
			return
		}
		outcome := "unchanged"
		if installed {
			outcome = "installed"
		}
		// Report, never the document: the signature is what makes a license
		// reusable, and the admin listener does not hand one out.
		writeAdminJSON(w, http.StatusOK, map[string]any{"outcome": outcome, "license": lic.Report()})
	}
}

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
