package memorystreams

import (
	"context"

	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
)

var (
	agentStore  = memory.New()
	clientStore = memory.New()
)

type Wrapper struct {
	pb.Transport_ConnectServer

	context  context.Context
	cancelFn context.CancelCauseFunc
	OrgID    string
}

func (w Wrapper) Context() context.Context { return w.context }
func (w Wrapper) Disconnect(cause error)   { w.cancelFn(cause) }

func NewWrapperStream(orgID string, s pb.Transport_ConnectServer) Wrapper {
	ctx, cancelFn := context.WithCancelCause(s.Context())
	return Wrapper{s, ctx, cancelFn, orgID}
}

// SetClient add a server stream into the memory based on the session id
func SetClient(sessionID string, s pb.Transport_ConnectServer) { clientStore.Set(sessionID, s) }

// DelClient removes from the client memory store
func DelClient(sessionID string) { clientStore.Del(sessionID) }

// GetClient retrieves a server stream from the session memory store
func GetClient(sessionID string) pb.Transport_ConnectServer {
	obj := clientStore.Get(sessionID)
	stream, ok := obj.(pb.Transport_ConnectServer)
	if !ok {
		return nil
	}
	return stream
}

// DisconnectClient cancel the context from the stream
// and remove the object from the memory store
func DisconnectClient(sessionID string, cause error) {
	defer DelClient(sessionID)
	obj := GetClient(sessionID)
	if w, ok := obj.(Wrapper); ok {
		w.Disconnect(cause)
	}
}

// SetAgent add a server stream into the agent memory store
func SetAgent(agentID string, s pb.Transport_ConnectServer) { agentStore.Set(agentID, s) }

// HasAgent return true if the stream exists in the agent memory store
func HasAgent(agentID string) bool { return GetAgent(agentID) != nil }

// DelAgent removes from the agent memory store
func DelAgent(agentID string) { agentStore.Del(agentID) }

// GetAgent retrieves a server stream from the agent memory store
func GetAgent(agentID string) pb.Transport_ConnectServer {
	obj := agentStore.Get(agentID)
	stream, ok := obj.(pb.Transport_ConnectServer)
	if !ok {
		return nil
	}
	return stream
}

// DisconnectAgent cancel the context from the stream
// and remove the object from the memory store
func DisconnectAgent(agentID string, cause error) {
	defer DelAgent(agentID)
	obj := GetAgent(agentID)
	if w, ok := obj.(Wrapper); ok {
		w.Disconnect(cause)
	}
}

func DisconnectAllAgentsByOrg(orgID string, err error) (count int) {
	for agentID, obj := range agentStore.List() {
		if stream, ok := obj.(Wrapper); ok {
			if stream.OrgID == orgID {
				count++
				DisconnectAgent(agentID, err)
			}
		}
	}
	return
}
