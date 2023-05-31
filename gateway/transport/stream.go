package transport

import (
	"context"
	"time"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
)

type dataStream struct {
	pkt *pb.Packet
	err error
}

// newDataStreamCh starts the stream.Recv() in background.
// In case it fails to deliver data, it will cancel the given context
//
// It blocks until it receives a message into m or the stream is
// done. It returns io.EOF when the client has performed a CloseSend. On
// any non-EOF error, the stream is aborted and the error contains the
// RPC status.
//
// It is safe to have a goroutine calling SendMsg and another goroutine
// calling RecvMsg on the same stream at the same time, but it is not
// safe to call RecvMsg on the same stream in different goroutines.
func newDataStreamCh(stream pb.Transport_ConnectServer, cancelFn context.CancelFunc) chan *dataStream {
	ch := make(chan *dataStream)
	go func() {
		defer close(ch)
		for {
			pkt, err := stream.Recv()
			select {
			case ch <- &dataStream{pkt, err}:
				if err == nil {
					continue
				}
			case <-time.After(time.Second * 1):
				var pktType string
				if pkt != nil {
					pktType = pkt.Type
				}
				log.Debugf("timeout (1s) processing packet %v, err=%v", pktType, err)
				cancelFn()
			}
			break
		}
	}()
	return ch
}
