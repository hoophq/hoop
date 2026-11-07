// Package sidecarsessionreaper ends the sidecar sessions that stop receiving
// events. A sidecar that stops, restarts or drops its queue never sends their
// session_end, and nothing else would close them.
package sidecarsessionreaper

import (
	"context"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"gorm.io/gorm"
)

const (
	// reapInterval is how often a replica looks. Staleness is minutes, so
	// a minute late is noise.
	reapInterval = time.Minute

	// reapTimeout bounds one pass below the interval, so a wedged pass is
	// cut loose before the next one is due.
	reapTimeout = 50 * time.Second
)

// Run reaps every reapInterval. The first pass waits SidecarUnseenAfter: after
// a gateway outage every sidecar reads as unseen until its next heartbeat.
// Each replica ticks; one replica at a time runs the pass.
func Run(ctx context.Context, db *gorm.DB) {
	grace := time.NewTimer(services.SidecarUnseenAfter)
	defer grace.Stop()
	select {
	case <-ctx.Done():
		return
	case <-grace.C:
	}

	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		reap(ctx, db)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func reap(ctx context.Context, db *gorm.DB) {
	ctx, cancel := context.WithTimeout(ctx, reapTimeout)
	defer cancel()
	var n int
	_, err := models.RunOnOneReplica(ctx, db, "sidecar-session-reaper", func(ctx context.Context) error {
		var err error
		n, err = services.ReapSidecarSessions(ctx, db, time.Now().UTC())
		return err
	})
	if err != nil {
		log.Errorf("failed reaping sidecar sessions, reason=%v", err)
		return
	}
	if n > 0 {
		log.Infof("ended %v sidecar session(s) that stopped receiving events", n)
	}
}
