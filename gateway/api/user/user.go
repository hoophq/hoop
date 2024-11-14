package userapi

import (
	"errors"
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
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgaudit "github.com/hoophq/hoop/gateway/pgrest/audit"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
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

	existingUser, err := models.GetUserByEmailAndOrg(newUser.Email, ctx.OrgID)
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
	var hashedPassword string
	// user.Subject for local auth is altered in this flow, that's
	// why we create this separated variable so we can modify it
	// accordingly to the auth method
	userSubject := newUser.Email
	newUser.Verified = false
	if appconfig.Get().AuthMethod() == "local" {
		newUser.Verified = true
		hashedPwdBytes, err := bcrypt.GenerateFromPassword([]byte(newUser.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Errorf("failed hashing password, err=%v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
			return
		}
		hashedPassword = string(hashedPwdBytes)
		userSubject = fmt.Sprintf("local|%v", newUser.ID)
	}

	modelsUser := models.User{
		ID:             newUser.ID,
		Subject:        userSubject,
		OrgID:          ctx.OrgID,
		Name:           newUser.Name,
		HashedPassword: hashedPassword,
		Picture:        newUser.Picture,
		Email:          newUser.Email,
		Verified:       newUser.Verified,
		Status:         string(openapi.StatusInvited),
		SlackID:        newUser.SlackID,
	}
	if err := models.CreateUser(modelsUser); err != nil {
		log.Errorf("failed persisting invited user, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	if newUser.Groups != nil && len(newUser.Groups) > 0 {
		var userGroups []models.UserGroup
		for i := range newUser.Groups {
			userGroups = append(userGroups, models.UserGroup{
				OrgID:  ctx.OrgID,
				UserID: newUser.ID,
				Name:   newUser.Groups[i],
			})
		}
		if err := models.InsertUserGroups(userGroups); err != nil {
			log.Errorf("failed persisting user groups, err=%v", err)
			sentry.CaptureException(err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
			return
		}
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

	existingUser, err := models.GetUserBySubjectAndOrg(userID, ctx.OrgID)

	if err != nil {
		log.Errorf("failed getting user %s, err=%v", userID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user"})
		return
	}

	if existingUser == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": fmt.Sprintf("user %s not found", userID)})
		return
	}

	var req openapi.User
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	userGroups, err := models.GetUserGroupsByUserID(existingUser.ID)
	if err != nil {
		log.Errorf("failed getting user groups for user %s, err=%v", existingUser.ID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user groups"})
		return
	}
	var userGroupsList []string
	for ug := range userGroups {
		userGroupsList = append(userGroupsList, userGroups[ug].Name)
	}

	// don't let admin users to remove admin group from themselves
	if existingUser.Subject == ctx.UserID {
		// TODO: maybe refactor this to get from userGroups directly
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

	log.Debugf("updating user %s and its user groups", userID)

	newUserGroups := []models.UserGroup{}
	for i := range req.Groups {
		newUserGroups = append(newUserGroups, models.UserGroup{
			OrgID:  ctx.OrgID,
			UserID: existingUser.ID,
			Name:   req.Groups[i],
		})
	}
	// update user and user groups
	if err := models.UpdateUserAndUserGroups(existingUser, newUserGroups); err != nil {
		log.Errorf("failed updating user and user groups, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating user and user groups"})
		return
	}

	analytics.New().Identify(&types.APIContext{
		OrgID:      ctx.OrgID,
		OrgName:    ctx.OrgName,
		UserID:     existingUser.Email,
		UserName:   existingUser.Name,
		UserEmail:  existingUser.Email,
		UserGroups: req.Groups,
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
		Role:     toRole(req),
		SlackID:  existingUser.SlackID,
		Picture:  existingUser.Picture,
		Groups:   req.Groups,
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
	users, err := models.ListUsers(ctx.OrgID)
	if err != nil {
		log.Errorf("failed listing users, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing users"})
		return
	}

	orgsGroups, err := models.GetUserGroupsByOrgID(ctx.OrgID)
	if err != nil {
		log.Errorf("failed getting org groups, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting org groups"})
		return
	}

	// map users from db to openapi.User
	usersList := []openapi.User{}
	for i, u := range users {
		usersList = append(usersList,
			openapi.User{
				ID:       u.Subject,
				Name:     u.Name,
				Email:    u.Email,
				Status:   openapi.StatusType(u.Status),
				Verified: u.Verified,
				SlackID:  u.SlackID,
				Picture:  u.Picture,
			})
		usersList[i].Groups = []string{}
		for _, ug := range orgsGroups {
			if ug.UserID == u.ID {
				usersList[i].Groups = append(usersList[i].Groups, ug.Name)
			}
		}
		usersList[i].Role = toRole(usersList[i])
	}

	c.JSON(http.StatusOK, usersList)
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
	user, err := models.GetUserBySubjectAndOrg(subject, ctx.OrgID)
	if err != nil {
		log.Errorf("failed getting user %s, err=%v", subject, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": fmt.Sprintf("user %s not found", subject)})
		return
	}
	if user.Subject == ctx.UserID {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "cannot delete yourself"})
		return
	}
	if err := models.DeleteUser(ctx.OrgID, subject); err != nil {
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
	var user *models.User
	var err error
	if isValidMailAddress(emailOrID) {
		user, err = models.GetUserByEmailAndOrg(emailOrID, ctx.OrgID)
	} else {
		user, err = models.GetUserBySubjectAndOrg(emailOrID, ctx.OrgID)
	}
	if err != nil {
		log.Errorf("failed getting user %s, err=%v", emailOrID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": fmt.Sprintf("user %s not found", emailOrID)})
		return
	}

	userGroups, err := models.GetUserGroupsByUserID(user.ID)
	if err != nil {
		log.Errorf("failed getting user groups for user %s, err=%v", user.ID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user groups"})
		return
	}

	userResponse := openapi.User{
		ID:       user.Subject,
		Name:     user.Name,
		Email:    user.Email,
		Status:   openapi.StatusType(user.Status),
		Verified: user.Verified, // DEPRECATED in flavor of role
		SlackID:  user.SlackID,
		Picture:  user.Picture,
	}
	userResponse.Groups = []string{}
	for _, ug := range userGroups {
		userResponse.Groups = append(userResponse.Groups, ug.Name)
	}
	userResponse.Role = toRole(userResponse)

	c.JSON(http.StatusOK, userResponse)
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
	case ctx.IsAuditorUser():
		roleName = openapi.RoleAuditorType
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
	u, err := models.GetUserBySubjectAndOrg(ctx.UserID, ctx.OrgID)

	if err != nil || u == nil {
		errMsg := fmt.Errorf("failed obtaining user from store, notfound=%v, err=%v", errors.Is(err, gorm.ErrRecordNotFound), err)
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
	if err := models.UpdateUser(u); err != nil {
		log.Errorf("failed updating slack id of user, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating slack id"})
		return
	}

	userGroups, err := models.GetUserGroupsByUserID(u.ID)
	if err != nil {
		log.Errorf("failed getting user groups for user %s, err=%v", u.ID, err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed getting user groups"})
		return
	}
	var userGroupsList []string
	for ug := range userGroups {
		userGroupsList = append(userGroupsList, userGroups[ug].Name)
	}

	c.JSON(http.StatusOK, openapi.User{
		ID:       u.Subject,
		Name:     u.Name,
		Email:    u.Email,
		Status:   openapi.StatusType(u.Status),
		Verified: u.Verified,
		SlackID:  u.SlackID,
		Picture:  u.Picture,
		Groups:   userGroupsList,
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
	userGroups, err := models.GetUserGroupsByOrgID(ctx.OrgID)
	var groups []string
	for ug := range userGroups {
		groups = append(groups, userGroups[ug].Name)
	}
	if err != nil {
		log.Errorf("failed listing groups, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing groups"})
		return
	}
	dedupeGroups := map[string]string{}
	for _, ug := range userGroups {
		dedupeGroups[ug.Name] = ug.Name
	}
	var groupsList []string
	for groupName := range dedupeGroups {
		groupsList = append(groupsList, groupName)
	}
	c.JSON(http.StatusOK, groupsList)
}

func isValidMailAddress(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

func toRole(user openapi.User) string {
	if slices.Contains(user.Groups, types.GroupAdmin) {
		return string(openapi.RoleAdminType)
	}
	return string(openapi.RoleStandardType)
}

func toRoleLegacy(user pgrest.User) string {
	if slices.Contains(user.Groups, types.GroupAdmin) {
		return string(openapi.RoleAdminType)
	}
	return string(openapi.RoleStandardType)
}
