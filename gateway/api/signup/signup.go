package signupapi

import (
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
)

type SignupRequest struct {
	OrgName string `json:"organization_name" binding:"required,alphanum,min=3,max=30"`
}

func Post(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	if !ctx.IsAnonymous() || !user.IsOrgMultiTenant() {
		c.JSON(http.StatusConflict, gin.H{"message": "user already signed up"})
		return
	}
	var req SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	orgID, err := pgusers.New().CreateOrGetOrg(req.OrgName)
	switch err {
	case pgusers.ErrOrgAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "organization name is already claimed"})
	case nil:
		user := pgrest.User{
			ID:       uuid.NewString(),
			OrgID:    orgID,
			Subject:  ctx.UserAnonSubject,
			Name:     ctx.UserAnonProfile,
			Email:    ctx.UserAnonEmail,
			Verified: true,
			Status:   "active",
			SlackID:  "",
			Groups:   []string{types.GroupAdmin},
		}
		if err := pgusers.New().Upsert(user); err != nil {
			log.Errorf("failed creating user, err=%v", err)
			sentry.CaptureException(err)
			c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating user"})
			return
		}
		identifySignup(user, req.OrgName, c.GetHeader("user-agent"), ctx.ApiURL)
		c.JSON(http.StatusOK, gin.H{"org_id": orgID, "name": req.OrgName})
	default:
		log.Errorf("failed creating organization, err=%v", err)
		sentry.CaptureException(err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating organization"})
	}
}

func identifySignup(u pgrest.User, orgName, userAgent, apiURL string) {
	client := analytics.New()
	client.Identify(&types.APIContext{
		OrgID:      u.OrgID,
		OrgName:    orgName,
		UserID:     u.Email, // use user id as email
		UserName:   u.Name,
		UserEmail:  u.Email,
		UserGroups: u.Groups,
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
