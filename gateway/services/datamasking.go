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

	return models.GetDataMaskingEntityTypesByConnectionAndAttributes(models.DB, parsedOrgID, connectionName, attributes)
}
