package services

import (
	"context"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/session/eventbroker"
	"gorm.io/gorm"
)

const (
	// SidecarUnseenAfter ends the sessions of a sidecar that missed five
	// heartbeats (one a minute): it stopped, and its session_end with it.
	SidecarUnseenAfter = 5 * time.Minute

	// SidecarSessionIdleAfter ends a session a restart or a full queue lost
	// the session_end of. Long on purpose: an open psql idles for hours.
	SidecarSessionIdleAfter = 24 * time.Hour

	// sidecarReapBatchSize caps one pass; a backlog drains over several.
	sidecarReapBatchSize = 100
)

// ReapSidecarSessions ends stale sidecar sessions under the lock their batches
// take, and fires their close hooks. A reaped session still takes events.
func ReapSidecarSessions(ctx context.Context, db *gorm.DB, now time.Time) (int, error) {
	st := models.SidecarSessionStaleness{
		SidecarSeenBefore: now.Add(-SidecarUnseenAfter),
		EventBefore:       now.Add(-SidecarSessionIdleAfter),
	}
	stale, err := models.ListStaleSidecarSessions(ctx, db, st, sidecarReapBatchSize)
	if err != nil {
		return 0, fmt.Errorf("listing stale sidecar sessions: %w", err)
	}
	reaped := 0
	for _, s := range stale {
		var ok bool
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := models.LockSidecarSession(tx, s.ID); err != nil {
				return err
			}
			ok, err = models.ReapSidecarSession(tx, s.OrgID, s.ID, st, now)
			if err != nil || !ok {
				return err
			}
			return models.SetSessionMetricsEndedAt(tx, s.ID)
		})
		if err != nil {
			// One bad row must not stop the pass: the next one retries it.
			log.With("sid", s.ID).Warnf("failed reaping sidecar session, reason=%v", err)
			continue
		}
		if !ok {
			continue
		}
		reaped++
		eventbroker.Default.Remove(s.ID)
		runSidecarSessionHooks(sidecarSessionHook{DB: db, OrgID: s.OrgID, SessionID: s.ID, Closed: true, Reaped: true})
	}
	return reaped, nil
}
