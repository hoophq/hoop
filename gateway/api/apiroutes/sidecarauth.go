package apiroutes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// SidecarTokenHeader carries the pre-generated key a sidecar authenticates
// with. It is not the "authorization" header: a sidecar is not a user and
// never holds a JWT, and keeping it separate stops the token from reaching
// the JWT/hpk_ branches in AuthMiddleware.
const SidecarTokenHeader = "hoop-sidecar-token"

const sidecarContextKey = "sidecar-auth"

// SidecarAuthMiddleware authenticates a sidecar and installs an org-scoped
// context with no user. It records nothing: reporting what a sidecar last
// said is the handlers' job.
func (r *Router) SidecarAuthMiddleware(c *gin.Context) {
	token := c.GetHeader(SidecarTokenHeader)
	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	sidecar, err := models.GetSidecarByKeyHash(models.DB, models.HashAPIKey(token))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
			return
		}
		log.Errorf("failed looking up sidecar token, err=%v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	c.Set(sidecarContextKey, sidecar)
	c.Set(storagev2.ContextKey, storagev2.NewOrganizationContext(sidecar.OrgID).WithApiURL(r.apiURL))
	c.Next()
}

// SidecarFromContext returns the sidecar SidecarAuthMiddleware authenticated,
// or nil when the request did not pass through it.
func SidecarFromContext(c *gin.Context) *models.Sidecar {
	obj, ok := c.Get(sidecarContextKey)
	if !ok {
		return nil
	}
	sidecar, _ := obj.(*models.Sidecar)
	return sidecar
}
