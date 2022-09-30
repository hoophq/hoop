package main

import (
	"fmt"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/api"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	xtdb "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/user"
	pb "github.com/runopsio/hoop/proto"
	"github.com/runopsio/hoop/proto/version"
	"os"
)

func main() {
	fmt.Println(string(version.JSON()))
	s := &xtdb.Storage{}
	err := s.Connect()
	if err != nil {
		panic(err)
	}

	agentService := agent.Service{Storage: &agent.Storage{Storage: s}}
	connectionService := connection.Service{Storage: &connection.Storage{Storage: s}}
	userService := user.Service{Storage: &user.Storage{Storage: s}}
	clientService := client.Service{Storage: &client.Storage{Storage: s}}

	a := &api.Api{
		AgentHandler:      agent.Handler{Service: &agentService},
		ConnectionHandler: connection.Handler{Service: &connectionService},
		UserHandler:       user.Handler{Service: &userService},
	}

	g := &transport.Server{
		AgentService:      agentService,
		ConnectionService: connectionService,
		UserService:       userService,
		ClientService:     clientService,
	}

	profile := os.Getenv("PROFILE")
	if profile == pb.DevProfile {
		api.PROFILE = pb.DevProfile

		err = a.CreateTrialEntities()
		if err != nil {
			panic(err)
		}
	} else {
		api.DownloadAuthPublicKey()
	}

	go g.StartRPCServer()
	a.StartAPI()
}
