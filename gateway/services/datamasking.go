package services

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
)

func GetDataMaskingRulesForConnection(orgID, connectionName string) (json.RawMessage, error) {
	parsedOrgID := uuid.MustParse(orgID)
	attributes, err := models.GetConnectionAttributes(models.DB, parsedOrgID, connectionName)
	if err != nil {
		return nil, err
	}

	if len(attributes) > 0 {
		return models.GetDataMaskingEntityTypesByAttributes(models.DB, parsedOrgID, attributes)
	}

	return models.GetDataMaskingEntityTypes(orgID, connectionName)
}
