package services

import (
	"errors"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/require"
)

func TestValidateMandatoryMetadata(t *testing.T) {
	tests := []struct {
		name        string
		connection  *models.Connection
		session     models.Session
		expectedErr error
	}{
		{
			name: "All mandatory fields satisfied",
			connection: &models.Connection{
				MandatoryMetadataFields: []string{"field1", "field2"},
			},
			session: models.Session{
				Metadata: map[string]any{
					"field1": "value1",
					"field2": "value2",
				},
			},
			expectedErr: nil,
		},
		{
			name: "Missing mandatory field",
			connection: &models.Connection{
				MandatoryMetadataFields: []string{"field1", "field2"},
			},
			session: models.Session{
				Metadata: map[string]any{
					"field1": "value1",
				},
			},
			expectedErr: errors.New("missing required metadata field: field2"),
		},
		{
			name: "Mandatory field is nil",
			connection: &models.Connection{
				MandatoryMetadataFields: []string{"field1"},
			},
			session: models.Session{
				Metadata: map[string]any{
					"field1": nil,
				},
			},
			expectedErr: errors.New("missing required metadata field: field1"),
		},
		{
			name: "Mandatory field is empty string",
			connection: &models.Connection{
				MandatoryMetadataFields: []string{"field1"},
			},
			session: models.Session{
				Metadata: map[string]any{
					"field1": "",
				},
			},
			expectedErr: errors.New("missing required metadata field: field1"),
		},
		{
			name: "No mandatory fields in connection",
			connection: &models.Connection{
				MandatoryMetadataFields: []string{},
			},
			session: models.Session{
				Metadata: map[string]any{
					"field1": "value1",
				},
			},
			expectedErr: nil,
		},
		{
			name: "Mandatory field not found in metadata",
			connection: &models.Connection{
				MandatoryMetadataFields: []string{"field1"},
			},
			session: models.Session{
				Metadata: map[string]any{},
			},
			expectedErr: errors.New("missing required metadata field: field1"),
		},
		{
			name: "Multiple missing mandatory fields",
			connection: &models.Connection{
				MandatoryMetadataFields: []string{"field1", "field2", "field3"},
			},
			session: models.Session{
				Metadata: map[string]any{
					"field1": "value1",
				},
			},
			expectedErr: errors.New("missing required metadata field: field2"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateMandatoryMetadata(test.connection, test.session)
			if (err != nil && test.expectedErr == nil) || (err == nil && test.expectedErr != nil) {
				t.Errorf("Expected error: %v, got: %v", test.expectedErr, err)
			}

			if err != nil && test.expectedErr != nil && err.Error() != test.expectedErr.Error() {
				require.Equal(t, test.expectedErr.Error(), err.Error(), "Expected error message: %v, got: %v", test.expectedErr.Error(), err.Error())
			}
		})
	}
}
