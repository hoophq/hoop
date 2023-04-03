package user

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
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
		Persist(user any) error
		ListAllGroups(context *Context) ([]string, error)
		CreateDefaultOrganization() error
	}

	Analytics interface {
		Identify(ctx *Context)
		Track(userID, eventName string, properties map[string]any)
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

	id := c.Param("id")
	user, err := a.Service.FindOne(context, id)
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
		!pb.IsInList(GroupAdmin, user.Groups) {
		user.Groups = append(user.Groups, GroupAdmin)
	}

	existingUser.Status = user.Status
	existingUser.Groups = user.Groups

	err = a.Service.Persist(existingUser)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	context.User = existingUser
	a.Analytics.Identify(context)

	c.JSON(http.StatusOK, existingUser)
}

func (a *Handler) Post(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	var newUser InvitedUser
	if err := c.ShouldBindJSON(&newUser); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	if newUser.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "email can't be empty"})
		return
	}

	newUser.Id = uuid.NewString()
	newUser.Org = context.Org.Id

	err := a.Service.Persist(&newUser)
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newUser)
}

func (a *Handler) Userinfo(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	c.JSON(http.StatusOK, context.User)
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
