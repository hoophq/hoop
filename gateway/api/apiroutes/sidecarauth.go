package apiroutes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
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

// sidecarContextKey holds a sidecar read with its configuration;
// sidecarWithoutConfigurationContextKey one read without it.
const (
	sidecarContextKey                     = "sidecar-auth"
	sidecarWithoutConfigurationContextKey = "sidecar-auth-no-configuration"
)

// SidecarRequestTimeout bounds every sidecar request. A sidecar gives up on
// the plane after 15s (controlPlaneTimeout, sidecar/daemon) and sends again;
// work past that answers nobody and holds a goroutine and a pool connection.
// The database calls of a sidecar route run under it and are cancelled with
// it.
const SidecarRequestTimeout = 15 * time.Second

// SidecarAuthMiddleware authenticates a sidecar, read with its
// configuration, and installs an org-scoped context with no user, carrying
// the organization's license so EnterpriseLicenseOnly can run after it. It
// records nothing: reporting what a sidecar last said is the handlers' job.
//
// A sidecar sends a token or a service account identity, never both: with
// two credentials there is no answer to which sidecar is calling.
func (r *Router) SidecarAuthMiddleware(c *gin.Context) { r.sidecarAuth(c, true) }

// SidecarAuthWithoutConfigurationMiddleware is SidecarAuthMiddleware for a
// route that never reads the configuration: event batches and review polls,
// which arrive far more often than handshakes. SidecarConfigurationFromContext
// answers nil behind it, so a handler that needs the configuration cannot be
// mounted here by mistake and read an empty one.
func (r *Router) SidecarAuthWithoutConfigurationMiddleware(c *gin.Context) { r.sidecarAuth(c, false) }

func (r *Router) sidecarAuth(c *gin.Context, withConfiguration bool) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), SidecarRequestTimeout)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	db := models.DB.WithContext(ctx)

	token := c.GetHeader(SidecarTokenHeader)
	identity := c.GetHeader(SidecarIdentityHeader)
	if token != "" && identity != "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "send one sidecar credential: the " +
			SidecarTokenHeader + " header or the " + SidecarIdentityHeader + " header, not both"})
		return
	}
	if identity != "" {
		r.sidecarIdentityAuth(c, db, identity, withConfiguration)
		return
	}
	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	lookup := models.GetSidecarByKeyHashWithoutConfiguration
	if withConfiguration {
		lookup = models.GetSidecarByKeyHash
	}
	sidecar, err := lookup(db, models.HashAPIKey(token))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
			return
		}
		log.Errorf("failed looking up sidecar token, err=%v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}
	r.serveSidecar(c, db, sidecar, withConfiguration)
}

// sidecarIdentityAuth resolves the sidecar a service account token reaches.
// A refused token is 401. Before the token is verified the answer is
// generic and the reason is only in this log, with the issuer the token
// names; after, the answer names the reason, the sidecar logs it, and its
// operator fixes the mapping from it. This is the one place a refusal is
// logged.
func (r *Router) sidecarIdentityAuth(c *gin.Context, db *gorm.DB, identity string, withConfiguration bool) {
	sidecar, err := services.AuthenticateSidecarIdentity(c.Request.Context(), db, r.sidecarIdentity, identity, withConfiguration)
	if err != nil {
		var refusal *services.SidecarIdentityRefusal
		if errors.As(err, &refusal) {
			l := log.With("issuer", refusal.Issuer, "reason", refusal.LogReason())
			if errors.Is(refusal, services.ErrSidecarIdentityAmbiguous) {
				// Mappings of two organizations match one token: the plane's
				// operator fixes it, not the sidecar's.
				l.Errorf("refused a sidecar service account token matched in more than one organization")
			} else {
				l.Infof("refused a sidecar service account token")
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": refusal.Message})
			return
		}
		log.Errorf("failed authenticating a sidecar service account token, err=%v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}
	r.serveSidecar(c, db, sidecar, withConfiguration)
}

// serveSidecar installs the contexts every sidecar route reads, whichever
// credential authenticated the sidecar. The license came with the sidecar
// row; a row read without it reads the org.
func (r *Router) serveSidecar(c *gin.Context, db *gorm.DB, sidecar *models.Sidecar, withConfiguration bool) {
	licenseData := sidecar.OrgLicenseData
	if !sidecar.OrgLicenseRead {
		var err error
		licenseData, err = models.GetOrgLicenseData(db, sidecar.OrgID)
		if err != nil {
			log.Errorf("failed reading the organization license, err=%v", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
			return
		}
	}

	key := sidecarWithoutConfigurationContextKey
	if withConfiguration {
		key = sidecarContextKey
	}
	c.Set(key, sidecar)
	c.Set(storagev2.ContextKey, storagev2.NewOrganizationContext(sidecar.OrgID).
		WithOrgLicenseData(licenseData).
		WithApiURL(r.apiURL))
	c.Next()
}

// SidecarFromContext returns the sidecar either sidecar middleware
// authenticated, or nil when the request passed through neither. Its
// configuration is empty behind SidecarAuthWithoutConfigurationMiddleware:
// a handler that reads it uses SidecarConfigurationFromContext.
func SidecarFromContext(c *gin.Context) *models.Sidecar {
	if sidecar := SidecarConfigurationFromContext(c); sidecar != nil {
		return sidecar
	}
	obj, ok := c.Get(sidecarWithoutConfigurationContextKey)
	if !ok {
		return nil
	}
	sidecar, _ := obj.(*models.Sidecar)
	return sidecar
}

// SidecarConfigurationFromContext returns the sidecar SidecarAuthMiddleware
// authenticated, read with its configuration, or nil when the request did
// not pass through that middleware.
func SidecarConfigurationFromContext(c *gin.Context) *models.Sidecar {
	obj, ok := c.Get(sidecarContextKey)
	if !ok {
		return nil
	}
	sidecar, _ := obj.(*models.Sidecar)
	return sidecar
}

// SidecarSeatOnly replaces EnterpriseLicenseOnly on the routes a sidecar
// calls. A free organization runs one sidecar under the free rule caps; a
// sidecar past its seats is refused here, so it stops at its next handshake
// instead of serving rules nobody pays for. Runs after a sidecar middleware.
func SidecarSeatOnly(c *gin.Context) {
	sidecar := SidecarFromContext(c)
	if sidecar == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "unauthorized"})
		return
	}
	ctx := storagev2.ParseContext(c)
	var data json.RawMessage
	if ctx.OrgLicenseData != nil {
		data = *ctx.OrgLicenseData
	}
	err := services.CheckSidecarSeat(models.DB.WithContext(c.Request.Context()), sidecar.OrgID, sidecar.ID, data)
	switch {
	case err == nil:
		c.Next()
	case errors.Is(err, services.ErrSidecarSeatLimit):
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": err.Error()})
	default:
		log.Errorf("failed checking the sidecar seat, err=%v", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
	}
}
