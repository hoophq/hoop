package services

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
)

func GetDataMaskingRulesForConnection(orgID, connectionName string) (json.RawMessage, error) {
	parsedOrgID, err := uuid.Parse(orgID)
	if err != nil {
		return nil, fmt.Errorf("invalid organization ID %q: %w", orgID, err)
	}
	attributes, err := models.GetConnectionAttributes(models.DB, parsedOrgID, connectionName)
	if err != nil {
		return nil, err
	}

	return models.GetDataMaskingEntityTypesByConnectionAndAttributes(models.DB, parsedOrgID, connectionName, attributes)
}
