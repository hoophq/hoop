package gateway

import (
	"fmt"
	"github.com/runopsio/hoop/gateway/plugin"
	"os"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/api"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	xtdb "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/user"
)

func Run() {
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
	pluginService := plugin.Service{Storage: &plugin.Storage{Storage: s}}

	a := &api.Api{
		AgentHandler:      agent.Handler{Service: &agentService},
		ConnectionHandler: connection.Handler{Service: &connectionService},
		UserHandler:       user.Handler{Service: &userService},
		PluginHandler:     plugin.Handler{Service: &pluginService},
	}

	g := &transport.Server{
		AgentService:      agentService,
		ConnectionService: connectionService,
		UserService:       userService,
		ClientService:     clientService,
		PluginService:     pluginService,
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
