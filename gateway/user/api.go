package user

import (
	"fmt"
	"net/http"
	"net/mail"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type (
	Handler struct {
		Service   service
		Analytics Analytics
	}

	service interface {
		Signup(org *Org, user *User) (txId int64, err error)
		FindBySub(sub string) (*Context, error)
		FindAll(context *Context) ([]User, error)
		FindOne(context *Context, id string) (*User, error)
		FindByEmail(ctx *Context, email string) (*User, error)
		FindBySlackID(ctx *Org, slackID string) (*User, error)
		FindInvitedUser(email string) (*InvitedUser, error)
		Persist(user any) error
		ListAllGroups(context *Context) ([]string, error)
		CreateDefaultOrganization() error
		GetOrgByName(name string) (*Org, error)
	}

	Analytics interface {
		Identify(ctx *types.APIContext)
		Track(ctx *types.APIContext, eventName string, properties map[string]any)
	}
)

func (a *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	users, err := a.Service.FindAll(context)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.PureJSON(http.StatusOK, users)
}

func (a *Handler) FindOne(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	emailOrID := c.Param("id")
	var user *User
	var err error
	if isValidMailAddress(emailOrID) {
		user, err = a.Service.FindByEmail(context, emailOrID)
	} else {
		user, err = a.Service.FindOne(context, emailOrID)
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

func (a *Handler) Put(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	id := c.Param("id")
	existingUser, err := a.Service.FindOne(context, id)
	if err != nil {
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if existingUser == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "not found"})
		return
	}

	var user User
	if err := c.ShouldBindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if !isInStatus(user.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "invalid status"})
		return
	}

	// for admins to not auto-exclude themselves from admin by mistake
	if existingUser.Id == context.User.Id &&
		existingUser.IsAdmin() &&
		!pb.IsInList(types.GroupAdmin, user.Groups) {
		user.Groups = append(user.Groups, types.GroupAdmin)
	}

	existingUser.Status = user.Status
	existingUser.SlackID = user.SlackID
	existingUser.Groups = user.Groups

	err = a.Service.Persist(existingUser)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	context.User = existingUser
	a.Analytics.Identify(context.ToAPIContext())

	c.JSON(http.StatusOK, existingUser)
}

func (a *Handler) UpdateSelfSlackID(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)
	self, err := a.Service.FindOne(context, context.User.Id)
	if err != nil {
		log.Errorf("failed obtaining user from store, reason=%v", err)
		sentry.CaptureException(err)
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
	self.SlackID = slackID
	err = a.Service.Persist(self)
	if err != nil {
		log.Errorf("failed updating slack id of user, reason=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed updating slack id"})
		return
	}
	c.JSON(http.StatusOK, self)
}

func (a *Handler) Post(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	var newUser InvitedUser
	if err := c.ShouldBindJSON(&newUser); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if !isValidMailAddress(newUser.Email) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "invalid email"})
		return
	}

	existingUser, err := a.Service.FindInvitedUser(newUser.Email)
	if err != nil {
		log.Errorf("failed fetching existing invited user, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching existing invited user"})
		return
	}
	if existingUser != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "user was already invited"})
		return
	}

	newUser.Id = uuid.NewString()
	newUser.Org = context.Org.Id

	if err := a.Service.Persist(&newUser); err != nil {
		log.Errorf("failed persisting invited user, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newUser)
}

func (a *Handler) Userinfo(c *gin.Context) {
	context, _ := c.Get("context")
	ctx := context.(*Context)
	groupList := []string{}
	if len(ctx.User.Groups) > 0 {
		groupList = ctx.User.Groups
	}
	c.JSON(http.StatusOK, &struct {
		ID            string     `json:"id"`
		OrgID         string     `json:"org_id"`
		Name          string     `json:"name"`
		Email         string     `json:"email"`
		Status        StatusType `json:"status"`
		SlackID       string     `json:"slack_id"`
		Groups        []string   `json:"groups"`
		IsAdmin       bool       `json:"is_admin"`
		IsMultiTenant bool       `json:"is_multitenant"`
		IsApiV2       bool       `json:"is_apiv2"`
	}{
		ID:            ctx.User.Id,
		OrgID:         ctx.Org.Id,
		Name:          ctx.User.Name,
		Email:         ctx.User.Email,
		Status:        ctx.User.Status,
		SlackID:       ctx.User.SlackID,
		Groups:        groupList,
		IsAdmin:       ctx.User.IsAdmin(),
		IsMultiTenant: IsOrgMultiTenant(),
		IsApiV2:       ctx.Org.IsApiV2,
	})
}

func (a *Handler) UsersGroups(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	groups, err := a.Service.ListAllGroups(context)
	if err != nil {
		log.Printf("failed to list groups, err: %v", err)
		sentry.CaptureException(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.PureJSON(http.StatusOK, groups)
}

func isValidMailAddress(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// ContextLogger do a best effort to get the context logger,
// if it fail to retrieve, returns a noop logger
func ContextLogger(c *gin.Context) *zap.SugaredLogger {
	obj, _ := c.Get(ContextLoggerKey)
	if logger := obj.(*zap.SugaredLogger); logger != nil {
		return logger
	}
	return zap.NewNop().Sugar()
}

// ContextUser do a best effort to get the user context from the request
// if it fail, it will return an empty one that can be used safely
func ContextUser(c *gin.Context) *Context {
	obj, _ := c.Get("context")
	ctx := obj.(*Context)
	if ctx == nil {
		return &Context{Org: &Org{}, User: &User{}}
	}
	if ctx.Org == nil {
		ctx.Org = &Org{}
	}
	if ctx.User == nil {
		ctx.User = &User{}
	}
	return ctx
}
