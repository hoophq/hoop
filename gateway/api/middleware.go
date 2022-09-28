package api

import (
	"errors"

	"github.com/gin-gonic/gin"
)

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

func (api *Api) Authenticate(c *gin.Context) {
	email := "tester@hoop.dev"

	ctx, err := api.UserHandler.Service.ContextByEmail(email)
	if err != nil {
		c.Error(err)
		return
	}

	if ctx == nil {
		c.Error(errors.New("user not found"))
		return
	}

	c.Set("context", ctx)
	c.Next()
}
