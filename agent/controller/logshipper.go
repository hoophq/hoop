package controller

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

const (
	logShipInterval  = 2 * time.Second
	logShipBatchSize = 500
)

// shipPending accumulates this process's log entries between shipper ticks;
// each tick drains and sends it. Bounded at logShipBatchSize (newest kept —
// tail semantics). The observer is registered once, on the first controller
// run, and is shared across reconnects (runDefaultMode recreates the
// controller). The ring storage lives on the gateway only.
var (
	shipMu           sync.Mutex
	shipPending      []log.TailEntry
	shipObserverOnce sync.Once
)

func shipObserver(e log.TailEntry) {
	shipMu.Lock()
	defer shipMu.Unlock()
	shipPending = append(shipPending, e)
	if len(shipPending) > logShipBatchSize {
		shipPending = append([]log.TailEntry(nil), shipPending[len(shipPending)-logShipBatchSize:]...)
	}
}

func drainPending() []log.TailEntry {
	shipMu.Lock()
	defer shipMu.Unlock()
	entries := shipPending
	shipPending = nil
	return entries
}

// runLogShipper periodically ships this process's new log entries to the
// gateway as ClientAgentLogs packets.
func (a *Agent) runLogShipper() {
	shipObserverOnce.Do(func() { log.TailObserve(shipObserver) })
	// Ship only entries logged after this connection is established: discard
	// anything buffered while disconnected instead of replaying it.
	drainPending()
	ticker := time.NewTicker(logShipInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.shutdownCtx.Done():
			return
		case <-a.client.StreamContext().Done():
			return
		case <-ticker.C:
		}
		entries := drainPending()
		if len(entries) == 0 {
			continue
		}
		payload, err := json.Marshal(entries)
		if err != nil {
			continue
		}
		if err := a.client.Send(&pb.Packet{Type: pbclient.AgentLogs, Payload: payload}); err != nil {
			// Debug: below the default capture floor; under LOG_LEVEL=debug a
			// failed send adds at most one entry per tick — bounded, transient.
			log.Debugf("failed shipping agent logs to gateway, reason=%v", err)
		}
	}
}
