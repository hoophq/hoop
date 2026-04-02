package transport

import (
	"encoding/base64"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/sshca"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

const defaultCertValidity = 24 * time.Hour

// injectSSHCertificate generates an ephemeral SSH certificate signed by the
// gateway CA and adds it to the connection secrets in-memory. The certificate
// is never persisted to the database. If the CA is not configured, it silently
// returns so that existing password/key auth continues to work.
func injectSSHCertificate(pctx *plugintypes.Context) {
	caSigner, err := sshca.LoadCASignerFromConfig()
	if err != nil {
		log.With("sid", pctx.SID, "connection", pctx.ConnectionName).
			Debugf("SSH CA not available, skipping certificate injection: %v", err)
		return
	}

	user, _ := pctx.ConnectionSecret["USER"].(string)
	if user == "" {
		log.With("sid", pctx.SID, "connection", pctx.ConnectionName).
			Warnf("SSH connection missing USER env var, skipping certificate injection")
		return
	}

	validity := resolveTokenValidity(pctx.UserID)

	certBytes, privKeyPEM, err := sshca.IssueCertificate(caSigner, []string{user}, validity)
	if err != nil {
		log.With("sid", pctx.SID, "connection", pctx.ConnectionName).
			Errorf("failed to issue SSH certificate: %v", err)
		return
	}

	pctx.ConnectionSecret["SSH_CERTIFICATE"] = base64.StdEncoding.EncodeToString(certBytes)
	pctx.ConnectionSecret["SSH_PRIVATE_KEY"] = base64.StdEncoding.EncodeToString(privKeyPEM)

	log.With("sid", pctx.SID, "connection", pctx.ConnectionName).
		Infof("issued ephemeral SSH certificate for principal %q, validity=%v", user, validity.Truncate(time.Second))
}

// resolveTokenValidity attempts to derive the certificate validity from the
// user's JWT token expiration. Falls back to defaultCertValidity if the token
// is not available or not parseable (e.g. opaque OIDC tokens).
func resolveTokenValidity(userID string) time.Duration {
	userToken, err := models.GetUserToken(models.DB, userID)
	if err != nil || userToken == nil {
		return defaultCertValidity
	}

	// Parse the JWT without validation to extract the expiration claim.
	// We only need the "exp" field; signature verification is not required here
	// because the token was already validated at authentication time.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	claims := jwt.MapClaims{}
	_, _, err = parser.ParseUnverified(userToken.Token, claims)
	if err != nil {
		return defaultCertValidity
	}

	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return defaultCertValidity
	}

	remaining := time.Until(exp.Time)
	if remaining <= 0 {
		return defaultCertValidity
	}
	return remaining
}
