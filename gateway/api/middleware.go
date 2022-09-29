package api

import (
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	pb "github.com/runopsio/hoop/proto"
	"net/http"
	"strings"
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
	email := parseEmail(c)

	ctx, err := api.UserHandler.Service.ContextByEmail(email)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
	}

	if ctx == nil {
		c.AbortWithStatus(http.StatusUnauthorized)
	}

	c.Set("context", ctx)
	c.Next()
}

func parseEmail(c *gin.Context) string {
	if MODE == pb.DevProfile {
		return "tester@hoop.dev"
	}

	tokenHeader := c.GetHeader("authorization")

	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		c.AbortWithStatus(http.StatusUnauthorized)
	}

	tokenValue := tokenParts[1]

	token, err := jwt.Parse(tokenValue, jwks.Keyfunc)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
	}

	var email string
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		email = claims["https://runops.io/email"].(string)
	} else {
		c.AbortWithStatus(http.StatusUnauthorized)
	}

	return email
}
