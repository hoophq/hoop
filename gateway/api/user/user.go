package userapi

import (
	"net/http"
	"net/mail"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	userstorage "github.com/runopsio/hoop/gateway/storagev2/user"
)

func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var newUser types.InvitedUser
	if err := c.ShouldBindJSON(&newUser); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if !isValidMailAddress(newUser.Email) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "invalid email"})
		return
	}

	invitedUser, err := userstorage.FindInvitedUser(ctx, newUser.Email)
	if err != nil {
		log.Errorf("failed fetching existing invited user, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching existing invited user"})
		return
	}
	if invitedUser != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "user was already invited"})
		return
	}

	newUser.ID = uuid.NewString()
	newUser.OrgID = ctx.OrgID

	if err := userstorage.UpdateInvitedUser(ctx, &newUser); err != nil {
		log.Errorf("failed persisting invited user, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	trackInvitedUserContext := &types.APIContext{
		OrgID:      ctx.OrgID,
		OrgName:    ctx.OrgName,
		UserID:     newUser.Email,
		UserName:   newUser.Name,
		UserEmail:  newUser.Email,
		UserGroups: newUser.Groups,
	}
	ctx.Analytics().Identify(trackInvitedUserContext)
	ctx.Analytics().Track(trackInvitedUserContext, "signup", map[string]any{})

	c.JSON(http.StatusCreated, newUser)

}

func GetUserByID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	emailOrID := c.Param("id")
	var user *types.User
	var err error
	if isValidMailAddress(emailOrID) {
		user, err = userstorage.FindByEmail(ctx, emailOrID)
	} else {
		user, err = userstorage.GetEntity(ctx, emailOrID)
	}
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	c.PureJSON(http.StatusOK, user)
}

func isValidMailAddress(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}
