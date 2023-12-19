package apiconnectionapps

import (
	"fmt"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgagents "github.com/runopsio/hoop/gateway/pgrest/agents"
	"github.com/runopsio/hoop/gateway/storagev2"
)

type IsConnectedFn func(connectionName string) bool

var (
	timeoutDuration             = time.Second * 30
	ErrRequestConnectionTimeout = fmt.Errorf("timeout (%vs) on acquiring connection", timeoutDuration.Seconds())
	connectionRequestStore      = memory.New()
	agentUpdateStore            = memory.New()
	intervalUpdateAgentStatus   = time.Minute * 2
)

// RequestGrpcConnection will store a request to connect in the gateway via gRPC.
// A timeout (25s) error will occour if a connection was not established or
// it couldn't validate the isConnectedFn for 5 seconds.
//
// isConnectedFn must be a function that validates when the agent establishes the gRPC connection.
func RequestGrpcConnection(agentID string, isConnectedFn IsConnectedFn) error {
	requestCh := make(chan struct{})
	connectionRequestStore.Set(agentID, requestCh)
	// remove the request from the memory store and close the channel
	defer func() { connectionRequestStore.Del(agentID); close(requestCh) }()
	select {
	case <-requestCh:
		// wait for the agent to connect via gRPC validating via isConnectedFn
		for i := 1; ; i++ {
			if isConnectedFn(agentID) {
				return nil
			}
			if i == 5 {
				return fmt.Errorf("fail to establish a connection with the remote agent")
			}
			time.Sleep(time.Millisecond * 1300)
		}
	case <-time.After(timeoutDuration):
		return ErrRequestConnectionTimeout
	}
}

// requestConnectionOK lookup for a connection request in the memory and notify
// the request channel that it sent the grpc connection request
func requestGrpcConnectionOK(agentID string) bool {
	obj := connectionRequestStore.Pop(agentID)
	if obj == nil {
		return false
	}
	if requestCh, _ := obj.(chan struct{}); requestCh != nil {
		select {
		case requestCh <- struct{}{}:
			return true
		case <-time.After(time.Second * 2):
			log.Warnf("channel %v is busy, timeout (2s)", agentID)
		}
	}
	return false
}

// updateAgentStatus will update the agent status to CONNECTED
// in a 5 minutes interval
func updateAgentStatus(orgID, agentName string) {
	if !pgrest.Rollout {
		return
	}

	key := fmt.Sprintf("%s:%s", orgID, agentName)
	now := time.Now().UTC()
	t1, _ := agentUpdateStore.Get(key).(time.Time)
	if t1.IsZero() || t1.Add(intervalUpdateAgentStatus).Before(now) {
		agentUpdateStore.Del(key)
		orgCtx := storagev2.NewOrganizationContext(orgID, nil)
		agent, err := pgagents.New().FetchOneByNameOrID(orgCtx, agentName)
		if err != nil || agent == nil {
			log.Warnf("failed fetching agent %v, err=%v", agentName, err)
			return
		}
		// if the agent already has the platform metadata,
		// it means that it's already connected via gRPC
		if agent.GetMeta("platform") != "" {
			agentUpdateStore.Set(key, now)
			return
		}
		log.With("org", orgID).Debugf("sync agent embedded status %v", agentName)
		agent.Status = "CONNECTED"
		agent.UpdatedAt = func() *string { t := now.Format(time.RFC3339Nano); return &t }()
		if err := pgagents.New().Upsert(agent); err != nil {
			log.With("org", orgID).Warnf("failed updating agent status for %v, err=%v", agentName, err)
			return
		}
		agentUpdateStore.Set(key, now)
		return
	}
}
