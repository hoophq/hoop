// Package sidecarcredentials serves the credentials a user mints for a
// sidecar lane, and the grants that say who may mint one.
package sidecarcredentials

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

// sidecarCredentialCapability is the header entry a build that decodes
// credential_set sends, generated from the field's cap tag.
const sidecarCredentialCapability = "credential_set"

// sidecarCredentialMaxHours is services.SidecarCredentialMaxTTL in hours.
const sidecarCredentialMaxHours = int(services.SidecarCredentialMaxTTL / time.Hour)

// lane is the sidecar and the listener a path names. laneFromPath answers
// the request and returns nil when either is unknown.
type lane struct {
	sidecar  *models.Sidecar
	listener daemon.ListenerConfig
}

func laneFromPath(c *gin.Context) *lane {
	ctx := storagev2.ParseContext(c)
	sc, err := models.GetSidecarByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return nil
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar")
		return nil
	}
	name := c.Param("lane")
	for _, l := range sc.Configuration.Listeners {
		if l.Name == name {
			return &lane{sidecar: sc, listener: l}
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"message": "sidecar " + sc.Name + " has no listener named " + name})
	return nil
}

func toOpenAPIGrant(g models.SidecarLaneGrant) openapi.SidecarLaneGrant {
	out := openapi.SidecarLaneGrant{
		ID:           g.ID,
		SidecarID:    g.SidecarID,
		ListenerName: g.ListenerName,
		CreatedBy:    g.CreatedBy,
		CreatedAt:    g.CreatedAt,
	}
	if g.Subject != nil {
		out.Subject = *g.Subject
	}
	if g.GroupName != nil {
		out.GroupName = *g.GroupName
	}
	return out
}

// ListGrants
//
//	@Summary		List Sidecar Lane Grants
//	@Description	Who may mint a credential for this lane of the sidecar.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			nameOrID	path		string	true	"Name or UUID of the sidecar"
//	@Param			lane		path		string	true	"Listener name"
//	@Success		200			{array}		openapi.SidecarLaneGrant
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/lanes/{lane}/grants [get]
func ListGrants(c *gin.Context) {
	ln := laneFromPath(c)
	if ln == nil {
		return
	}
	rows, err := models.ListSidecarLaneGrants(models.DB, ln.sidecar.OrgID, ln.sidecar.ID, ln.listener.Name)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing sidecar lane grants")
		return
	}
	out := make([]openapi.SidecarLaneGrant, 0, len(rows))
	for _, g := range rows {
		out = append(out, toOpenAPIGrant(g))
	}
	c.JSON(http.StatusOK, out)
}

// CreateGrant
//
//	@Summary		Create Sidecar Lane Grant
//	@Description	Let a user or a group mint credentials for this lane of the sidecar. The body names one of the two.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string							true	"Name or UUID of the sidecar"
//	@Param			lane		path		string							true	"Listener name"
//	@Param			request		body		openapi.SidecarLaneGrantRequest	true	"The request body resource"
//	@Success		201			{object}	openapi.SidecarLaneGrant
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/lanes/{lane}/grants [post]
func CreateGrant(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ln := laneFromPath(c)
	if ln == nil {
		return
	}
	var req openapi.SidecarLaneGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	subject := strings.TrimSpace(req.Subject)
	group := strings.TrimSpace(req.GroupName)
	if (subject == "") == (group == "") {
		c.JSON(http.StatusBadRequest, gin.H{"message": "set subject or group_name, not both"})
		return
	}
	g := &models.SidecarLaneGrant{
		OrgID:        ln.sidecar.OrgID,
		SidecarID:    ln.sidecar.ID,
		ListenerName: ln.listener.Name,
		CreatedBy:    ctx.UserEmail,
	}
	if subject != "" {
		g.Subject = &subject
	} else {
		g.GroupName = &group
	}
	if err := models.CreateSidecarLaneGrant(models.DB, g); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar lane grant")
		return
	}
	c.JSON(http.StatusCreated, toOpenAPIGrant(*g))
}

// DeleteGrant
//
//	@Summary		Delete Sidecar Lane Grant
//	@Description	Remove a grant. Credentials already minted under it keep working until they expire or are revoked.
//	@Tags			Sidecars
//	@Param			nameOrID	path	string	true	"Name or UUID of the sidecar"
//	@Param			lane		path	string	true	"Listener name"
//	@Param			id			path	string	true	"The grant id"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/lanes/{lane}/grants/{id} [delete]
func DeleteGrant(c *gin.Context) {
	ln := laneFromPath(c)
	if ln == nil {
		return
	}
	err := models.DeleteSidecarLaneGrant(models.DB, ln.sidecar.OrgID, ln.sidecar.ID, ln.listener.Name, c.Param("id"))
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "grant not found"})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting sidecar lane grant")
	default:
		c.Status(http.StatusNoContent)
	}
}

// Mint
//
//	@Summary		Mint Sidecar Lane Credential
//	@Description	Mint a key the caller presents to this lane of the sidecar as their password. The key is shown once. Refused when no grant of the lane names the caller or one of their groups.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string								true	"Name or UUID of the sidecar"
//	@Param			lane		path		string								true	"Listener name"
//	@Param			request		body		openapi.SidecarCredentialRequest	false	"The request body resource"
//	@Success		201			{object}	openapi.SidecarCredential
//	@Failure		400,403,404,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/lanes/{lane}/credentials [post]
func Mint(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	ln := laneFromPath(c)
	if ln == nil {
		return
	}
	var req openapi.SidecarCredentialRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
			return
		}
	}
	ttl := services.SidecarCredentialDefaultTTL
	if req.ExpiresInHours < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "expires_in_hours must be positive"})
		return
	}
	// Bound the hours before the Duration multiply, which can overflow.
	if req.ExpiresInHours > sidecarCredentialMaxHours {
		c.JSON(http.StatusBadRequest, gin.H{"message": "expires_in_hours cannot exceed 720"})
		return
	}
	if req.ExpiresInHours > 0 {
		ttl = time.Duration(req.ExpiresInHours) * time.Hour
	}

	allowed, err := models.HasSidecarLaneGrant(models.DB, ctx.OrgID, ln.sidecar.ID, ln.listener.Name,
		[]string{ctx.UserID, ctx.UserEmail}, ctx.UserGroups)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed checking sidecar lane grants")
		return
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"message": "no grant lets you mint a credential for this lane"})
		return
	}
	// A build without the entry refuses the whole document that carries the
	// set, and with it every rule of every lane. Refusing the mint instead
	// keeps the sidecar served. Before the first handshake the build is
	// unknown, so the mint waits for it.
	if !slices.Contains(ln.sidecar.Capabilities, sidecarCredentialCapability) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "sidecar " + ln.sidecar.Name + " runs a build that does not decode credentials; upgrade it first"})
		return
	}

	cred, key, err := services.MintSidecarCredential(models.DB, ln.sidecar, ln.listener.Name, ctx.UserID, ttl)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed minting the credential")
		return
	}
	log.With("sidecar", ln.sidecar.Name, "lane", ln.listener.Name, "user", ctx.UserEmail).
		Infof("minted a sidecar lane credential, expires_at=%v", cred.ExpireAt.Format(time.RFC3339))
	c.JSON(http.StatusCreated, openapi.SidecarCredential{
		ID:           cred.ID,
		SidecarName:  ln.sidecar.Name,
		ListenerName: ln.listener.Name,
		Protocol:     ln.listener.Protocol,
		User:         ctx.UserEmail,
		Key:          key,
		CreatedAt:    cred.CreatedAt,
		ExpiresAt:    cred.ExpireAt,
	})
}

// ListMine
//
//	@Summary		List My Sidecar Lane Credentials
//	@Description	The caller's live credentials, across sidecars. The key is never returned.
//	@Tags			Sidecars
//	@Produce		json
//	@Success		200	{array}		openapi.SidecarCredential
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/users/me/sidecar-credentials [get]
func ListMine(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	rows, err := models.ListSidecarCredentialsByUser(models.DB, ctx.OrgID, ctx.UserID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing sidecar credentials")
		return
	}
	out := make([]openapi.SidecarCredential, 0, len(rows))
	for _, r := range rows {
		sidecarName, laneName, _ := models.ParseSidecarCredentialConnectionName(r.ConnectionName)
		out = append(out, openapi.SidecarCredential{
			ID:           r.ID,
			SidecarName:  sidecarName,
			ListenerName: laneName,
			User:         r.UserEmail,
			CreatedAt:    r.CreatedAt,
			ExpiresAt:    r.ExpireAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

// Revoke
//
//	@Summary		Revoke Sidecar Lane Credential
//	@Description	Stop a credential. A user revokes their own; an admin revokes any.
//	@Tags			Sidecars
//	@Param			id	path	string	true	"The credential id"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/sidecar-credentials/{id} [delete]
func Revoke(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	cred, err := models.GetSidecarCredential(models.DB, ctx.OrgID, c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "credential not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching the credential")
		return
	}
	// Another user's credential reads as absent, so the id leaks nothing.
	if cred.UserID != ctx.UserID && !ctx.IsAdmin() {
		c.JSON(http.StatusNotFound, gin.H{"message": "credential not found"})
		return
	}
	if cred.RevokedAt != nil {
		c.Status(http.StatusNoContent)
		return
	}
	if err := models.RevokeSidecarCredential(models.DB, ctx.OrgID, cred.ID); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed revoking the credential")
		return
	}
	c.Status(http.StatusNoContent)
}
