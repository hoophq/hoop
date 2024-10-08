package localauthapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/hoophq/hoop/common/log"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/crypto/bcrypt"
)

func Register(c *gin.Context) {
	var user User
	if err := c.BindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Debugf("looking for existing user %v", user.Email)
	// fetch user by email
	existingUser, err := models.GetUserByEmail(user.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		return
	}
	if existingUser != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User already exists"})
		return
	}

	// local auth creates the user at the default organization for now.
	// we plan to make it much more permissive, but at this moment this
	// limitation comes to make sure things are working as expected.
	org, totalUsers, err := pgorgs.New().FetchOrgByName("default")
	if err != nil {
		log.Debugf("failed fetching default organization, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch default organization"})
		return
	}
	// if there is one user already, do not allow new users to be created
	// it avoids a security issue of anyone being able to add themselves to
	// the default organization. Instead, they should get an invitation
	if totalUsers > 0 {
		log.Debugf("default organization already has users")
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	adminGroupName := types.GroupAdmin
	userID := uuid.New().String()
	err = models.CreateUser(models.User{
		ID:             userID,
		Subject:        fmt.Sprintf("local|%v", userID),
		OrgID:          org.ID,
		Email:          user.Email,
		Name:           user.Name,
		Status:         "active",
		Verified:       true,
		HashedPassword: string(hashedPassword),
	})

	if err != nil {
		log.Debugf("failed creating user, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	adminUserGroup := models.UserGroup{
		OrgID:  org.ID,
		UserID: userID,
		Name:   adminGroupName,
	}

	err = models.InsertUserGroups([]models.UserGroup{adminUserGroup})
	if err != nil {
		log.Errorf("failed creating user group, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user group"})
		return
	}

	expirationTime := time.Now().Add(168 * time.Hour) // 7 days
	claims := &Claims{
		UserID:      userID,
		UserEmail:   user.Email,
		UserSubject: fmt.Sprintf("local|%v", userID),
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

	c.JSON(http.StatusCreated, gin.H{"message": "User created successfully"})
}
