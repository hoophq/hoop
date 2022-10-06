package api

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	pb "github.com/runopsio/hoop/common/proto"
)

var invalidAuthErr = errors.New("invalid auth")

func (api *Api) Authenticate(c *gin.Context) {
	email, err := validateClaims(c)
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

func validateClaims(c *gin.Context) (string, error) {
	if PROFILE == pb.DevProfile {
		return "tester@hoop.dev", nil
	}

	tokenHeader := c.GetHeader("authorization")
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" || tokenParts[1] == "" {
		return "", invalidAuthErr
	}
	return parseClaims(tokenParts[1])
}

func parseClaims(tokenValue string) (string, error) {
	token, err := jwt.Parse(tokenValue, jwks.Keyfunc)
	if err != nil {
		return "", invalidAuthErr
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		email, ok := claims["https://hoop.dev/email"].(string)
		if !ok || email == "" {
			return "", invalidAuthErr
		}
		return email, nil
	}

	return "", invalidAuthErr
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, accept, origin")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
