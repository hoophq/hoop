package memorystreams

import (
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
)

var (
	agentStore  = memory.New()
	clientStore = memory.New()
)

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
