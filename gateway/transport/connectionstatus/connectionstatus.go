package connectionstatus

import (
	"fmt"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
	pgconnections "github.com/runopsio/hoop/gateway/pgrest/connections"
	streamtypes "github.com/runopsio/hoop/gateway/transport/streamclient/types"
)

func InitConciliationProcess() {
	log.Infof("initializing connection status conciliation process")
	updateResourcesToOffline()
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
				err := updateStatus(pgrest.NewOrgContext(v.orgID), v.streamID, pgrest.ConnectionStatusOffline, nil)
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

func updateResourcesToOffline() {
	if err := pgagents.New().UpdateAllToOffline(); err != nil {
		log.Warnf("failed to update agent resources to offline status, reason=%v")
	}
	if err := pgconnections.New().UpdateAllToOffline(); err != nil {
		log.Warnf("failed to update connection resources to offline status, reason=%v")
	}
}

func getState(id streamtypes.ID) (v *stateObject) {
	obj := statusStore.Get(id.String())
	v, _ = obj.(*stateObject)
	return
}

func SetOnlinePreConnect(ctx pgrest.OrgContext, streamAgentID streamtypes.ID) {
	state := getState(streamAgentID)
	// noop if it's grpc connected, it will trigger the offline
	// when the it disconnects
	if state != nil && state.grpcConnected {
		return
	}
	if state == nil {
		if err := updateStatus(ctx, streamAgentID, pgrest.ConnectionStatusOnline, nil); err != nil {
			log.Warnf("fail to update the status of stream %v/%v, reason=%v",
				streamAgentID.ResourceID(), streamAgentID.ResourceName(), err)
		}
	}
	statusStore.Set(streamAgentID.String(), &stateObject{
		orgID:         ctx.GetOrgID(),
		streamID:      streamAgentID,
		createdAt:     time.Now().UTC(),
		grpcConnected: false,
	})
}

func updateStatus(ctx pgrest.OrgContext, streamAgentID streamtypes.ID, status string, metadata map[string]string) (err error) {
	connectionName := streamAgentID.ResourceName()
	if connectionName != "" {
		return pgconnections.New().UpdateStatusByName(ctx, connectionName, status)
	}
	// update the status of the agent resource
	agentID := streamAgentID.ResourceID()
	agentStatus := pgrest.AgentStatusDisconnected
	if status == pgrest.ConnectionStatusOnline {
		agentStatus = pgrest.AgentStatusConnected
	}
	if err = pgagents.New().UpdateStatus(ctx, agentID, agentStatus, metadata); err != nil {
		return fmt.Errorf("failed to update agent status, reason=%v", err)
	}
	// update the status of all connections that belongs to this agent id
	return pgconnections.New().UpdateStatusByAgentID(ctx, agentID, status)
}

func SetOnline(ctx pgrest.OrgContext, streamAgentID streamtypes.ID, metadata map[string]string) error {
	err := updateStatus(ctx, streamAgentID, pgrest.ConnectionStatusOnline, metadata)
	if err == nil {
		statusStore.Set(streamAgentID.String(), &stateObject{
			orgID:         ctx.GetOrgID(),
			streamID:      streamAgentID,
			createdAt:     time.Now().UTC(),
			grpcConnected: true,
		})
	}
	return err
}

func SetOffline(ctx pgrest.OrgContext, streamAgentID streamtypes.ID, metadata map[string]string) error {
	// if an error is found the status will remain online
	// TODO: add attribute to advertise the state is somehow dirty
	err := updateStatus(ctx, streamAgentID, pgrest.ConnectionStatusOffline, metadata)
	if err == nil {
		statusStore.Del(streamAgentID.String())
	}
	return err
}
