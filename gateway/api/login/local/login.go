package loginlocalapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	localprovider "github.com/hoophq/hoop/gateway/idp/local"
	"github.com/hoophq/hoop/gateway/models"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
	Name     string `json:"name"`
}

func Login(c *gin.Context) {
	var user User
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
	instance, err := localprovider.GetInstance()
	if err != nil {
		return "", fmt.Errorf("failed to get local provider instance: %v", err)
	}
	return instance.NewAccessToken(subject, email, time.Hour*168) // 168 hours = 7 days
}
