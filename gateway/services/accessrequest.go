package services

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

func GetRuleForConnection(orgID uuid.UUID, connectionName, accessType string) (*models.AccessRequestRule, error) {
	connectionAttributes, err := models.GetConnectionAttributes(models.DB, orgID, connectionName)
	if err != nil {
		return nil, fmt.Errorf("failed fetching connection attributes: %s", err)
	}

	if len(connectionAttributes) > 0 {
		rule, err := models.GetRequestRuleByAttributesAndAccessType(models.DB, orgID, connectionAttributes, accessType)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed fetching access request rules: %s", err)
		}

		if rule != nil {
			return rule, nil
		}
	}

	rule, err := models.GetAccessRequestRuleByResourceNameAndAccessType(models.DB, orgID, connectionName, accessType)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed fetching access request rule: %s", err)
	}

	return rule, nil
}

func GetRulesForConnection(orgID uuid.UUID, connectionName string) ([]models.AccessRequestRule, error) {
	connectionAttributes, err := models.GetConnectionAttributes(models.DB, orgID, connectionName)
	if err != nil {
		return nil, fmt.Errorf("failed fetching connection attributes: %s", err)
	}

	if len(connectionAttributes) > 0 {
		rules, err := models.GetRequestRulesByAttributes(models.DB, orgID, connectionAttributes)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed fetching access request rules: %s", err)
		}

		if len(rules) > 0 {
			return rules, nil
		}
	}

	rules, err := models.GetAccessRequestRuleByResourceName(models.DB, orgID, connectionName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed fetching access request rule: %s", err)
	}

	return rules, nil
}
