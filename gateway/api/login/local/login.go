package loginlocalapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"golang.org/x/crypto/bcrypt"
)

const defaultTokenExpiration = time.Hour * 12

// LocalAuthLogin
//
//	@Summary		Local | Login
//	@Description	Generate a new access token  to interact with the API that expires in 12 hours.
//	@Tags			Authentication
//	@Produce		json
//	@Success		200
//	@Param			Token			header		string	false	"The access token generated after a successful login"
//	@Failure		400,401,404,500	{object}	openapi.HTTPError
//	@Router			/localauth/login [get]
func Login(c *gin.Context) {
	var user openapi.LocalUserRequest
	if err := c.BindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	dbUser, err := models.GetUserByEmail(user.Email)
	if err != nil {
		log.Errorf("failed fetching user by email %s, reason=%v", user.Email, err)
		c.JSON(http.StatusUnauthorized, gin.H{"message": "invalid credentials"})
		return
	}
	if dbUser == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "user not found"})
		return
	}

	if dbUser.HashedPassword == "" {
		log.Warnf("user %s has no password set, cannot login", user.Email)
		c.JSON(http.StatusUnauthorized, gin.H{"message": "invalid credentials"})
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(dbUser.HashedPassword), []byte(user.Password))
	if err != nil {
		log.Errorf("failed comparing password for user %s, reason=%v", user.Email, err)
		c.JSON(http.StatusUnauthorized, gin.H{"message": "invalid credentials"})
		return
	}

	tokenString, err := generateNewAccessToken(dbUser.Email, dbUser.Email)
	if err != nil {
		log.Errorf("failed signing token for %s, reason=%v", user.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to generate token"})
		return
	}

	c.Header("Access-Control-Expose-Headers", "Token")
	c.Header("Token", tokenString)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func generateNewAccessToken(subject, email string) (string, error) {
	localVerifier, err := idp.NewLocalVerifierProvider()
	if err != nil {
		return "", fmt.Errorf("failed to get local provider instance: %v", err)
	}
	return localVerifier.NewAccessToken(subject, email, defaultTokenExpiration)
}
