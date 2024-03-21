package connectionrequests

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	"github.com/runopsio/hoop/common/proto"
)

type IsOnlineFn func(agentID string) bool

type Info struct {
	OrgID          string
	AgentID        string
	ConnectionName string
	SID            string
}

func (i *Info) id() string {
	return fmt.Sprintf("%s:%s:%s:%s", i.OrgID, i.AgentID, i.ConnectionName, i.SID)
}

// IsSet report if all attributes are set
func (i *Info) isSet() bool {
	return i.OrgID != "" && i.AgentID != "" &&
		i.ConnectionName != "" && i.SID != ""
}

var (
	requestConnectionStore   = memory.New()
	connectTimeoutDuration   = time.Second * 15
	ErrConnTimeout           = fmt.Errorf("timeout on acquiring proxy connection")
	ErrAlreadyInProgress     = fmt.Errorf("a request is already in progress")
	ErrMissingRequiredFields = fmt.Errorf("missing required attributes requesting proxy connection")
)

// AgentPreConnect synchronize the state of connections in the store
// and return a response indicating if an agent should call the Connect RPC method.
//
// The sync process manages connections that are manageable by this package.
// It enforces by adding an attribute to the created connection (managed_by).
// A cache is held to avoid performing unecessary queries in the store, processes that
// mutate connections should invalidate the cache in case of mutations.
//
// The response has two outcomes:
//
// CONNECT: indicates the agent should call the Connect RPC method
//
// BACKOFF: indicate a backoff which may contain a error message with the reason
// returned when there are connection requests and also if the sync was performed with success.
//
// This function also release the proxy connections if there's an agent online or
// if the synchronize process returns with an error. The error is sent to the all clients
// waiting for a response
func AgentPreConnect(orgID, agentID string, req *proto.PreConnectRequest, isAgentOnlineFn IsOnlineFn) *proto.PreConnectResponse {
	// sync the connection with the store
	var syncErr error
	if req.Name != "" {
		syncErr = connectionSync(orgID, agentID, req)
	}

	// only release it if has an agent online or it returned error from sync process
	if isAgentOnlineFn(agentID) || syncErr != nil {
		_ = AcceptProxyConnection(orgID, agentID, syncErr)
	}

	// if there's pending connection requests, allow an agent to connect
	hasConnectionRequests := len(connectionRequestItems(orgID, agentID)) > 0
	if hasConnectionRequests && syncErr == nil {
		return &proto.PreConnectResponse{
			Status:  proto.PreConnectStatusConnectType,
			Message: "",
		}
	}

	// backoff and report the error if there's any pending connection
	if syncErr == nil {
		syncErr = fmt.Errorf("")
	}
	return &proto.PreConnectResponse{
		Status:  proto.PreConnectStatusBackoffType,
		Message: syncErr.Error(),
	}
}

func connectionRequestItems(orgID, agentID string) map[string]any {
	keyPrefix := fmt.Sprintf("%s:%s", orgID, agentID)
	return requestConnectionStore.Filter(func(k string) bool {
		return strings.HasPrefix(k, keyPrefix)
	})
}

// AcceptProxyConnection release all connections performed for this organization and agent
//
// When an agent performs a connection, it could call this function to release connection requests
func AcceptProxyConnection(orgID, agentID string, err error) (v bool) {
	conectionRequests := connectionRequestItems(orgID, agentID)
	for key := range conectionRequests {
		obj := requestConnectionStore.Pop(key)
		if requestCh, _ := obj.(chan error); requestCh != nil {
			timeout, cancelFn := context.WithTimeout(context.Background(), time.Second*2)
			defer cancelFn()
			select {
			case <-timeout.Done():
				log.Warnf("timeout releasing proxy connection, orgid=%v, agentid=%v", orgID, agentID)
			case requestCh <- err:
			}
		}
	}
	return len(conectionRequests) > 0
}

// RequestProxyConnection request for a connection with an agent.
// It should be called when an agent is offline.
//
// The AcceptProxyConnection function is used to release the request if
// there's an agent available to connect
func RequestProxyConnection(info Info) (err error) {
	if !info.isSet() {
		return ErrMissingRequiredFields
	}
	if requestConnectionStore.Get(info.id()) != nil {
		return ErrAlreadyInProgress
	}
	requestCh := make(chan error)
	requestConnectionStore.Set(info.id(), requestCh)
	timeout, cancelFn := context.WithTimeout(context.Background(), connectTimeoutDuration)
	defer func() { cancelFn(); close(requestCh) }()
	select {
	case <-timeout.Done():
		requestConnectionStore.Del(info.id())
		return ErrConnTimeout
	case err = <-requestCh:
		return
	}
}
