package eventrouting

import (
	"context"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
)

// RunSupervisor starts the event-routing worker pool, unless the pool is
// disabled via EVENT_ROUTING_WORKERS=0. It returns once the pool has been
// launched; the pool itself runs until parentCtx is cancelled.
func RunSupervisor(parentCtx context.Context) {
	if appconfig.Get().EventRoutingWorkers() == 0 {
		log.Infof("event-routing: EVENT_ROUTING_WORKERS=0, worker pool disabled")
		return
	}
	StartWorkerPool(parentCtx)
}
