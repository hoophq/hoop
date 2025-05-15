package connectionstatus

import (
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/gateway/models"
	streamtypes "github.com/hoophq/hoop/gateway/transport/streamclient/types"
)

func InitConciliationProcess() {
	log.Infof("initializing connection status conciliation process")
	if err := models.UpdateAllAgentsToOffline(); err != nil {
		log.Warnf("failed updating connection and agent resources to offline status, reason=%v", err)
	}
	go func() {
		for {
			for _, obj := range statusStore.List() {
				v, _ := obj.(*stateObject)
				if v == nil || v.grpcConnected {
					continue
				}
				// skip if the state is fresh
				createdAtExtra5s := v.createdAt.Add(time.Second * 5)
				if createdAtExtra5s.After(time.Now().UTC()) {
					continue
				}
				// update the state to offline, didn't received any updates in the last seconds
				err := updateStatus(v.orgID, v.streamID, models.ConnectionStatusOffline, nil)
				if err != nil {
					log.Warnf("failed updating resources to offline status, reason=%v", err)
				} else {
					statusStore.Del(v.streamID.String())
				}
			}
			time.Sleep(conciliationBackoffDuration)
		}
	}()
}

var (
	statusStore                 = memory.New()
	conciliationBackoffDuration = time.Second * 5
)

type stateObject struct {
	orgID         string
	streamID      streamtypes.ID
	createdAt     time.Time
	grpcConnected bool
}

func getState(id streamtypes.ID) (v *stateObject) {
	obj := statusStore.Get(id.String())
	v, _ = obj.(*stateObject)
	return
}

func SetOnlinePreConnect(orgID string, streamAgentID streamtypes.ID) {
	state := getState(streamAgentID)
	// noop if it's grpc connected, it will trigger the offline
	// when it disconnects
	if state != nil && state.grpcConnected {
		return
	}
	if state == nil {
		if err := updateStatus(orgID, streamAgentID, models.ConnectionStatusOnline, nil); err != nil {
			log.Warnf("fail to update the status of stream %v/%v, reason=%v",
				streamAgentID.ResourceID(), streamAgentID.ResourceName(), err)
		}
	}
	statusStore.Set(streamAgentID.String(), &stateObject{
		orgID:         orgID,
		streamID:      streamAgentID,
		createdAt:     time.Now().UTC(),
		grpcConnected: false,
	})
}

func updateStatus(orgID string, streamAgentID streamtypes.ID, status string, metadata map[string]string) (err error) {
	connectionName := streamAgentID.ResourceName()
	if connectionName != "" {
		return models.UpdateConnectionStatusByName(orgID, connectionName, status)
	}
	agentStatus := models.AgentStatusDisconnected
	if status == models.ConnectionStatusOnline {
		agentStatus = models.AgentStatusConnected
	}

	// update the status of the agent all associated connections
	agentID := streamAgentID.ResourceID()
	if err := models.UpdateAgentStatus(orgID, agentID, agentStatus, metadata); err != nil {
		return fmt.Errorf("failed to update agent status, reason=%v", err)
	}
	return nil
}

func SetOnline(orgID string, streamAgentID streamtypes.ID, metadata map[string]string) error {
	err := updateStatus(orgID, streamAgentID, models.ConnectionStatusOnline, metadata)
	if err == nil {
		statusStore.Set(streamAgentID.String(), &stateObject{
			orgID:         orgID,
			streamID:      streamAgentID,
			createdAt:     time.Now().UTC(),
			grpcConnected: true,
		})
	}
	return err
}

func SetOffline(orgID string, streamAgentID streamtypes.ID, metadata map[string]string) error {
	// if an error is found the status will remain online
	// TODO: add attribute to advertise the state is somehow dirty
	err := updateStatus(orgID, streamAgentID, models.ConnectionStatusOffline, metadata)
	if err == nil {
		statusStore.Del(streamAgentID.String())
	}
	return err
}
