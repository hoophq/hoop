package serviceaccountapi

import (
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pgserviceaccounts "github.com/runopsio/hoop/gateway/pgrest/serviceaccounts"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type ServiceAccountRequest struct {
	Subject string                         `json:"subject"`
	Name    string                         `json:"name"`
	Status  types.ServiceAccountStatusType `json:"status"`
	Groups  []string                       `json:"groups"`
}

func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	serviceAccountList, err := pgserviceaccounts.New().FetchAll(ctx)
	if err != nil {
		sentry.CaptureException(err)
		log.Errorf("failed listing service accounts, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, serviceAccountList)
}

func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req ServiceAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Subject == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "subject is empty"})
		return
	}
	if req.Status != "active" && req.Status != "inactive" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("wrong status value %q", req.Status)})
		return
	}
	objID := genDeterministicUUID(req.Subject)
	svcAccount, err := pgserviceaccounts.New().FetchOne(ctx, objID)
	if err != nil {
		sentry.CaptureException(err)
		log.Errorf("failed retrieving service account entity, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if svcAccount != nil {
		c.JSON(http.StatusConflict, gin.H{"message": "service account already exists"})
		return
	}

	obj := &types.ServiceAccount{
		ID:      objID,
		Subject: req.Subject,
		OrgID:   ctx.OrgID,
		Name:    req.Name,
		Status:  types.ServiceAccountStatusActive,
		Groups:  req.Groups,
	}
	if _, err := pgserviceaccounts.New().Upsert(ctx, obj); err != nil {
		sentry.CaptureException(err)
		log.Errorf("failed creating service account with subject %s, err=%v", req.Subject, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, obj)
}

func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req ServiceAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Status != "active" && req.Status != "inactive" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("wrong status value %q", req.Status)})
		return
	}
	objID := genDeterministicUUID(c.Param("subject"))
	svcAccount, err := pgserviceaccounts.New().FetchOne(ctx, objID)
	if err != nil {
		sentry.CaptureException(err)
		log.Errorf("failed retrieving service account entity, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	if svcAccount == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "service account not found"})
		return
	}

	svcAccount.Name = req.Name
	svcAccount.Status = req.Status
	svcAccount.Groups = req.Groups
	if _, err := pgserviceaccounts.New().Upsert(ctx, svcAccount); err != nil {
		sentry.CaptureException(err)
		log.Errorf("failed updating service account with subject %s, err=%v", req.Subject, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, svcAccount)
}

func genDeterministicUUID(subject string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("serviceaccount/%s", subject))).String()
}
