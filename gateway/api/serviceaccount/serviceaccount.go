package serviceaccountapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
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
	saItems, err := models.ListServiceAccounts(ctx.OrgID)
	if err != nil {
		log.Errorf("failed listing service accounts, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	var items []openapi.ServiceAccount
	for _, item := range saItems {
		items = append(items, toOpenID(item))
	}
	c.JSON(http.StatusOK, items)
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

	sa := &models.ServiceAccount{
		ID:      genDeterministicUUID(req.Subject),
		OrgID:   ctx.OrgID,
		Subject: req.Subject,
		Name:    req.Name,
		Groups:  req.Groups,
		Status:  string(req.Status),
	}
	err := models.CreateServiceAccount(sa)
	audit.LogFromContextErr(c, audit.ResourceServiceAccount, audit.ActionCreate, sa.ID, sa.Name, payloadServiceAccount(req.Subject, req.Name, req.Groups, string(req.Status)), err)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": models.ErrAlreadyExists.Error()})
	case nil:
		c.JSON(http.StatusCreated, openapi.ServiceAccount{
			ID:      sa.ID,
			OrgID:   sa.OrgID,
			Subject: sa.Subject,
			Name:    sa.Name,
			Groups:  sa.Groups,
			Status:  openapi.ServiceAccountStatusType(sa.Status),
		})
	default:
		log.Errorf("failed creating service account with subject %s, err=%v", req.Subject, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
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
	sa := &models.ServiceAccount{
		OrgID:   ctx.OrgID,
		ID:      genDeterministicUUID(c.Param("subject")),
		Subject: req.Subject,
		Name:    req.Name,
		Groups:  req.Groups,
		Status:  string(req.Status),
	}
	err := models.UpdateServiceAccount(sa)
	audit.LogFromContextErr(c, audit.ResourceServiceAccount, audit.ActionUpdate, sa.ID, sa.Name, payloadServiceAccount(req.Subject, req.Name, req.Groups, string(req.Status)), err)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": models.ErrNotFound.Error()})
	case nil:
		c.JSON(http.StatusOK, openapi.ServiceAccount{
			ID:      sa.ID,
			OrgID:   sa.OrgID,
			Subject: sa.Subject,
			Name:    sa.Name,
			Groups:  sa.Groups,
			Status:  openapi.ServiceAccountStatusType(sa.Status),
		})
	default:
		log.Errorf("failed updating service account with subject %s, err=%v", req.Subject, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

func genDeterministicUUID(subject string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "serviceaccount/%s", subject)).String()
}

func toOpenID(svc models.ServiceAccount) openapi.ServiceAccount {
	return openapi.ServiceAccount{
		ID:      svc.ID,
		OrgID:   svc.OrgID,
		Subject: svc.Subject,
		Name:    svc.Name,
		Status:  openapi.ServiceAccountStatusType(svc.Status),
		Groups:  svc.Groups,
	}
}

func payloadServiceAccount(subject, name string, groups []string, status string) audit.PayloadFn {
	return func() map[string]any {
		return map[string]any{"subject": subject, "name": name, "groups": groups, "status": status}
	}
}
