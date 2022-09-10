package transport

import (
	pb "github.com/runopsio/hoop/domain/proto"
	agent2 "github.com/runopsio/hoop/gateway/agent"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
	"log"
)

func (s *Server) subscribeAgent(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	hostname := extractData(md, "hostname")
	machineId := extractData(md, "machine_id")
	kernelVersion := extractData(md, "kernel_version")

	agent, err := s.AgentService.FindOne(token)
	if err != nil || agent == nil {
		return status.Errorf(codes.Unauthenticated, "invalid authentication")
	}

	agent.Hostname = hostname
	agent.MachineId = machineId
	agent.KernelVersion = kernelVersion
	agent.Status = agent2.StatusConnected

	_, err = s.AgentService.Persist(agent)
	if err != nil {
		return status.Errorf(codes.Internal, "internal error")
	}

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)
	return s.listenMessages(agent, stream)
}

func (s *Server) listenMessages(agent *agent2.Agent, stream pb.Transport_ConnectServer) error {
	ctx := stream.Context()

	for {
		log.Println("start of iteration")
		select {
		case <-ctx.Done():
			log.Println("agent disconnected...")
			agent.Status = agent2.StatusDisconnected
			s.AgentService.Persist(agent)
			return ctx.Err()
		default:
		}

		// receive data from stream
		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("agent disconnected...")
			agent.Status = agent2.StatusDisconnected
			s.AgentService.Persist(agent)
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

		go func(stream pb.Transport_ConnectServer) {
			log.Printf("sending response type [%s] and component [%s]", resp.Type, resp.Component)
			if err := stream.Send(&resp); err != nil {
				log.Printf("send error %v", err)
			}
		}(stream)
	}
}

func extractData(md metadata.MD, metaName string) string {
	data := md.Get(metaName)
	if len(data) == 0 {
		return ""
	}

	return data[0]
}
