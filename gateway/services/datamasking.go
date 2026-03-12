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

	// var connID string
	// err = models.DB.Raw(`
	// 	SELECT id FROM private.connections
	// 	WHERE org_id = ? AND name = ?`, orgID, connectionName).
	// 	First(&connID).Error
	// if err != nil {
	// 	return nil, err
	// }

	return models.GetDataMaskingEntityTypes(orgID, connectionName)
}
