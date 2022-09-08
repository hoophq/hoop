package transport

import (
	pb "github.com/runopsio/hoop/domain/proto"
	"io"
	"log"
	"strconv"
	"time"
)

type Server struct {
	pb.UnimplementedTransportServer
}

func (s Server) Connect(srv pb.Transport_ConnectServer) error {
	log.Println("connecting grpc server")
	ctx := srv.Context()

	for {
		log.Println("start of iteration")
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// receive data from stream
		req, err := srv.Recv()
		if err == io.EOF {
			// return will close stream from server side
			log.Println("EOF sent: exit")
			return nil
		}
		if err != nil {
			log.Printf("received error %v", err)
			continue
		}

		log.Printf("receive request type [%s] and component [%s]", req.Type, req.Component)

		// update max and send it to stream
		resp := pb.Packet{
			Component: "server",
			Type:      req.Type,
			Spec:      make(map[string][]byte),
			Payload:   []byte("payload as bytes"),
		}

		seconds, _ := strconv.Atoi(req.Type)
		time.Sleep(time.Millisecond * 1000 * time.Duration(seconds))
		log.Printf("sending response type [%s] and component [%s]", resp.Type, resp.Component)
		if err := srv.Send(&resp); err != nil {
			log.Printf("send error %v", err)
		}
	}
}
