package eventrouting

import (
	"context"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
)

const FlagName = "experimental.event_routing"

const supervisorCheckPeriod = 15 * time.Second

var (
	supMu      sync.Mutex
	supCancel  context.CancelFunc
	supRunning bool
)

// RunSupervisor watches the feature flag for orgID and starts/stops the
// worker pool as the flag toggles. It blocks until parentCtx is cancelled.
//
// When orgID is empty (multi-tenant gateway) the supervisor starts the pool
// unconditionally; per-org gating then happens at the publish site.
func RunSupervisor(parentCtx context.Context, orgID string) {
	if appconfig.Get().EventRoutingWorkers() == 0 {
		log.Infof("event-routing: EVENT_ROUTING_WORKERS=0, supervisor disabled")
		return
	}

	if orgID == "" {
		log.Infof("event-routing: no control orgID, starting pool unconditionally")
		StartWorkerPool(parentCtx)
		return
	}

	syncWithFlag(parentCtx, orgID)

	ticker := time.NewTicker(supervisorCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-parentCtx.Done():
			stopPool()
			return
		case <-ticker.C:
			syncWithFlag(parentCtx, orgID)
		}
	}
}

func syncWithFlag(parentCtx context.Context, orgID string) {
	supMu.Lock()
	defer supMu.Unlock()

	enabled := featureflag.IsEnabled(orgID, FlagName)

	switch {
	case enabled && !supRunning:
		ctx, cancel := context.WithCancel(parentCtx)
		supCancel = cancel
		StartWorkerPool(ctx)
		supRunning = true
		log.Infof("event-routing: supervisor started worker pool (flag=on)")
	case !enabled && supRunning:
		supCancel()
		supCancel = nil
		supRunning = false
		log.Infof("event-routing: supervisor stopped worker pool (flag=off)")
	}
}

func stopPool() {
	supMu.Lock()
	defer supMu.Unlock()
	if supCancel != nil {
		supCancel()
		supCancel = nil
	}
	supRunning = false
}
