package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
)

var (
	ErrRequiredMetadata = errors.New("missing required metadata field")
)

func validateRequiredMetadata(connection models.Connection, session models.Session) error {
	for _, metadataField := range connection.MandatoryMetadataFields {
		if value, found := session.Metadata[metadataField]; !found || value == nil || value == "" {
			return fmt.Errorf("%w: %s", ErrRequiredMetadata, metadataField)
		}
	}
	return nil
}

func UpsertSession(_ context.Context, session models.Session, connection models.Connection) error {
	if err := validateRequiredMetadata(connection, session); err != nil {
		return err
	}

	return models.UpsertSession(session)
}
