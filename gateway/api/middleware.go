package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	pb "github.com/runopsio/hoop/common/proto"
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
	email, err := parseEmail(c)
	if err != nil {
		log.Printf("failed authenticating, err=%v", err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	ctx, err := api.UserHandler.Service.ContextByEmail(email)
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	if ctx == nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	c.Set("context", ctx)
	c.Next()
}

func parseEmail(c *gin.Context) (string, error) {
	if PROFILE == pb.DevProfile {
		return "tester@hoop.dev", nil
	}

	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		return "", fmt.Errorf("failed parsing token from header")
	}
	tokenValue := tokenParts[1]

	token, err := jwt.Parse(tokenValue, jwks.Keyfunc)
	if err != nil {
		return "", err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims["https://runops.io/email"].(string), nil
	}
	return "", fmt.Errorf("invalid token")
}
