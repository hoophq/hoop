package userapi

import (
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"slices"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/apiutils"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgaudit "github.com/hoophq/hoop/gateway/pgrest/audit"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/crypto/bcrypt"
)

var isOrgMultiTenant = os.Getenv("ORG_MULTI_TENANT") == "true"

// InviteUser
//
//	@Summary		Invite User
//	@Description	Inviting a user will pre configure user definitions like display name, profile picture, groups or his slack id
//	@Tags			User Management
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.User	true	"The request body resource"
//	@Success		201				{object}	openapi.User
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/users [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var newUser openapi.User
	if err := c.ShouldBindJSON(&newUser); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if !isValidMailAddress(newUser.Email) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "invalid email"})
		return
	}

	existingUser, err := pgusers.New().FetchOneByEmail(ctx, newUser.Email)
	if err != nil {
		log.Errorf("failed fetching existing invited user, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching existing invited user"})
		return
	}
	if existingUser != nil {
		c.JSON(http.StatusConflict, gin.H{"message": fmt.Sprintf("user already exists with email %s", newUser.Email)})
		return
	}

	newUser.ID = uuid.NewString()
	// user.Subject for local auth is altered in this flow, that's
	// why we create this separated variable so we can modify it
	// accordingly to the auth method
	userSubject := newUser.Email
	if appconfig.Get().AuthMethod() == "local" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newUser.Password), bcrypt.DefaultCost)
		newUser.Password = string(hashedPassword)
		if err != nil {
			log.Errorf("failed hashing password, err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
		userSubject = fmt.Sprintf("local|%v", newUser.ID)
	}

	newUser.Verified = false
	pguser := pgrest.User{
		ID:       newUser.ID,
		Subject:  userSubject,
		OrgID:    ctx.OrgID,
		Name:     newUser.Name,
		Password: newUser.Password,
		Picture:  newUser.Picture,
		Email:    newUser.Email,
		Verified: newUser.Verified, // DEPRECATED in flavor of role
		Status:   string(openapi.StatusActive),
		SlackID:  newUser.SlackID,
		Groups:   newUser.Groups,
	}
	newUser.Role = toRole(pguser)
	if err := pgusers.New().Upsert(pguser); err != nil {
		log.Errorf("failed persisting invited user, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	ctx.Analytics().Identify(&types.APIContext{
		OrgID:      ctx.OrgID,
		OrgName:    ctx.OrgName,
		UserID:     newUser.Email,
		UserName:   newUser.Name,
		UserEmail:  newUser.Email,
		UserGroups: newUser.Groups,
	})
	go func() {
		// wait some time until the identify call get times to reach to intercom
		time.Sleep(time.Second * 10)
		properties := map[string]any{
			"user-agent": apiutils.NormalizeUserAgent(c.Request.Header.Values),
			"name":       newUser.Name,
			"api-url":    ctx.ApiURL,
		}
		ctx.Analytics().Track(newUser.Email, analytics.EventSignup, properties)
		ctx.Analytics().Track(newUser.Email, analytics.EventCreateInvitedUser, properties)
	}()

	c.JSON(http.StatusCreated, newUser)
}

// UpdateUser
//
//	@Summary		Update User
//	@Description	Updates an existing user
//	@Tags			User Management
//	@Accept			json
//	@Produce		json
//	@Param			id			path		string			true	"The subject identifier of the user"
//	@Param			request		body		openapi.User	true	"The request body resource"
//	@Success		200			{object}	openapi.User
//	@Failure		400,422,500	{object}	openapi.HTTPError
//	@Router			/users/{id} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	userID := c.Param("id")

	existingUser, err := pgusers.New().FetchOneBySubject(ctx, userID)
	if err != nil {
		log.Errorf("failed getting user %s, err=%v", userID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user"})
		return
	}
	if existingUser == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	var req openapi.User
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	// don't let admin users to remove admin group from themselves
	if existingUser.Subject == ctx.UserID {
		if !slices.Contains(req.Groups, types.GroupAdmin) {
			req.Groups = append(req.Groups, types.GroupAdmin)
		}
		if req.Status != openapi.StatusActive {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "cannot deactivate yourself"})
			return
		}
	}

	existingUser.Name = req.Name
	existingUser.Picture = req.Picture
	existingUser.Status = string(req.Status)
	existingUser.SlackID = req.SlackID
	existingUser.Groups = req.Groups

	if err := pgusers.New().Upsert(*existingUser); err != nil {
		log.Errorf("failed updating user %s, err=%v", userID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	analytics.New().Identify(&types.APIContext{
		OrgID:      ctx.OrgID,
		OrgName:    ctx.OrgName,
		UserID:     existingUser.Email,
		UserName:   existingUser.Name,
		UserEmail:  existingUser.Email,
		UserGroups: existingUser.Groups,
		UserStatus: existingUser.Status,
		SlackID:    existingUser.SlackID,
		ApiURL:     ctx.ApiURL,
		GrpcURL:    ctx.GrpcURL,
	})

	c.JSON(http.StatusOK, openapi.User{
		ID:       existingUser.Subject,
		Name:     existingUser.Name,
		Email:    existingUser.Email,
		Status:   openapi.StatusType(existingUser.Status),
		Verified: existingUser.Verified, // DEPRECATED in flavor of role
		Role:     toRole(*existingUser),
		SlackID:  existingUser.SlackID,
		Picture:  existingUser.Picture,
		Groups:   existingUser.Groups,
	})
}

// ListUsers
//
//	@Summary		List Users
//	@Description	List all users
//	@Tags			User Management
//	@Produce		json
//	@Success		200	{array}		openapi.User
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/users [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	users, err := pgusers.New().FetchAll(ctx)
	if err != nil {
		log.Errorf("failed listing users, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing users"})
		return
	}
	userList := []openapi.User{}
	for _, u := range users {
		userList = append(userList,
			openapi.User{
				ID:       u.Subject,
				Name:     u.Name,
				Email:    u.Email,
				Status:   openapi.StatusType(u.Status),
				Verified: u.Verified,
				Role:     toRole(u), // DEPRECATED in flavor of role
				SlackID:  u.SlackID,
				Picture:  u.Picture,
				Groups:   u.Groups,
			})
	}
	c.JSON(http.StatusOK, userList)
}

// DeleteUser
//
//	@Summary		Delete User
//	@Description	Delete a user.
//	@Tags			User Management
//	@Produce		json
//	@Param			id	path	string	true	"The subject identifier of the user"
//	@Success		204
//	@Failure		404,422,500	{object}	openapi.HTTPError
//	@Router			/users/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	subject := c.Param("id")
	user, err := pgusers.New().FetchOneBySubject(ctx, subject)
	if err != nil {
		log.Errorf("failed getting user %s, err=%v", subject, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	if user.Subject == ctx.UserID {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "cannot delete yourself"})
		return
	}
	if err := pgusers.New().Delete(ctx, subject); err != nil {
		log.Errorf("failed removing user %s, err=%v", subject, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed deleting user"})
		return
	}
	c.Writer.WriteHeader(204)
}

// GetUserByEmailOrID
//
//	@Summary		Get User
//	@Description	Get user by email or subject id
//	@Tags			User Management
//	@Produce		json
//	@Param			emailOrID	path		string	true	"The subject identifier or email of the user"
//	@Success		200			{object}	openapi.User
//	@Failure		404,500		{object}	openapi.HTTPError
//	@Router			/users/{emailOrID} [get]
func GetUserByEmailOrID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	emailOrID := c.Param("emailOrID")
	var user *pgrest.User
	var err error
	if isValidMailAddress(emailOrID) {
		user, err = pgusers.New().FetchOneByEmail(ctx, emailOrID)
	} else {
		user, err = pgusers.New().FetchOneBySubject(ctx, emailOrID)
	}
	if err != nil {
		log.Errorf("failed getting user %s, err=%v", emailOrID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}
	c.JSON(http.StatusOK, openapi.User{
		ID:       user.Subject,
		Name:     user.Name,
		Email:    user.Email,
		Status:   openapi.StatusType(user.Status),
		Verified: user.Verified, // DEPRECATED in flavor of role
		Role:     toRole(*user),
		SlackID:  user.SlackID,
		Picture:  user.Picture,
		Groups:   user.Groups,
	})
}

func getAskAIFeatureStatus(ctx pgrest.OrgContext) (string, error) {
	if !appconfig.Get().IsAskAIAvailable() {
		return "unavailable", nil
	}
	isEnabled, err := pgaudit.New().IsFeatureAskAiEnabled(ctx)
	if err != nil {
		return "", err
	}
	if isEnabled {
		return "enabled", nil
	}
	return "disabled", nil
}

// GetUserInfo
//
//	@Summary		Get UserInfo
//	@Description	Get own user's information
//	@Tags			User Management
//	@Produce		json
//	@Success		200	{object}	openapi.UserInfo
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/userinfo [get]
func GetUserInfo(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	askAIFeatureStatus, err := getAskAIFeatureStatus(ctx)
	if err != nil {
		log.Errorf("unable to obtain ask-ai feature status, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to obtain ask-ai feature status"})
		return
	}
	groupList := []string{}
	if len(ctx.UserGroups) > 0 {
		groupList = ctx.UserGroups
	}
	tenancyType := "selfhosted"
	if isOrgMultiTenant {
		tenancyType = "multitenant"
	}
	roleName := openapi.RoleStandardType
	switch {
	case ctx.IsAnonymous():
		roleName = openapi.RoleUnregisteredType
	case ctx.IsAdminUser():
		roleName = openapi.RoleAdminType
	}
	userInfoData := openapi.UserInfo{
		User: openapi.User{
			ID:       ctx.UserID,
			Name:     ctx.UserName,
			Email:    ctx.UserEmail,
			Picture:  ctx.UserPicture,
			Status:   openapi.StatusType(ctx.UserStatus),
			Role:     string(roleName),
			Verified: true, // DEPRECATED in flavor of role (guest)
			SlackID:  ctx.SlackID,
			Groups:   groupList,
		},
		IsAdmin:               ctx.IsAdminUser(), // DEPRECATED in flavor of role (admin)
		IsMultitenant:         isOrgMultiTenant,  // DEPRECATED is flavor of tenancy_type
		TenancyType:           tenancyType,
		OrgID:                 ctx.OrgID,
		OrgName:               ctx.OrgName,
		OrgLicense:            ctx.OrgLicense,
		FeatureAskAI:          askAIFeatureStatus,
		WebAppUsersManagement: appconfig.Get().WebappUsersManagement(),
	}
	if ctx.IsAnonymous() {
		userInfoData.Verified = false
		userInfoData.Email = ctx.UserAnonEmail
		userInfoData.Name = ctx.UserAnonProfile
		userInfoData.Picture = ctx.UserAnonPicture
		userInfoData.ID = ctx.UserAnonSubject
	}
	c.JSON(http.StatusOK, userInfoData)
}

// PatchUserSlackID
//
//	@Summary		Patch User Slack ID
//	@Description	Patch own user's slack id
//	@Tags			User Management
//	@Param			request	body	openapi.UserPatchSlackID	true	"The request body resource"
//	@Produce		json
//	@Success		200			{object}	openapi.User
//	@Failure		400,422,500	{object}	openapi.HTTPError
//	@Router			/users/self/slack [patch]
func PatchSlackID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	u, err := pgusers.New().FetchOneBySubject(ctx, ctx.UserID)
	if err != nil || u == nil {
		errMsg := fmt.Errorf("failed obtaining user from store, notfound=%v, err=%v", u == nil, err)
		sentry.CaptureException(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining user"})
		return
	}
	var req openapi.UserPatchSlackID
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.SlackID == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing slack_id attribute"})
		return
	}
	u.SlackID = req.SlackID
	if err := pgusers.New().Upsert(*u); err != nil {
		log.Errorf("failed updating slack id of user, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating slack id"})
		return
	}
	c.JSON(http.StatusOK, openapi.User{
		ID:       u.Subject,
		Name:     u.Name,
		Email:    u.Email,
		Status:   openapi.StatusType(u.Status),
		Verified: u.Verified,
		SlackID:  u.SlackID,
		Picture:  u.Picture,
		Groups:   u.Groups,
	})
}

// ListUserGroups
//
//	@Summary		List User Groups
//	@Description	List all groups from all users
//	@Tags			User Management
//	@Produce		json
//	@Success		200	{array}		string
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/users/groups [get]
func ListAllGroups(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	groups, err := pgusers.New().ListAllGroups(ctx)
	if err != nil {
		log.Errorf("failed listing groups, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing groups"})
		return
	}
	c.JSON(http.StatusOK, groups)
}

func isValidMailAddress(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

func toRole(user pgrest.User) string {
	if slices.Contains(user.Groups, types.GroupAdmin) {
		return string(openapi.RoleAdminType)
	}
	return string(openapi.RoleStandardType)
}
