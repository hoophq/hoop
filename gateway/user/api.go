package user

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Signup(org *Org, user *User) (txId int64, err error)
		FindBySub(sub string) (*Context, error)
		FindAll(context *Context) ([]User, error)
		FindOne(context *Context, id string) (*User, error)
		Persist(user any) error
	}
)

func (a *Handler) FindAll(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	users, err := a.Service.FindAll(context)
	if err != nil {
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
		existingUser.isAdmin() &&
		!isInList(GroupAdmin, user.Groups) {
		user.Groups = append(user.Groups, GroupAdmin)
	}

	existingUser.Status = user.Status
	existingUser.Groups = user.Groups

	err = a.Service.Persist(existingUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

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
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, newUser)
}
