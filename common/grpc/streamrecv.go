package grpc

import (
	pb "github.com/runopsio/hoop/common/proto"
)

type DataStream struct {
	pkt *pb.Packet
	err error
}

func (s *DataStream) Recv() (*pb.Packet, error) { return s.pkt, s.err }

// NewStreamRecv starts the stream.Recv() in background.
// and send the messages through the dataStream channel
//
// It blocks until it receives a message into m or the stream is
// done. It returns io.EOF when the client has performed a CloseSend. On
// any non-EOF error, the stream is aborted and the error contains the
// RPC status.
//
// It is safe to have a goroutine calling SendMsg and another goroutine
// calling RecvMsg on the same stream at the same time, but it is not
// safe to call RecvMsg on the same stream in different goroutines.
func NewStreamRecv(stream pb.ClientReceiver) chan *DataStream {
	ch := make(chan *DataStream)
	go func() {
		defer close(ch)
		for {
			pkt, err := stream.Recv()
			ch <- &DataStream{pkt, err}
			if err != nil {
				break
			}
		}
	}()
	return ch
}
