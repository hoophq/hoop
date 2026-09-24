package apisidecar

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// slackChannelsSidecar returns the sidecar the path names, or answers the
// request and returns nil. Only a control plane files sidecar reviews, so a
// gateway answers 412, as it does for /sidecars/reviews.
func slackChannelsSidecar(c *gin.Context) *models.Sidecar {
	if !appconfig.Get().IsControlPlane() {
		c.JSON(http.StatusPreconditionFailed, gin.H{"message": "sidecar slack channels are served by the control plane"})
		return nil
	}
	ctx := storagev2.ParseContext(c)
	item, err := models.GetSidecarByNameOrID(models.DB, ctx.OrgID, c.Param("nameOrID"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
			return nil
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar")
		return nil
	}
	return item
}

func toOpenAPISlackChannels(rows []models.SidecarSlackChannels) openapi.SidecarSlackChannels {
	out := openapi.SidecarSlackChannels{Channels: []string{}, Listeners: []openapi.SidecarListenerSlackChannels{}}
	for _, r := range rows {
		if r.ListenerName == "" {
			out.Channels = []string(r.Channels)
			continue
		}
		out.Listeners = append(out.Listeners, openapi.SidecarListenerSlackChannels{
			Name: r.ListenerName, Channels: []string(r.Channels),
		})
	}
	return out
}

// normalizeChannels trims the ids and drops blanks and repeats.
func normalizeChannels(channels []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, ch := range channels {
		ch = strings.TrimSpace(ch)
		if ch == "" || seen[ch] {
			continue
		}
		seen[ch] = true
		out = append(out, ch)
	}
	return out
}

var errInvalidChannels = errors.New("invalid slack channels")

// slackChannelRows turns the request into the rows to store, or says why it
// cannot: a listener the sidecar does not have, or one named twice.
func slackChannelRows(sidecar *models.Sidecar, req openapi.SidecarSlackChannels) ([]models.SidecarSlackChannels, string) {
	known := map[string]bool{}
	for _, l := range sidecar.Configuration.Listeners {
		if l.Name != "" {
			known[l.Name] = true
		}
	}
	rows := []models.SidecarSlackChannels{{ListenerName: "", Channels: normalizeChannels(req.Channels)}}
	seen := map[string]bool{}
	for _, l := range req.Listeners {
		name := strings.TrimSpace(l.Name)
		if !known[name] {
			return nil, fmt.Sprintf("sidecar %s has no listener named %q", sidecar.Name, name)
		}
		if seen[name] {
			return nil, fmt.Sprintf("listener %q is repeated", name)
		}
		seen[name] = true
		rows = append(rows, models.SidecarSlackChannels{ListenerName: name, Channels: normalizeChannels(l.Channels)})
	}
	return rows, ""
}

// GetSlackChannels
//
//	@Summary		Get Sidecar Slack Channels
//	@Description	Where the sidecar's reviews are posted in Slack. Control plane only.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			nameOrID		path		string	true	"Name or UUID of the sidecar"
//	@Success		200				{object}	openapi.SidecarSlackChannels
//	@Failure		404,412,500		{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/slack-channels [get]
func GetSlackChannels(c *gin.Context) {
	sidecar := slackChannelsSidecar(c)
	if sidecar == nil {
		return
	}
	rows, err := models.ListSidecarSlackChannels(models.DB, sidecar.OrgID, sidecar.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar slack channels")
		return
	}
	c.JSON(http.StatusOK, toOpenAPISlackChannels(rows))
}

// PutSlackChannels
//
//	@Summary		Set Sidecar Slack Channels
//	@Description	Replace where the sidecar's reviews are posted in Slack. A listener's channels replace the sidecar's; an empty list inherits. Control plane only.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID			path		string							true	"Name or UUID of the sidecar"
//	@Param			request				body		openapi.SidecarSlackChannels	true	"The request body resource"
//	@Success		200					{object}	openapi.SidecarSlackChannels
//	@Failure		400,404,412,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/{nameOrID}/slack-channels [put]
func PutSlackChannels(c *gin.Context) {
	sidecar := slackChannelsSidecar(c)
	if sidecar == nil {
		return
	}
	var req openapi.SidecarSlackChannels
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// A listener must exist in the stored configuration: channels set for a
	// name no listener has would never be read, and would silently start
	// applying to whatever listener takes that name later. The configuration
	// is re-read with a row lock, so a listener removed by a concurrent write
	// cannot slip in between the check and the write.
	var invalid string
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		locked, err := models.GetSidecarByNameOrIDForUpdate(tx, sidecar.OrgID, sidecar.ID)
		if err != nil {
			return err
		}
		rows, msg := slackChannelRows(locked, req)
		if msg != "" {
			invalid = msg
			return errInvalidChannels
		}
		return models.ReplaceSidecarSlackChannels(tx, sidecar.OrgID, sidecar.ID, rows)
	})
	switch {
	case errors.Is(err, errInvalidChannels):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": invalid})
		return
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "sidecar not found"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed storing sidecar slack channels")
		return
	}
	saved, err := models.ListSidecarSlackChannels(models.DB, sidecar.OrgID, sidecar.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching sidecar slack channels")
		return
	}
	c.JSON(http.StatusOK, toOpenAPISlackChannels(saved))
}
