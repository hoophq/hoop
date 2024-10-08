package signupapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/agentcontroller"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

// Signup
//
//	@Summary		Signup
//	@Description	Signup anonymous authenticated user. This endpoint is only used for multi tenant setups.
//	@Tags			Authentication
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.SignupRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.SignupRequest
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/signup [post]
func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if !ctx.IsAnonymous() || !appconfig.Get().OrgMultitenant() {
		c.JSON(http.StatusConflict, gin.H{"message": "user already signed up"})
		return
	}
	var req openapi.SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	_, signingKey := appconfig.Get().LicenseSigningKey()
	if signingKey == nil {
		log.Errorf("unable to sign license: missing license private key")
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to sign license"})
		return
	}
	lic, err := license.Sign(
		signingKey,
		license.EnterpriseType,
		fmt.Sprintf("multi tenant customer: %v", req.OrgName),
		[]string{"*.hoop.dev"},
		(time.Hour*8760)*20, // 20 years
	)
	if err != nil {
		log.Errorf("unable to sign license: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to sign license"})
		return
	}
	licenseDataJSONBytes, err := json.Marshal(lic)
	if err != nil {
		log.Errorf("unable to encode license to json: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to sign license (json encoding)"})
		return
	}
	orgID, err := pgorgs.New().CreateOrGetOrg(req.OrgName, licenseDataJSONBytes)
	switch err {
	case pgusers.ErrOrgAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "organization name is already claimed"})
	case nil:
		agentcontroller.Sync()
		profileName := ctx.UserAnonProfile
		if len(req.ProfileName) > 0 {
			profileName = req.ProfileName
		}
		profilePicture := ctx.UserAnonPicture
		if len(req.ProfilePicture) > 0 {
			profilePicture = req.ProfilePicture
		}
		user := models.User{
			ID:       uuid.NewString(),
			OrgID:    orgID,
			Subject:  ctx.UserAnonSubject,
			Name:     profileName,
			Picture:  profilePicture,
			Email:    ctx.UserAnonEmail,
			Verified: true,
			Status:   "active",
			SlackID:  "",
			// Groups:   []string{types.GroupAdmin},
		}
		if err := models.UpdateUser(&user); err != nil {
			log.Errorf("failed creating user, err=%v", err)
			sentry.CaptureException(err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating user"})
			return
		}
		adminGroup := models.UserGroup{
			OrgID:  orgID,
			UserID: user.ID,
			Name:   types.GroupAdmin,
		}
		if err := models.InsertUserGroups([]models.UserGroup{adminGroup}); err != nil {
			log.Errorf("failed creating user group, err=%v", err)
			sentry.CaptureException(err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating user group"})
			return
		}

		log.With("org_name", req.OrgName, "org_id", orgID).Infof("user signup up with success")
		identifySignup(user, req.OrgName, c.GetHeader("user-agent"), ctx.ApiURL)
		c.JSON(http.StatusOK, openapi.SignupRequest{
			OrgID:          orgID,
			OrgName:        req.OrgName,
			ProfileName:    profileName,
			ProfilePicture: profilePicture,
		})
	default:
		log.Errorf("failed creating organization, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating organization"})
	}
}

func identifySignup(u models.User, orgName, userAgent, apiURL string) {
	client := analytics.New()
	client.Identify(&types.APIContext{
		OrgID:     u.OrgID,
		OrgName:   orgName,
		UserID:    u.Email, // use user id as email
		UserName:  u.Name,
		UserEmail: u.Email,
	})
	go func() {
		// wait some time until the identify call get times to reach to intercom
		time.Sleep(time.Second * 10)
		client.Track(u.Email, analytics.EventSignup, map[string]any{
			"user-agent": userAgent,
			"name":       u.Name,
			"api-url":    apiURL,
		})
	}()
}
