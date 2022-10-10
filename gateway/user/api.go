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
		Login(email, redirect string) (string, error)
		Callback(state, code string) (string, error)
		Signup(org *Org, user *User) (txId int64, err error)
		ContextByEmail(email string) (*Context, error)
	}
)

const redirectLogin = "http://localhost:3000"

func (h *Handler) Login(c *gin.Context) {
	email := c.Query("email")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "missing email query param"})
		return
	}
	redirect := c.Query("redirect")
	if redirect == "" {
		redirect = redirectLogin
	}

	url, err := h.Service.Login(email, redirect)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, map[string]string{"login_url": url})
}

func (h *Handler) Callback(c *gin.Context) {
	state := c.Query("state")
	code := c.Query("code")

	redirect, err := h.Service.Callback(state, code)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Redirect(http.StatusOK, redirect)
}
