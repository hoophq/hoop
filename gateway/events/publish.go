package events

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// Publish records an event and creates one pending dispatch per matching
// active subscription. Feature-flag gated. Errors are logged but never
// returned to the caller.
func Publish(orgID, eventType string, payload map[string]any, source, producerEventID string) {
	if !featureflag.IsEnabled(orgID, "experimental.event_routing") {
		return
	}

	if _, ok := Catalog[eventType]; !ok {
		log.Warnf("event-routing: unknown event type %q, skipping publish", eventType)
		return
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		log.Warnf("event-routing: failed marshalling payload for %q: %v", eventType, err)
		return
	}

	now := time.Now().UTC()
	event := models.Event{
		ID:        uuid.NewString(),
		OrgID:     orgID,
		EventType: eventType,
		Payload:   payloadJSON,
		OccurredAt: now,
		Source:    source,
		ProducerEventID: sql.NullString{
			String: producerEventID,
			Valid:  producerEventID != "",
		},
		CreatedAt: now,
	}

	if err := models.DB.Transaction(func(tx *gorm.DB) error {
		eventID, isNew, err := models.UpsertEventIdempotent(tx, event)
		if err != nil {
			return err
		}
		if !isNew {
			return nil
		}

		subIDs, err := models.ListActiveSubscriptionIDsForEvent(tx, orgID, eventType)
		if err != nil {
			return err
		}
		if len(subIDs) == 0 {
			return nil
		}

		return models.BulkInsertPendingDispatches(tx, orgID, eventID, subIDs)
	}); err != nil {
		log.Warnf("event-routing: failed publishing %q event: %v", eventType, err)
	}
}
