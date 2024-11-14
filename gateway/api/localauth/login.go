package localauthapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
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

	tokenString, err := generateNewAccessToken(dbUser.ID, dbUser.Email)
	if err != nil {
		log.Errorf("failed signing token for %s, reason=%v", user.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to generate token"})
		return
	}

	c.Header("Access-Control-Expose-Headers", "Token")
	c.Header("Token", tokenString)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
