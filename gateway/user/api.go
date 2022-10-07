package user

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Signup(org *Org, user *User) (txId int64, err error)
		ContextByEmail(email string) (*Context, error)

		ListOrgs(context *Context) error
	}
)

func (h *Handler) ListOrgs(c *gin.Context) {
	ctx, _ := c.Get("context")
	context := ctx.(*Context)

	err := h.Service.ListOrgs(context)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Status(http.StatusOK)
}
