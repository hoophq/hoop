package localauthapi

import (
	"fmt"
	"libhoop/log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/pgrest"
	pglocalauthsession "github.com/hoophq/hoop/gateway/pgrest/localauthsession"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/crypto/bcrypt"
)

// If the system is set as single tenant,
// we default the new user to the default organization.
// Otherwise, a new organization is created for the user.
func manageOrgCreation(user pgrest.User) (string, error) {
	var tenancy string
	if pgusers.IsOrgMultiTenant() {
		tenancy = "multi-tenant"
	} else {
		tenancy = "single-tenant"
	}
	switch tenancy {
	case "multi-tenant":
		log.Debug("Creating new organization")
		newOrgID, err := pgorgs.New().CreateOrGetOrg(fmt.Sprintf("%q Orgnization", user.Email), nil)
		if err != nil {
			log.Debugf("failed creating organization, err=%v", err)
			return "", fmt.Errorf("Failed to create organization")
		}
		return newOrgID, nil
	default:
		// fetch default organization
		org, totalUsers, err := pgorgs.New().FetchOrgByName("default")
		if err != nil {
			return "", fmt.Errorf("Failed to fetch default organization")
		}
		// if there is one user already, do not allow new users to be created
		// it avoids a security issue of anyone being able to add themselves to
		// the default organization. Instead, they should get an invitation
		if totalUsers > 0 {
			return "", fmt.Errorf("You can not access this instance. Please contact your administrator")
		}

		return org.ID, nil
	}
}

func Register(c *gin.Context) {
	fmt.Printf("Register\n")
	var user pgrest.User
	if err := c.BindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fmt.Printf("Registering user %+v", user)

	log.Debugf("looking for existing user %v", user.Email)
	// fetch user by email
	existingUser, err := pgusers.GetOneByEmail(user.Email)
	if existingUser != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User already exists"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user"})
		return
	}
	newOrgID, err := manageOrgCreation(user)
	if err != nil {
		log.Debugf("failed creating organization, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create organization"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	adminGroupName := types.GroupAdmin
	userID := uuid.New().String()
	err = pgusers.New().Upsert(pgrest.User{
		ID:       userID,
		Subject:  fmt.Sprintf("local|%v", userID),
		OrgID:    newOrgID,
		Email:    user.Email,
		Name:     user.Name,
		Status:   "active",
		Password: string(hashedPassword),
		Groups:   []string{adminGroupName},
	})

	if err != nil {
		log.Debugf("failed creating user, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	expirationTime := time.Now().Add(168 * time.Hour) // 7 days
	claims := &Claims{
		UserID:    userID,
		UserEmail: user.Email,
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
		UserID:    userID,
		UserEmail: user.Email,
		Token:     tokenString,
		ExpiresAt: expirationTime.Format(time.RFC3339),
	}
	_, err = pglocalauthsession.CreateSession(newLocalAuthSession)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store session"})
		return
	}

	// TODO: generate token and return it
	c.Header("token", tokenString)

	c.JSON(http.StatusCreated, gin.H{"message": "User created successfully"})
}
