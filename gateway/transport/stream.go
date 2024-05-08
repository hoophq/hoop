package transport

import (
	"context"
	"time"

	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
)

type streamWrapper struct {
	pb.Transport_ConnectServer
	cancelCtx context.Context
	cancelFn  context.CancelCauseFunc
	orgID     string
}

func newStreamWrapper(stream pb.Transport_ConnectServer, orgID string) streamWrapper {
	ctx, cancelFn := context.WithCancelCause(stream.Context())
	return streamWrapper{
		Transport_ConnectServer: stream,
		cancelCtx:               ctx,
		cancelFn:                cancelFn,
		orgID:                   orgID,
	}
}

func (w streamWrapper) Context() context.Context { return w.cancelCtx }
func (w streamWrapper) Disconnect(cause error)   { w.cancelFn(cause) }

type dataStream struct {
	pkt *pb.Packet
	err error
}

func (s *dataStream) Recv() (*pb.Packet, error) { return s.pkt, s.err }

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
