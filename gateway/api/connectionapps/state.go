package apiconnectionapps

import (
	"fmt"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
)

type IsConnectedFn func(connectionName string) bool

var timeoutDuration = time.Second * 30
var ErrRequestConnectionTimeout = fmt.Errorf("timeout (%vs) on acquiring connection", timeoutDuration.Seconds())
var connectionRequestStore = memory.New()

// RequestGrpcConnection will notify the agent (connectionName) to connect in the gateway via gRPC.
// A timeout (25s) error will occour if a connection was not established or
// it couldn't validate the isConnectedFn for 5 seconds
//
// isConnectedFn must be a function that validates when the agent establishes the gRPC connection.
func RequestGrpcConnection(connectionName string, isConnectedFn IsConnectedFn) error {
	requestCh := make(chan struct{})
	connectionRequestStore.Set(connectionName, requestCh)
	// remove the request from the memory store and close the channel
	defer func() { connectionRequestStore.Del(connectionName); close(requestCh) }()
	select {
	case <-requestCh:
		// wait for the agent to connect via gRPC validating via isConnectedFn
		for i := 1; ; i++ {
			if isConnectedFn(connectionName) {
				return nil
			}
			if i == 5 {
				return fmt.Errorf("fail to establish a connection with the remote agent")
			}
			time.Sleep(time.Second * 1)
		}
	case <-time.After(timeoutDuration):
		return ErrRequestConnectionTimeout
	}
}

// requestConnectionOK lookup for a connection request in the memory and notify
// the request channel that it sent the grpc connection request
func requestGrpcConnectionOK(connectionName string) bool {
	obj := connectionRequestStore.Pop(connectionName)
	if obj == nil {
		return false
	}
	if requestCh, _ := obj.(chan struct{}); requestCh != nil {
		select {
		case requestCh <- struct{}{}:
			return true
		case <-time.After(time.Second * 2):
			log.Warnf("channel %v is busy, timeout (2s)", connectionName)
		}
	}
	return false
}
