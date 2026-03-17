package services

import (
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
)

func GetGuardrailsRulesForConnection(orgID, connectionName string) (*models.ConnectionGuardRailRules, error) {
	parsedOrgID := uuid.MustParse(orgID)
	attributes, err := models.GetConnectionAttributes(models.DB, parsedOrgID, connectionName)
	if err != nil {
		return nil, err
	}

	rules, err := models.GetConnectionGuardRailRulesByConnectionAndAttribute(models.DB, parsedOrgID, connectionName, attributes)
	if err != nil {
		return nil, err
	}
	rules.Name = connectionName

	return rules, nil
}
