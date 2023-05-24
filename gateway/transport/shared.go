package transport

import (
	"fmt"
	"sync"
	"time"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type autoConnectStream struct {
	stream pb.Transport_ConnectServer
	ch     chan any
}

var (
	stateLock        sync.RWMutex
	autoConnectState = map[string]any{}
)

func addStateEntry(state map[string]any, key string, val any) {
	stateLock.Lock()
	defer stateLock.Unlock()
	state[key] = val
}

func getStateEntry(state map[string]any, key string) any {
	stateLock.Lock()
	defer stateLock.Unlock()
	val, ok := state[key]
	if !ok {
		return nil
	}
	return val
}

func DispatchSubscribe(ac *types.AutoConnect) error {
	obj := getStateEntry(autoConnectState, ac.ID)
	if obj == nil {
		return fmt.Errorf("client %s not found in memory", ac.ID)
	}
	log.Infof("starting dispatcher")
	if disp, _ := obj.(*sharedDispatcher); disp != nil {
		select {
		case disp.apiResponseCh <- ac.RequestConnectionName:
		case <-time.After(time.Second * 5):
			return fmt.Errorf("timeout (5s) sending request connection")
		}

		select {
		case subscribeErr := <-disp.subscribeResponseCh:
			return subscribeErr
		case <-time.After(time.Second * 10):
			return fmt.Errorf("timeout (5s) subscribing")
		}

		// err := stream.Send(&pb.Packet{
		// 	Type: pbclient.DoSubscribe,
		// 	Spec: map[string][]byte{
		// 		pb.SpecAgentConnectionParamsKey: []byte{},
		// 	},
		// })
		// if err != nil {
		// 	return fmt.Errorf("failed sending subscribe packet to client, reason=%v", err)
		// }
	}
	return fmt.Errorf("failed type cast chan string, type=%T", obj)
}
