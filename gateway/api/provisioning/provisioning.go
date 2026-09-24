// Package apiprovisioning configures how a control plane learns its reviewers
// without anyone logging in (ADR-0019): the Slack directory sync, and the
// switch that hands groups back to login and the Users page.
package apiprovisioning

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/directorysync"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

const (
	defaultIntervalMinutes = 15
	minIntervalMinutes     = 5
	listGroupsTimeout      = 30 * time.Second
)

// controlPlaneOnly answers 412 outside the control plane: a gateway's groups
// are not provisioned.
func controlPlaneOnly(c *gin.Context) bool {
	if appconfig.Get().IsControlPlane() {
		return true
	}
	c.AbortWithStatusJSON(http.StatusPreconditionFailed,
		gin.H{"message": "provisioning is served by the control plane"})
	return false
}

func toOpenAPIDirectorySync(cfg *models.DirectorySyncConfig) openapi.DirectorySyncConfig {
	if cfg == nil {
		return openapi.DirectorySyncConfig{
			Provider:        models.ProvisioningSourceSlack,
			IntervalMinutes: defaultIntervalMinutes,
			GroupIDs:        []string{},
		}
	}
	groupIDs := []string(cfg.GroupIDs)
	if groupIDs == nil {
		groupIDs = []string{}
	}
	return openapi.DirectorySyncConfig{
		Enabled:                  true,
		Provider:                 cfg.Provider,
		GroupIDs:                 groupIDs,
		IntervalMinutes:          cfg.IntervalMinutes,
		AllowMemberManagedGroups: cfg.AllowMemberManagedGroups,
		LastRunAt:                cfg.LastRunAt,
		LastError:                cfg.LastError,
	}
}

// loadSync returns the org's sync, nil when there is none.
func loadSync(c *gin.Context, orgID string) (*models.DirectorySyncConfig, bool) {
	cfg, err := models.GetDirectorySyncConfig(models.DB, orgID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, true
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the directory sync")
		return nil, false
	}
	return cfg, true
}

// GetDirectorySync
//
//	@Summary		Get Directory Sync
//	@Description	Get the directory sync that pulls users and groups from Slack user groups. Control plane only.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200			{object}	openapi.DirectorySyncConfig
//	@Failure		412,500		{object}	openapi.HTTPError
//	@Router			/serverconfig/directory-sync [get]
func GetDirectorySync(c *gin.Context) {
	if !controlPlaneOnly(c) {
		return
	}
	ctx := storagev2.ParseContext(c)
	cfg, ok := loadSync(c, ctx.OrgID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, toOpenAPIDirectorySync(cfg))
}

// PutDirectorySync
//
//	@Summary		Configure Directory Sync
//	@Description	Configure the Slack directory sync: the user groups to sync, the interval, and whether member-managed user groups are accepted. It uses the org's Slack app. Control plane only.
//	@Tags			Server Management
//	@Accept			json
//	@Produce		json
//	@Param			request				body		openapi.DirectorySyncRequest	true	"The request body resource"
//	@Success		200					{object}	openapi.DirectorySyncConfig
//	@Failure		400,412,422,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/directory-sync [put]
func PutDirectorySync(c *gin.Context) {
	if !controlPlaneOnly(c) {
		return
	}
	ctx := storagev2.ParseContext(c)
	var req openapi.DirectorySyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.IntervalMinutes == 0 {
		req.IntervalMinutes = defaultIntervalMinutes
	}
	if req.IntervalMinutes < minIntervalMinutes {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "interval_minutes must be at least 5"})
		return
	}

	cfg := &models.DirectorySyncConfig{
		OrgID:                    ctx.OrgID,
		Provider:                 models.ProvisioningSourceSlack,
		GroupIDs:                 req.GroupIDs,
		IntervalMinutes:          req.IntervalMinutes,
		AllowMemberManagedGroups: req.AllowMemberManagedGroups,
	}
	if err := models.UpsertDirectorySyncConfig(models.DB, cfg); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed storing the directory sync")
		return
	}
	saved, ok := loadSync(c, ctx.OrgID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, toOpenAPIDirectorySync(saved))
}

// DeleteDirectorySync
//
//	@Summary		Delete Directory Sync
//	@Description	Stop the directory sync. Synced users and groups stay as they are, and stay managed until groups are released with DELETE /serverconfig/provisioning. Control plane only.
//	@Tags			Server Management
//	@Success		204
//	@Failure		412,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/directory-sync [delete]
func DeleteDirectorySync(c *gin.Context) {
	if !controlPlaneOnly(c) {
		return
	}
	ctx := storagev2.ParseContext(c)
	if err := models.DeleteDirectorySyncConfig(models.DB, ctx.OrgID); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting the directory sync")
		return
	}
	c.Status(http.StatusNoContent)
}

// RunDirectorySync
//
//	@Summary		Run Directory Sync
//	@Description	Run the directory sync now and return its outcome in last_run_at and last_error. Every run writes one audit entry with what changed. Control plane only.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200					{object}	openapi.DirectorySyncConfig
//	@Failure		404,409,412,422,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/directory-sync/run [post]
func RunDirectorySync(c *gin.Context) {
	if !controlPlaneOnly(c) {
		return
	}
	ctx := storagev2.ParseContext(c)
	cfg, ok := loadSync(c, ctx.OrgID)
	if !ok {
		return
	}
	if cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "no directory sync is configured"})
		return
	}
	if len(cfg.GroupIDs) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "select at least one group to sync"})
		return
	}
	// Detached from the request: a sync that the admin's browser stopped
	// waiting for still has to finish or roll back as a whole.
	actor := directorysync.Actor{Subject: ctx.UserID, Email: ctx.UserEmail, Name: ctx.UserName}
	err := directorysync.Run(context.Background(), models.DB, ctx.OrgID, actor)
	if errors.Is(err, directorysync.ErrSyncRunning) {
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
		return
	}
	// Any other failure is recorded on the config and returned in last_error.
	saved, ok := loadSync(c, ctx.OrgID)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, toOpenAPIDirectorySync(saved))
}

// ListDirectorySyncGroups
//
//	@Summary		List Directory Groups
//	@Description	List the Slack user groups the directory sync can read, for choosing which ones to sync. admin_managed is false for a user group a member who is not a workspace admin or owner edited last; the sync refuses it unless member-managed groups are allowed. Control plane only.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200					{array}		openapi.DirectoryGroup
//	@Failure		412,422,500,502		{object}	openapi.HTTPError
//	@Router			/serverconfig/directory-sync/groups [get]
func ListDirectorySyncGroups(c *gin.Context) {
	if !controlPlaneOnly(c) {
		return
	}
	ctx := storagev2.ParseContext(c)
	provider, err := directorysync.NewSlackProvider(ctx.OrgID)
	if errors.Is(err, directorysync.ErrSlackNotConfigured) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed opening the slack directory")
		return
	}
	reqCtx, cancel := context.WithTimeout(c.Request.Context(), listGroupsTimeout)
	defer cancel()
	groups, err := directorysync.ListGroupsWithGovernance(reqCtx, provider)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	out := make([]openapi.DirectoryGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, openapi.DirectoryGroup{ID: g.ID, Name: g.Name, AdminManaged: g.AdminManaged})
	}
	c.JSON(http.StatusOK, out)
}

// GetProvisioningStatus
//
//	@Summary		Get Provisioning Status
//	@Description	Report whether the Slack import owns the org's groups. While it does, login and the Users page change only the admin group. Control plane only.
//	@Tags			Server Management
//	@Produce		json
//	@Success		200			{object}	openapi.ProvisioningStatus
//	@Failure		412,500		{object}	openapi.HTTPError
//	@Router			/serverconfig/provisioning [get]
func GetProvisioningStatus(c *gin.Context) {
	if !controlPlaneOnly(c) {
		return
	}
	ctx := storagev2.ParseContext(c)
	managed, err := models.GroupsManagedByProvisioning(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed checking the provisioning status")
		return
	}
	c.JSON(http.StatusOK, openapi.ProvisioningStatus{GroupsManaged: managed})
}

// StopManagingGroups
//
//	@Summary		Stop Managing Groups
//	@Description	Hand the org's groups back to login and the Users page. It deletes only the links between users and their source; users and their groups stay. Refused while the directory sync exists, since its next run would take the groups back. Control plane only.
//	@Tags			Server Management
//	@Success		204
//	@Failure		409,412,500	{object}	openapi.HTTPError
//	@Router			/serverconfig/provisioning [delete]
func StopManagingGroups(c *gin.Context) {
	if !controlPlaneOnly(c) {
		return
	}
	ctx := storagev2.ParseContext(c)
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := models.GetDirectorySyncConfig(tx, ctx.OrgID); err == nil {
			return errSourceActive
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return models.ClearProvisioningLinks(tx, ctx.OrgID)
	})
	switch {
	case errors.Is(err, errSourceActive):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed releasing the managed groups")
	default:
		c.Status(http.StatusNoContent)
	}
}

var errSourceActive = errors.New("remove the Slack import first; its next run would manage the groups again")
