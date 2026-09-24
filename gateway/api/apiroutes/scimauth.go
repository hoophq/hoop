package apiroutes

import (
	"errors"
	"net/http"
	"strings"

	scimerrors "github.com/elimity-com/scim/errors"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

const scimOrgContextKey = "scim-org"

// SCIMAuthMiddleware authenticates an identity provider's SCIM request by its
// bearer token and installs an org-scoped context with no user (ADR-0019).
// Errors are SCIM error documents, which is what a SCIM client parses.
func (r *Router) SCIMAuthMiddleware(c *gin.Context) {
	header := c.GetHeader("Authorization")
	token := ""
	if scheme, value, ok := strings.Cut(header, " "); ok && strings.EqualFold(scheme, "Bearer") {
		token = strings.TrimSpace(value)
	}
	if token == "" {
		abortSCIM(c, http.StatusUnauthorized, "access denied")
		return
	}

	scimToken, err := models.GetSCIMTokenByHash(models.DB, models.HashAPIKey(token))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			abortSCIM(c, http.StatusUnauthorized, "access denied")
			return
		}
		log.Errorf("failed looking up scim token, err=%v", err)
		abortSCIM(c, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := models.TouchSCIMToken(models.DB, scimToken.OrgID); err != nil {
		log.With("org", scimToken.OrgID).Warnf("failed recording scim token use, err=%v", err)
	}

	c.Set(scimOrgContextKey, scimToken.OrgID)
	c.Set(storagev2.ContextKey, storagev2.NewOrganizationContext(scimToken.OrgID).WithApiURL(r.apiURL))
	c.Next()
}

// SCIMOrgFromContext returns the org SCIMAuthMiddleware authenticated, or ""
// when the request did not pass through it.
func SCIMOrgFromContext(c *gin.Context) string {
	return c.GetString(scimOrgContextKey)
}

func abortSCIM(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", "application/scim+json")
	c.AbortWithStatusJSON(status, scimerrors.ScimError{Status: status, Detail: detail})
}
