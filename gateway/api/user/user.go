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
	"github.com/runopsio/hoop/common/apiutils"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type StatusType string

const (
	StatusActive    StatusType = "active"
	StatusReviewing StatusType = "reviewing"
	StatusInactive  StatusType = "inactive"

	RoleAdminType        string = "admin"
	RoleStandardType     string = "standard"
	RoleUnregisteredType string = "unregistered"
	RoleGuestType        string = "guest"
)

type User struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Email    string     `json:"email"`
	Status   StatusType `json:"status"`
	Verified bool       `json:"verified"` // DEPRECATED in flavor of role
	Role     string     `json:"role"`
	SlackID  string     `json:"slack_id"`
	Groups   []string   `json:"groups"`
}

var isOrgMultiTenant = os.Getenv("ORG_MULTI_TENANT") == "true"

func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var newUser User
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
	newUser.Verified = false
	pguser := pgrest.User{
		ID:       newUser.ID,
		Subject:  newUser.Email,
		OrgID:    ctx.OrgID,
		Name:     newUser.Name,
		Email:    newUser.Email,
		Verified: newUser.Verified, // DEPRECATED in flavor of role
		Status:   string(StatusActive),
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
	var req User
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	// don't let admin users to remove admin group from themselves
	if existingUser.Subject == ctx.UserID {
		if !slices.Contains(req.Groups, types.GroupAdmin) {
			req.Groups = append(req.Groups, types.GroupAdmin)
		}
		if req.Status != StatusActive {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "cannot deactivate yourself"})
			return
		}
	}

	existingUser.Name = req.Name
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

	c.JSON(http.StatusOK, User{
		ID:       existingUser.Subject,
		Name:     existingUser.Name,
		Email:    existingUser.Email,
		Status:   StatusType(existingUser.Status),
		Verified: existingUser.Verified, // DEPRECATED in flavor of role
		Role:     toRole(*existingUser),
		SlackID:  existingUser.SlackID,
		Groups:   existingUser.Groups,
	})
}

func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	users, err := pgusers.New().FetchAll(ctx)
	if err != nil {
		log.Errorf("failed listing users, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed listing users"})
		return
	}
	userList := []User{}
	for _, u := range users {
		userList = append(userList,
			User{
				ID:       u.Subject,
				Name:     u.Name,
				Email:    u.Email,
				Status:   StatusType(u.Status),
				Verified: u.Verified,
				Role:     toRole(u), // DEPRECATED in flavor of role
				SlackID:  u.SlackID,
				Groups:   u.Groups,
			})
	}
	c.JSON(http.StatusOK, userList)
}

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

func GetUserByID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	emailOrID := c.Param("id")
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
	c.JSON(http.StatusOK, User{
		ID:       user.Subject,
		Name:     user.Name,
		Email:    user.Email,
		Status:   StatusType(user.Status),
		Verified: user.Verified, // DEPRECATED in flavor of role
		Role:     toRole(*user),
		SlackID:  user.SlackID,
		Groups:   user.Groups,
	})
}

func GetUserInfo(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	groupList := []string{}
	if len(ctx.UserGroups) > 0 {
		groupList = ctx.UserGroups
	}
	tenancyType := "selfhosted"
	if isOrgMultiTenant {
		tenancyType = "multitenant"
	}
	roleName := RoleStandardType
	switch {
	case ctx.IsAnonymous():
		roleName = RoleUnregisteredType
	case ctx.IsAdminUser():
		roleName = RoleAdminType
	}
	userInfoData := map[string]any{
		"id":             ctx.UserID,
		"name":           ctx.UserName,
		"email":          ctx.UserEmail,
		"status":         ctx.UserStatus,
		"verified":       true, // DEPRECATED in flavor of role (guest)
		"slack_id":       ctx.SlackID,
		"groups":         groupList,
		"is_admin":       ctx.IsAdminUser(), // DEPRECATED in flavor of role (admin)
		"is_multitenant": isOrgMultiTenant,  // DEPRECATED is flavor of tenancy_type
		"tenancy_type":   tenancyType,
		"role":           roleName,
		"org_id":         ctx.OrgID,
		"org_name":       ctx.OrgName,
	}
	if ctx.IsAnonymous() {
		userInfoData["verified"] = false
		userInfoData["email"] = ctx.UserAnonEmail
		userInfoData["name"] = ctx.UserAnonProfile
		userInfoData["id"] = ctx.UserAnonSubject
	}
	c.JSON(http.StatusOK, userInfoData)
}

func PatchSlackID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	u, err := pgusers.New().FetchOneBySubject(ctx, ctx.UserID)
	if err != nil || u == nil {
		errMsg := fmt.Errorf("failed obtaining user from store, notfound=%v, err=%v", u == nil, err)
		sentry.CaptureException(errMsg)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed obtaining user"})
		return
	}
	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	var slackID string
	if id, ok := req["slack_id"]; ok {
		slackID = fmt.Sprintf("%v", id)
	}
	if slackID == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "missing slack_id attribute"})
		return
	}
	u.SlackID = slackID
	if err := pgusers.New().Upsert(*u); err != nil {
		log.Errorf("failed updating slack id of user, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating slack id"})
		return
	}
	c.JSON(http.StatusOK, User{
		ID:       u.Subject,
		Name:     u.Name,
		Email:    u.Email,
		Status:   StatusType(u.Status),
		Verified: u.Verified,
		SlackID:  u.SlackID,
		Groups:   u.Groups,
	})
}

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
	if !user.Verified {
		return RoleGuestType
	}
	if slices.Contains(user.Groups, types.GroupAdmin) {
		return RoleAdminType
	}
	return RoleStandardType
}
