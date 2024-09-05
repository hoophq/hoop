package localauthapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"golang.org/x/crypto/bcrypt"
)

// body: {"email": "some@example.com", "password": "password"}
func Register(c *gin.Context) {
	var user pgrest.User
	if err := c.BindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	err = pgusers.New().Upsert(pgrest.User{
		ID:       uuid.New().String(),
		Email:    user.Email,
		Password: string(hashedPassword),
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "User created successfully"})
}
