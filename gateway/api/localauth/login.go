package localauthapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/pgrest"
	pglocalauthsession "github.com/hoophq/hoop/gateway/pgrest/localauthsession"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"golang.org/x/crypto/bcrypt"
)

func Login(c *gin.Context) {
	var user pgrest.User
	if err := c.BindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dbUser, err := pgusers.GetOneByEmail(user.Email)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(dbUser.Password), []byte(user.Password))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	expirationTime := time.Now().Add(168 * time.Hour) // 7 days
	claims := &Claims{
		UserID:    dbUser.ID,
		UserEmail: dbUser.Email,
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

	newLocalAuthSession := pgrest.LocalAuthSession{
		ID:        uuid.New().String(),
		UserID:    dbUser.ID,
		UserEmail: dbUser.Email,
		Token:     tokenString,
		ExpiresAt: expirationTime.Format(time.RFC3339),
	}
	_, err = pglocalauthsession.CreateSession(newLocalAuthSession)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store session"})
		return
	}

	c.Header("Access-Control-Expose-Headers", "Token")
	c.Header("Token", tokenString)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
