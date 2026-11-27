package userapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// selfUser returns the user who makes the request, or answers it and returns
// nil.
func selfUser(c *gin.Context) *models.User {
	ctx := storagev2.ParseContext(c)
	u, err := models.GetUserBySubjectAndOrg(ctx.UserID, ctx.OrgID)
	if err == nil && u == nil {
		err = gorm.ErrRecordNotFound
	}
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed obtaining the user")
		return nil
	}
	return u
}

// GetSlackLink
//
//	@Summary		Get Own Slack Link
//	@Description	Whether the user has a Slack user, and whether the user can link one with a one-time code.
//	@Tags			User Management
//	@Produce		json
//	@Success		200	{object}	openapi.UserSlackLink
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/users/self/slack/link [get]
func GetSlackLink(c *gin.Context) {
	u := selfUser(c)
	if u == nil {
		return
	}
	c.JSON(http.StatusOK, openapi.UserSlackLink{
		Available: slackservice.LinkAvailable(u.OrgID),
		Linked:    u.SlackID != "",
	})
}

// PostSlackLink
//
//	@Summary		Create Own Slack Link Code
//	@Description	Make the one-time code the user sends to the Hoop bot in Slack as `/hoop link <code>`. Hoop links the Slack user that sends it. A new code replaces the user's previous one.
//	@Tags			User Management
//	@Produce		json
//	@Success		201		{object}	openapi.UserSlackLinkCode
//	@Failure		412,500	{object}	openapi.HTTPError
//	@Router			/users/self/slack/link [post]
func PostSlackLink(c *gin.Context) {
	u := selfUser(c)
	if u == nil {
		return
	}
	if !slackservice.LinkAvailable(u.OrgID) {
		c.JSON(http.StatusPreconditionFailed, gin.H{"message": "the Slack integration is not set up, or connecting with a code is turned off"})
		return
	}
	code, expiresAt, err := slackservice.NewLinkCode(models.DB, u.OrgID, u.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating the slack link code")
		return
	}
	c.JSON(http.StatusCreated, openapi.UserSlackLinkCode{
		Code:      code,
		Command:   "/hoop " + slackservice.LinkCommandName + " " + code,
		ExpiresAt: expiresAt,
	})
}
