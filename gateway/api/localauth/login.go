package localauthapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"golang.org/x/crypto/bcrypt"
)

func Login(c *gin.Context) {
	var user openapi.User
	if err := c.BindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dbUser, err := pgusers.GetOneByEmail(user.Email)
	if err != nil {
		log.Debugf("failed fetching user by email %s, %v", user.Email, err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(dbUser.HashedPassword), []byte(user.HashedPassword))
	if err != nil {
		log.Debugf("failed comparing password for user %s, %v", user.Email, err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	expirationTime := time.Now().Add(168 * time.Hour) // 7 days
	claims := &Claims{
		UserID:      dbUser.ID,
		UserEmail:   dbUser.Email,
		UserSubject: dbUser.Subject,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(appconfig.Get().JWTSecretKey())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.Header("Access-Control-Expose-Headers", "Token")
	c.Header("Token", tokenString)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
