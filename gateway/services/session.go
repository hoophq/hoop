package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
)

var (
	ErrMissingMetadata = errors.New("missing required metadata field")
)

func validateMandatoryMetadata(connection *models.Connection, session models.Session) error {
	for _, metadataField := range connection.MandatoryMetadataFields {
		if value, found := session.Metadata[metadataField]; !found || value == nil || value == "" {
			return fmt.Errorf("%w: %s", ErrMissingMetadata, metadataField)
		}
	}
	return nil
}

func ValidateAndUpsertSession(_ context.Context, session models.Session, connection *models.Connection) error {
	if err := validateMandatoryMetadata(connection, session); err != nil {
		return err
	}

	return models.UpsertSession(session)
}
