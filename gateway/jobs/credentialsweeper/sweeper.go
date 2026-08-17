// Package credentialsweeper finalises the audit session of connection
// credentials whose access window has elapsed.
//
// The work used to run inline, as the first statement of GET /api/sessions,
// GET /api/sessions/{id} and the two credential POST handlers. That made a
// read mutate, made a read scoped to one org close sessions for every tenant
// on the deployment, and put an unbounded 1+2N serial statement loop on a
// user-facing path. Moving it here changes when the work happens and who pays
// for it, not what it does.
//
// The trade is deliberate: a session used to flip to done the moment somebody,
// anywhere on the deployment, hit one of those endpoints — often, but entirely
// arbitrary, and never at all for an org with no UI traffic. It is now bounded
// by sweepInterval for everyone. Anything that reads status = 'done' inherits
// that bound.
package credentialsweeper

import (
	"context"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

const (
	// sweepInterval bounds how long an expired credential's session keeps
	// reporting open. The webapp derives the credential's own expiry from
	// metadata.credentials_expire_at rather than from the session status, so
	// this only governs the status column and the session-details live-stream
	// check (verb=connect AND status=open). Fifteen seconds keeps that window
	// short enough that a user opening the detail modal right after expiry
	// does not subscribe to a stream that will never emit.
	sweepInterval = 15 * time.Second

	// sweepTimeout bounds a single tick. Set below sweepInterval so a wedged
	// sweep is cut loose before the next one is due, keeping at most one in
	// flight without needing a mutex.
	sweepTimeout = 10 * time.Second

	// sweepBatchSize caps the rows one tick may take. A backlog drains over
	// several ticks instead of in a single long transaction, which is what
	// pins the xmin horizon and starves autovacuum. At this interval the
	// ceiling is 120k credentials/hour, far above any real backlog.
	sweepBatchSize = 500
)

// Run sweeps once immediately, then every sweepInterval until ctx is done.
// The immediate pass drains whatever expired while the gateway was down.
//
// Every replica runs its own ticker. The model query takes its rows with
// FOR UPDATE SKIP LOCKED, so replicas take disjoint slices instead of
// contending; the work is duplicated-and-skipped rather than leader-elected,
// which is the right cost at this size.
func Run(ctx context.Context, db *gorm.DB) {
	sweep(ctx, db)

	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(ctx, db)
		}
	}
}

func sweep(ctx context.Context, db *gorm.DB) {
	// One tick may not outlive its interval: a sweep wedged on a lock or a
	// stalled connection would otherwise block the loop forever, silently
	// stopping all cleanup. The batch is small enough that hitting this
	// timeout means something is wrong, not that the work was too big.
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	// Logged rather than swallowed: the previous implementation discarded
	// every per-row error with `_ =`, so a row that could never be updated
	// was retried forever with no signal. A permanently failing sweep now
	// shows up in the logs on every tick.
	swept, err := models.CloseExpiredCredentialSessions(ctx, db, sweepBatchSize)
	if err != nil {
		log.Errorf("failed closing expired credential sessions, reason=%v", err)
		return
	}
	if swept > 0 {
		log.Infof("closed %v expired credential session(s)", swept)
	}
}
