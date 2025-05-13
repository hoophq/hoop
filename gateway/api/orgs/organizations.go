package orgs

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

//
func ListUserOrganizations(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	
	userOrgs, err := models.ListUserOrganizations(ctx.UserID)
	if err != nil {
		log.Errorf("failed listing user organizations, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	
	var result []openapi.Organization
	for _, userOrg := range userOrgs {
		org, err := models.GetOrganizationByNameOrID(userOrg.OrgID)
		if err != nil {
			continue
		}
		
		result = append(result, openapi.Organization{
			Id:   org.ID,
			Name: org.Name,
			Role: userOrg.Role,
		})
	}
	
	c.JSON(http.StatusOK, result)
}

//
func GetActiveOrganization(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	
	org, err := models.GetOrganizationByNameOrID(ctx.OrgID)
	if err != nil {
		log.Errorf("failed getting active organization, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	
	userOrgs, err := models.ListUserOrganizations(ctx.UserID)
	if err == nil {
		for _, userOrg := range userOrgs {
			if userOrg.OrgID == org.ID {
				c.JSON(http.StatusOK, openapi.Organization{
					Id:   org.ID,
					Name: org.Name,
					Role: userOrg.Role,
				})
				return
			}
		}
	}
	
	c.JSON(http.StatusOK, openapi.Organization{
		Id:   org.ID,
		Name: org.Name,
	})
}

//
func SetActiveOrganization(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	
	var req openapi.SetActiveOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	
	err := models.SetActiveOrganization(ctx.UserID, req.OrganizationId)
	if err != nil {
		log.Errorf("failed setting active organization, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	
	org, err := models.GetOrganizationByNameOrID(req.OrganizationId)
	if err != nil {
		log.Errorf("failed getting organization, reason=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	
	userOrgs, err := models.ListUserOrganizations(ctx.UserID)
	if err == nil {
		for _, userOrg := range userOrgs {
			if userOrg.OrgID == org.ID {
				c.JSON(http.StatusOK, openapi.Organization{
					Id:   org.ID,
					Name: org.Name,
					Role: userOrg.Role,
				})
				return
			}
		}
	}
	
	c.JSON(http.StatusOK, openapi.Organization{
		Id:   org.ID,
		Name: org.Name,
	})
}
