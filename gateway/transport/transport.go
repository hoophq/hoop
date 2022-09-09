package transport

import (
	pb "github.com/runopsio/hoop/domain/proto"
	"github.com/runopsio/hoop/gateway/domain"
	xtdb "github.com/runopsio/hoop/gateway/storage"
	"io"
	"log"
	"strconv"
	"time"
)

func NewGrpcServer() *Server {
	return &Server{
		storage: &xtdb.Storage{},
	}
}

type (
	Server struct {
		pb.UnimplementedTransportServer
		storage storage
	}

	storage interface {
		PersistAgent(agent *domain.Agent) (int64, error)
		GetAgents(context *domain.Context) ([]domain.Agent, error)
	}
)

func (s Server) Connect(stream pb.Transport_ConnectServer) error {
	log.Println("connecting grpc server")
	ctx := stream.Context()

	for {
		log.Println("start of iteration")
		select {
		case <-ctx.Done():
			log.Println("received DONE")
			return ctx.Err()
		default:
		}

		// receive data from stream
		req, err := stream.Recv()
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
		if seconds == 8 {
			return ctx.Err()
		}

		go func(stream pb.Transport_ConnectServer) {
			time.Sleep(time.Millisecond * 1000 * time.Duration(seconds))
			log.Printf("sending response type [%s] and component [%s]", resp.Type, resp.Component)
			if err := stream.Send(&resp); err != nil {
				log.Printf("send error %v", err)
			}
		}(stream)
	}
}
