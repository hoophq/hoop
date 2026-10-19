package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hoophq/hoop/gateway/events"
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

// ValidateAndUpsertSession validates mandatory metadata and persists the session row.
// Use this for subsequent updates to an existing session (status transitions, JIT
// approvals, etc). For the initial creation of a session use CreateSession so the
// session.started derived event is emitted exactly once.
func ValidateAndUpsertSession(_ context.Context, session models.Session, connection *models.Connection) error {
	if err := validateMandatoryMetadata(connection, session); err != nil {
		return err
	}

	return models.UpsertSession(session)
}

// CreateSession validates mandatory metadata, persists a brand-new session row, and
// publishes the session.started derived event. It is the single chokepoint for new
// API-originated sessions (webterminal, REST exec, runbooks, provision). The non-API
// origins (CLI, agent, proxy-manager) continue to publish session.started from the
// audit plugin's OnConnect, which is the only place those flows insert the session
// row.
func CreateSession(_ context.Context, session models.Session, connection *models.Connection) error {
	if err := validateMandatoryMetadata(connection, session); err != nil {
		return err
	}
	if err := models.UpsertSession(session); err != nil {
		return err
	}
	events.DeriveFromSessionStart(session.OrgID, &session, connection)
	return nil
}

// refusedSessionExitCode is the code the audit plugin records for a session
// that ends on a gateway error.
const refusedSessionExitCode = 254

// CloseRefusedSession closes the open session an API exec wrote before the
// transport refused its stream, and pairs the session.started that
// CreateSession published with a session.closed.
func CloseRefusedSession(orgID, userID, connectionName, sessionID, reason string) error {
	closed, err := models.CloseRefusedSession(models.DB, orgID, userID, connectionName, sessionID,
		refusedSessionExitCode, reason, time.Now().UTC())
	if err != nil || !closed {
		return err
	}
	sess, err := models.GetSessionByID(orgID, sessionID)
	if err != nil {
		return fmt.Errorf("session closed, but loading it for its closed event failed: %w", err)
	}
	events.DeriveFromSessionEnd(orgID, sess)
	return nil
}
