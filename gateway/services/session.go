package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

var (
	ErrMissingMetadata = errors.New("missing required metadata field")
)

func validateMandatoryMetadata(connection models.Connection, session models.Session) error {
	for _, metadataField := range connection.MandatoryMetadataFields {
		if value, found := session.Metadata[metadataField]; !found || value == nil || value == "" {
			return fmt.Errorf("%w: %s", ErrMissingMetadata, metadataField)
		}
	}
	return nil
}

func UpsertSession(_ context.Context, session models.Session, connection models.Connection) error {
	if err := validateMandatoryMetadata(connection, session); err != nil {
		return err
	}

	return models.UpsertSession(session)
}

type SessionAnalyticsInput struct {
	session            *models.Session
	connection         *models.Connection
	guardRailRules     *models.ConnectionGuardRailRules
	accessRequestRules *models.AccessRequestRule
}

type SessionAnalytics struct {
	// Session fields
	OrgID             uuid.UUID
	SessionID         uuid.UUID
	ConnectionType    string
	ConnectionSubtype string
	Status            string
	Verb              string
	CreatedAt         time.Time
	EndSession        *time.Time

	IsAdminUser bool

	// Connection fields
	JiraIssueTemplateActivated bool

	// Agent fields
	AgentVersion string

	// Guardrails
	GuardrailsActivated bool

	// Access request rules
	AccessRequestActivated      bool
	AccessRequestForceApprovals []string
	AccessRequestMinApprovals   *int
}

func GetAnalyticsSnapshot(ctx *storagev2.Context, input SessionAnalyticsInput) (*SessionAnalytics, error) {
	orgID := uuid.MustParse(ctx.OrgID)

	session, err := models.GetSessionByID(orgID.String(), input.session.ID)
	if err != nil {
		return nil, err
	}

	connection, err := models.GetConnectionByNameOrID(ctx, session.Connection)
	if err != nil {
		return nil, err
	}

	guardRailRules, err := models.GetConnectionGuardRailRules(orgID.String(), connection.Name)
	if err != nil {
		return nil, err
	}

	return &SessionAnalytics{
		OrgID:                       orgID,
		IsAdminUser:                 ctx.IsAdminUser(),
		SessionID:                   uuid.MustParse(session.ID),
		ConnectionType:              session.ConnectionType,
		ConnectionSubtype:           session.ConnectionSubtype,
		Status:                      session.Status,
		Verb:                        session.Verb,
		CreatedAt:                   session.CreatedAt,
		EndSession:                  session.EndSession,
		JiraIssueTemplateActivated:  connection.JiraIssueTemplateID.Valid && connection.JiraIssueTemplateID.String != "",
		AgentVersion:                "",
		GuardrailsActivated:         guardRailRules != nil,
		AccessRequestActivated:      false,
		AccessRequestForceApprovals: nil,
		AccessRequestMinApprovals:   nil,
	}, nil
}
