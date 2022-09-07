package transport

import (
	pb "github.com/runopsio/hoop/domain/proto"
	"io"
	"log"
)

type Server struct {
	pb.UnimplementedTransportServer
}

func (s Server) Connect(srv pb.Transport_ConnectServer) error {
	log.Println("connecting grpc server")
	ctx := srv.Context()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// receive data from stream
		req, err := srv.Recv()
		if err == io.EOF {
			// return will close stream from server side
			log.Println("exit")
			return nil
		}
		if err != nil {
			log.Printf("receive error %v", err)
			continue
		}

		// continue if number reveived from stream
		// less than max
		log.Printf("receive request type: %s", req.Type)

		// update max and send it to stream
		resp := pb.Packet{
			Component: "server",
			Type:      "generic-response",
			Spec:      make(map[string][]byte),
			Payload:   []byte("payload as bytes"),
		}

		if err := srv.Send(&resp); err != nil {
			log.Printf("send error %v", err)
		}
	}
}
