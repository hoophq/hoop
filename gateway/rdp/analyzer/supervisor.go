package analyzer

import (
	"context"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/rdp/ocr"
)

// FlagName is the feature flag that gates the RDP PII detection pipeline.
// When the flag is OFF the supervisor stops the worker pool and the recorder
// must skip enqueueing analysis jobs.
const FlagName = "experimental.rdp_pii_detection"

const supervisorCheckPeriod = 15 * time.Second

var (
	supMu      sync.Mutex
	supCancel  context.CancelFunc
	supRunning bool
)

// RunSupervisor watches the feature flag for orgID and starts/stops the
// worker pool as the flag toggles. It blocks until parentCtx is cancelled,
// so callers should typically invoke it with `go analyzer.RunSupervisor(...)`.
//
// When orgID is empty (multi-tenant gateway with no single control org),
// the supervisor degrades to a one-shot StartWorkerPool: per-org gating is
// then expected to happen at the enqueue site instead.
//
// When analyzerURL or OCR is not available the supervisor exits immediately —
// the pipeline is structurally disabled regardless of the flag.
func RunSupervisor(parentCtx context.Context, analyzerURL, orgID string) {
	if analyzerURL == "" {
		log.Infof("rdp-analyzer: Presidio not configured, supervisor disabled")
		return
	}
	if !ocr.IsAvailable() {
		log.Warnf("rdp-analyzer: tesseract not found in PATH, supervisor disabled")
		return
	}
	if resolveWorkerCount() == 0 {
		log.Infof("rdp-analyzer: RDP_ANALYSIS_WORKERS=0, supervisor disabled")
		return
	}

	if orgID == "" {
		log.Infof("rdp-analyzer: no control orgID provided, starting pool unconditionally")
		StartWorkerPool(parentCtx, analyzerURL)
		return
	}

	syncWithFlag(parentCtx, analyzerURL, orgID)

	ticker := time.NewTicker(supervisorCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-parentCtx.Done():
			stopPool()
			return
		case <-ticker.C:
			syncWithFlag(parentCtx, analyzerURL, orgID)
		}
	}
}

// syncWithFlag reconciles the running state of the worker pool with the
// current value of the feature flag. Safe for concurrent invocation.
func syncWithFlag(parentCtx context.Context, analyzerURL, orgID string) {
	supMu.Lock()
	defer supMu.Unlock()

	enabled := featureflag.IsEnabled(orgID, FlagName)

	switch {
	case enabled && !supRunning:
		ctx, cancel := context.WithCancel(parentCtx)
		supCancel = cancel
		StartWorkerPool(ctx, analyzerURL)
		supRunning = true
		log.Infof("rdp-analyzer: supervisor started worker pool (flag=on)")
	case !enabled && supRunning:
		supCancel()
		supCancel = nil
		supRunning = false
		log.Infof("rdp-analyzer: supervisor stopped worker pool (flag=off)")
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
