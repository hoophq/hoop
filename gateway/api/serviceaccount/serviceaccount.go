package serviceaccountapi

import (
	"fmt"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	pgserviceaccounts "github.com/hoophq/hoop/gateway/pgrest/serviceaccounts"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// ListServiceAccounts
//
//	@Summary		List Service Accounts
//	@Description	List all service accounts
//	@Tags			User Management
//	@Produce		json
//	@Success		200	{array}		openapi.ServiceAccount
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/serviceaccounts [get]
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

// CreateServiceAccount
//
//	@Summary		Create Service Account
//	@Description	Create a service account that is associated with a identity provider client
//	@Tags			User Management
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.ServiceAccount	true	"The request body resource"
//	@Success		201				{object}	openapi.ServiceAccount
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/serviceaccounts [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ServiceAccount
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

	obj := &openapi.ServiceAccount{
		ID:      objID,
		Subject: req.Subject,
		OrgID:   ctx.OrgID,
		Name:    req.Name,
		Status:  req.Status,
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

// UpdateServiceAccount
//
//	@Summary		Update Service Account
//	@Description	Update a service account that is associated with a identity provider client
//	@Tags			User Management
//	@Accept			json
//	@Produce		json
//	@Param			subject			path		string					true	"The subject identifier of the service account"
//	@Param			request			body		openapi.ServiceAccount	true	"The request body resource"
//	@Success		200				{object}	openapi.ServiceAccount
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/serviceaccounts/{subject} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.ServiceAccount
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
	c.JSON(http.StatusOK, svcAccount)
}

func genDeterministicUUID(subject string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("serviceaccount/%s", subject))).String()
}
