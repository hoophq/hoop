package eventrouting

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
)

const pollInterval = 2 * time.Second

func StartWorkerPool(ctx context.Context) {
	n := appconfig.Get().EventRoutingWorkers()
	if n == 0 {
		return
	}

	if orphaned, err := models.MarkOrphanedDispatchesFailed(models.DB); err != nil {
		log.Warnf("event-routing: failed marking orphaned dispatches: %v", err)
	} else if orphaned > 0 {
		log.Infof("event-routing: marked %d orphaned dispatches as failed", orphaned)
	}

	log.Infof("event-routing: starting %d workers", n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, workerID)
		}(i)
	}

	go func() {
		wg.Wait()
		log.Infof("event-routing: all workers stopped")
	}()
}

func runWorker(ctx context.Context, workerID int) {
	logger := log.With("worker", fmt.Sprintf("event-routing-%d", workerID))
	logger.Infof("worker started")

	for {
		select {
		case <-ctx.Done():
			logger.Infof("worker stopping (context cancelled)")
			return
		default:
		}

		dispatch, err := models.ClaimNextDispatch(models.DB)
		if err != nil {
			logger.Errorf("failed to claim dispatch: %v", err)
			sleep(ctx, pollInterval)
			continue
		}

		if dispatch == nil {
			sleep(ctx, pollInterval)
			continue
		}

		logger.With("dispatch_id", dispatch.ID, "event_id", dispatch.EventID).
			Infof("processing dispatch")

		if err := processDispatch(ctx, dispatch); err != nil {
			errMsg := err.Error()
			if len(errMsg) > 1000 {
				errMsg = errMsg[:1000]
			}
			logger.With("dispatch_id", dispatch.ID).Warnf("dispatch failed: %s", errMsg)
			_ = models.MarkDispatchFailed(models.DB, dispatch.ID, errMsg)
			continue
		}

		_ = models.MarkDispatchDelivered(models.DB, dispatch.ID)
		logger.With("dispatch_id", dispatch.ID).Infof("dispatch delivered")
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
