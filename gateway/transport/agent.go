package transport

import (
	agent2 "github.com/runopsio/hoop/gateway/agent"
	pb "github.com/runopsio/hoop/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
	"log"
	"sync"
	"time"
)

type (
	connectedAgents struct {
		agents map[string]pb.Transport_ConnectServer
		mu     sync.Mutex
	}
)

var ca = connectedAgents{
	agents: make(map[string]pb.Transport_ConnectServer),
	mu:     sync.Mutex{},
}

func (ca *connectedAgents) bind(agentId string, stream pb.Transport_ConnectServer) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	ca.agents[agentId] = stream
}

func (ca *connectedAgents) unbind(agentId string) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	delete(ca.agents, agentId)
}

func (ca *connectedAgents) getAgentStream(id string) pb.Transport_ConnectServer {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	return ca.agents[id]
}

func (s *Server) subscribeAgent(stream pb.Transport_ConnectServer, token string) error {
	ctx := stream.Context()
	md, _ := metadata.FromIncomingContext(ctx)

	hostname := extractData(md, "hostname")
	machineId := extractData(md, "machine_id")
	kernelVersion := extractData(md, "kernel_version")

	agent, err := s.AgentService.FindByToken(token)
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

	ca.bind(token, stream)

	log.Printf("successful connection hostname: [%s], machineId [%s], kernelVersion [%s]", hostname, machineId, kernelVersion)

	done := make(chan bool)
	go s.listenAgentMessages(agent, stream, done)

	<-done
	return nil
}

func (s *Server) listenAgentMessages(agent *agent2.Agent, stream pb.Transport_ConnectServer, done chan bool) {
	ctx := stream.Context()

	// keep alive msg
	go func() {
		for {
			proto := &pb.Packet{
				Component: pb.PacketGatewayComponent,
				Type:      pb.PacketKeepAliveType,
			}
			log.Println("sending keep alive command")
			if err := stream.Send(proto); err != nil {
				if err != nil {
					log.Printf("failed sending keep alive command, err=%v", err)
					break
				}
			}
			time.Sleep(time.Second * 10)
		}
	}()

	for {
		log.Println("start of iteration")
		select {
		case <-ctx.Done():
			log.Println("agent disconnected...")
			ca.unbind(agent.Token)
			agent.Status = agent2.StatusDisconnected
			s.AgentService.Persist(agent)
			done <- true
		default:
		}

		// receive data from stream
		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("agent disconnected...")
			ca.unbind(agent.Token)
			agent.Status = agent2.StatusDisconnected
			s.AgentService.Persist(agent)
			done <- true
		}
		if err != nil {
			log.Printf("received error %v", err)
			continue
		}

		log.Printf("receive request type [%s] and component [%s]", req.Type, req.Component)

		// find original client and send response back

		//resp := pb.Packet{
		//	Component: "server",
		//	Type:      req.Type,
		//	Spec:      make(map[string][]byte),
		//	Payload:   []byte("payload as bytes"),
		//}
		//
		//go func(stream pb.Transport_ConnectServer) {
		//	log.Printf("sending response type [%s] and component [%s]", resp.Type, resp.Component)
		//	if err := stream.Send(&resp); err != nil {
		//		log.Printf("send error %v", err)
		//	}
		//}(stream)
	}
}
