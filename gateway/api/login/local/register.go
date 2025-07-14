package loginlocalapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/crypto/bcrypt"
)

func Register(c *gin.Context) {
	var user User
	if err := c.ShouldBindJSON(&user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	log.Debugf("looking for existing user %v", user.Email)
	// fetch user by email
	existingUser, err := models.GetUserByEmail(user.Email)
	if err != nil {
		log.Errorf("failed fetching user by email %s, reason=%v", user.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching user by email, reason=" + err.Error()})
		return
	}
	if existingUser != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "user already exists"})
		return
	}

	// local auth creates the user at the default organization for now.
	// we plan to make it much more permissive, but at this moment this
	// limitation comes to make sure things are working as expected.
	org, err := models.GetOrganizationByNameOrID(proto.DefaultOrgName)
	if err != nil {
		log.Debugf("failed fetching default organization, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to fetch default organization"})
		return
	}
	// if there is one user already, do not allow new users to be created
	// it avoids a security issue of anyone being able to add themselves to
	// the default organization. Instead, they should get an invitation
	if org.TotalUsers > 0 {
		log.Warnf("unable to register new users, main user already exists")
		c.JSON(http.StatusForbidden, gin.H{"message": "unable to register new users, please contact your administrator"})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed to hash password"})
		return
	}

	tokenString, err := generateNewAccessToken(user.Email, user.Email)
	if err != nil {
		log.Errorf("failed generating access token, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed generating token"})
		return
	}

	adminGroupName := types.GroupAdmin
	userID := uuid.New().String()

	err = models.CreateUser(models.User{
		ID:             userID,
		Subject:        user.Email,
		OrgID:          org.ID,
		Email:          user.Email,
		Name:           user.Name,
		Status:         "active",
		Verified:       true,
		HashedPassword: string(hashedPassword),
	})

	if err != nil {
		log.Debugf("failed creating user, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Failed to create user"})
		return
	}

	adminUserGroup := models.UserGroup{
		OrgID:  org.ID,
		UserID: userID,
		Name:   adminGroupName,
	}
	trackClient := analytics.New()
	trackClient.Identify(&types.APIContext{
		OrgID:           org.ID,
		OrgName:         org.Name,
		UserName:        user.Name,
		UserID:          user.Email,
		UserAnonSubject: org.ID,
		UserEmail:       user.Email,
		UserGroups:      []string{adminGroupName},
		ApiURL:          appconfig.Get().ApiURL(),
	})
	trackClient.Track(user.Email, analytics.EventSingleTenantFirstUserCreated, nil)

	err = models.InsertUserGroups([]models.UserGroup{adminUserGroup})
	if err != nil {
		log.Errorf("failed creating user group, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Failed to create user group"})
		return
	}

	// add default system tags
	_ = models.UpsertBatchConnectionTags(apiconnections.DefaultConnectionTags(org.ID))

	c.Header("Access-Control-Expose-Headers", "Token")
	c.Header("Token", tokenString)

	c.JSON(http.StatusCreated, gin.H{"message": "User created successfully"})
}
