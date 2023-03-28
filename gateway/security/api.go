package security

import (
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
)

type (
	Handler struct {
		Service service
	}

	service interface {
		Login(callback string) (string, error)
		Callback(state, code string) string
	}
)

func (h *Handler) Login(c *gin.Context) {
	url, err := h.Service.Login(defaultRedirect(c))
	if err == errAuthDisabled {
		log.Warnf(errAuthDisabled.Error())
		c.JSON(http.StatusBadRequest, gin.H{"message": errAuthDisabled.Error()})
		return
	}
	if err != nil {
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, map[string]string{"login_url": url})
}

func (h *Handler) Callback(c *gin.Context) {
	errorMsg := c.Query("error")
	if errorMsg != "" {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	state := c.Query("state")
	code := c.Query("code")

	redirect := h.Service.Callback(state, code)
	c.Redirect(http.StatusTemporaryRedirect, redirect)
}

func defaultRedirect(c *gin.Context) string {
	defaultUrl := fmt.Sprintf("http://%s/callback", pb.ClientLoginCallbackAddress)
	redirect := c.Query("redirect")
	if redirect == "" {
		redirect = defaultUrl
	}

	return redirect
}
