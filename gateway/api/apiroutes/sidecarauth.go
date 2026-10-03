package apiroutes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// SidecarTokenHeader carries the pre-generated key a sidecar authenticates
// with. It is not the "authorization" header: a sidecar is not a user and
// never holds a JWT, and keeping it separate stops the token from reaching
// the JWT/hpk_ branches in AuthMiddleware.
const SidecarTokenHeader = "hoop-sidecar-token"

// SidecarIdentityHeader carries a platform service account JWT instead of a
// token. The name comes from the module that sends it, so a rename
// fails a build instead of breaking the path.
const SidecarIdentityHeader = daemon.SidecarIdentityHeader

const sidecarContextKey = "sidecar-auth"

// SidecarAuthMiddleware authenticates a sidecar and installs an org-scoped
// context with no user, carrying the organization's license so
// EnterpriseLicenseOnly can run after it. It records nothing: reporting what
// a sidecar last said is the handlers' job.
//
// A sidecar sends a token or a service account identity, never both: with
// two credentials there is no answer to which sidecar is calling.
func (r *Router) SidecarAuthMiddleware(c *gin.Context) {
	token := c.GetHeader(SidecarTokenHeader)
	identity := c.GetHeader(SidecarIdentityHeader)
	if token != "" && identity != "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "send one sidecar credential: the " +
			SidecarTokenHeader + " header or the " + SidecarIdentityHeader + " header, not both"})
		return
	}
	if identity != "" {
		r.sidecarIdentityAuth(c, identity)
		return
	}
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
	r.serveSidecar(c, sidecar)
}

// sidecarIdentityAuth resolves the sidecar a service account token reaches.
// A refused token is 401 with the reason; the sidecar logs it, and its
// operator fixes the mapping from it.
func (r *Router) sidecarIdentityAuth(c *gin.Context, identity string) {
	sidecar, err := services.AuthenticateSidecarIdentity(c.Request.Context(), models.DB, r.sidecarIdentity, identity)
	if err != nil {
		var refusal *services.SidecarIdentityRefusal
		if errors.As(err, &refusal) {
			log.With("reason", refusal.Message).Infof("refused a sidecar service account token")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": refusal.Message})
			return
		}
		log.Errorf("failed authenticating a sidecar service account token, err=%v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}
	r.serveSidecar(c, sidecar)
}

// serveSidecar installs the contexts every sidecar route reads, whichever
// credential authenticated the sidecar.
func (r *Router) serveSidecar(c *gin.Context, sidecar *models.Sidecar) {
	licenseData, err := models.GetOrgLicenseData(models.DB, sidecar.OrgID)
	if err != nil {
		log.Errorf("failed reading the organization license, err=%v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	c.Set(sidecarContextKey, sidecar)
	c.Set(storagev2.ContextKey, storagev2.NewOrganizationContext(sidecar.OrgID).
		WithOrgLicenseData(licenseData).
		WithApiURL(r.apiURL))
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
