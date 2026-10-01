package services

import (
	"github.com/hoophq/hoop/gateway/models"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// GetServiceIdentityContextByID loads an active API key or AI agent context.
func GetServiceIdentityContextByID(db *gorm.DB, orgID, id string) (*models.Context, error) {
	apiKey, err := models.GetAPIKeyByID(db, orgID, id)
	if err != nil {
		return nil, err
	}
	var name, status string
	var groups pq.StringArray
	if apiKey != nil {
		name, status, groups = apiKey.Name, apiKey.Status, apiKey.Groups
	} else {
		aiAgent, err := models.GetAIAgentByID(db, orgID, id)
		if err != nil {
			return nil, err
		}
		if aiAgent == nil {
			return nil, nil
		}
		name, status, groups = aiAgent.Name, aiAgent.Status, aiAgent.Groups
	}
	if status != "active" {
		return nil, nil
	}
	org, err := models.GetOrganizationByID(db, orgID)
	if err != nil {
		return nil, err
	}
	return &models.Context{
		OrgID:          org.ID,
		OrgName:        org.Name,
		OrgLicenseData: org.LicenseData,
		UserID:         id,
		UserSubject:    id,
		UserName:       name,
		UserEmail:      name,
		UserStatus:     "active",
		UserGroups:     groups,
	}, nil
}
