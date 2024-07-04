package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

var (
	dispatcherStateLock sync.RWMutex
	dipatcherStateMap   = map[string]*dispatcherState{}
	ErrForceReconnect   = errors.New("timeout (500ms) dispatching open session, forcing client to reconnect")
	ErrUnsupportedType  = errors.New("connection type is not supported")
)

type openSessionResponse struct {
	obj *pb.Packet
	err error
}

type dispatcherState struct {
	requestCh  chan *types.Client
	responseCh chan openSessionResponse
	cancelFn   context.CancelFunc
}

func newDispatcherState(cancelFn context.CancelFunc) *dispatcherState {
	return &dispatcherState{
		make(chan *types.Client),
		make(chan openSessionResponse),
		cancelFn,
	}
}

func addDispatcherStateEntry(key string, val *dispatcherState) {
	dispatcherStateLock.Lock()
	defer dispatcherStateLock.Unlock()
	dipatcherStateMap[key] = val
}

func getDispatcherState(key string) *dispatcherState {
	dispatcherStateLock.Lock()
	defer dispatcherStateLock.Unlock()
	val, ok := dipatcherStateMap[key]
	if !ok {
		return nil
	}
	return val
}

func removeDispatcherState(key string) {
	dispatcherStateLock.Lock()
	defer dispatcherStateLock.Unlock()
	delete(dipatcherStateMap, key)
}

// sendResponse back to who's listening the response channel.
// It will make the function DispatchOpenSession
// to return if someone is calling it and waiting for a response.
func (d *dispatcherState) sendResponse(pkt *pb.Packet, err error) {
	select {
	case d.responseCh <- openSessionResponse{pkt, err}:
	case <-time.After(time.Second * 2): // hang if doesn't send in 2 seconds
		log.Warnf("timeout (2s) sending response back to api client")
	}
}

// DispatchOpenSession will trigger the open session phase logic when calling this function.
// It will wait until it receives a response or timeout.
//
// A proxy manager client needs to be connected for this function to work properly
func DispatchOpenSession(req *types.Client) (*pb.Packet, error) {
	state := getDispatcherState(req.ID)
	if state == nil {
		return nil, fmt.Errorf("proxy manager state %s not found", req.ID)
	}
	// it will trigger the open session phase logic
	select {
	case state.requestCh <- req:
	case <-time.After(time.Millisecond * 500):
		// the channel is closed or busy, cancel the underline context and
		// indicate the caller that it's safe to reconnect it.
		log.Info(ErrForceReconnect)
		state.cancelFn()
		return nil, ErrForceReconnect
	}

	// then wait for the response
	select {
	case resp := <-state.responseCh:
		return resp.obj, resp.err
	case <-time.After(time.Second * 10):
		return nil, fmt.Errorf("timeout (10s) waiting to open a session")
	}
}

// DispatchDisconnect cancel the context of the stateful connection
// forcing the client to disconnect
func DispatchDisconnect(req *types.Client) error {
	state := getDispatcherState(req.ID)
	if state == nil {
		return fmt.Errorf("proxy manager state %s not found", req.ID)
	}
	removeDispatcherState(req.ID)
	state.cancelFn()
	return nil
}
