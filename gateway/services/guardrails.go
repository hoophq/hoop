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

	if len(attributes) == 0 {
		return models.GetConnectionGuardRailRules(orgID, connectionName)
	}

	rules, err := models.GetConnectionGuardRailRulesByAttribute(models.DB, parsedOrgID, attributes)
	if err != nil {
		return nil, err
	}
	rules.Name = connectionName

	return rules, nil
}
