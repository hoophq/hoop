package signupapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/agentcontroller"
	"github.com/hoophq/hoop/gateway/analytics"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

// Signup
//
//	@Summary		OIDC | Signup
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, fmt.Errorf("missing license private key"), "unable to sign license")
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "unable to sign license")
		return
	}
	licenseDataJSONBytes, err := json.Marshal(lic)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "unable to sign license (json encoding)")
		return
	}

	org, _, err := models.CreateOrgGetOrganization(req.OrgName, licenseDataJSONBytes)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating organization")
		return
	}

	if org.TotalUsers > 0 {
		c.JSON(http.StatusConflict, gin.H{"message": "organization name is already claimed"})
		return
	}

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
		OrgID:    org.ID,
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating user")
		return
	}
	adminGroup := models.UserGroup{
		OrgID:  org.ID,
		UserID: user.ID,
		Name:   types.GroupAdmin,
	}
	if err := models.InsertUserGroups([]models.UserGroup{adminGroup}); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating user group")
		return
	}
	// add default system tags
	_ = models.UpsertBatchConnectionTags(apiconnections.DefaultConnectionTags(org.ID))

	log.With("org_name", req.OrgName, "org_id", org.ID).Infof("user signup up with success")
	identifySignup(user, c.GetHeader("user-agent"), c.Request.Host, ctx.GetLicenseType(), licenseDataJSONBytes)
	c.JSON(http.StatusOK, openapi.SignupRequest{
		OrgID:          org.ID,
		OrgName:        req.OrgName,
		ProfileName:    profileName,
		ProfilePicture: profilePicture,
	})
}

func identifySignup(u models.User, userAgent, host, licenseType string, licenseData json.RawMessage) {
	trackClient := analytics.New()
	trackClient.Identify(&types.APIContext{
		OrgID:          u.OrgID,
		OrgLicenseData: &licenseData,
		UserID:         u.Subject,
		UserEmail:      u.Email,
		UserName:       u.Name,
	})
	go func() {
		// wait some time until the identify call get times to reach to intercom
		time.Sleep(time.Second * 10)
		trackClient.Track(u.Subject, analytics.EventSignup, map[string]any{
			"user-agent":   userAgent,
			"org-id":       u.OrgID,
			"api-hostname": host,
			"license-type": licenseType,
		})
		trackClient.Close()
	}()
}
