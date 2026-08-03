package controller

import (
	"encoding/json"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
)

const (
	logShipInterval  = 2 * time.Second
	logShipBatchSize = 500
)

// runLogShipper periodically ships new entries from the process log tail to
// the gateway as ClientAgentLogs packets. It only ships entries logged after
// the shipper starts, so reconnects don't replay the whole buffer.
func (a *Agent) runLogShipper() {
	lastSeq := log.Tail.LatestSeq()
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
		entries, seq := log.Tail.Since(lastSeq)
		if len(entries) == 0 {
			continue
		}
		if len(entries) > logShipBatchSize {
			// tail semantics: keep the newest entries, drop the rest
			entries = entries[len(entries)-logShipBatchSize:]
		}
		lastSeq = seq
		payload, err := json.Marshal(entries)
		if err != nil {
			continue
		}
		if err := a.client.Send(&pb.Packet{Type: pbclient.AgentLogs, Payload: payload}); err != nil {
			// Debug: captured only under LOG_LEVEL=debug, where a failed
			// send adds at most one entry per tick — bounded, transient.
			log.Debugf("failed shipping agent logs to gateway, reason=%v", err)
		}
	}
}
