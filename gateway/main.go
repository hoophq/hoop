package gateway

import (
	"fmt"
	"os"

	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/security"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"

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
	if err := s.Connect(); err != nil {
		panic(err)
	}

	transport.LoadPlugins()

	profile := os.Getenv("PROFILE")
	idProvider := idp.NewProvider(profile)

	agentService := agent.Service{Storage: &agent.Storage{Storage: s}}
	pluginService := plugin.Service{Storage: &plugin.Storage{Storage: s}}
	connectionService := connection.Service{PluginService: &pluginService, Storage: &connection.Storage{Storage: s}}
	userService := user.Service{Storage: &user.Storage{Storage: s}}
	clientService := client.Service{Storage: &client.Storage{Storage: s}}
	sessionService := session.Service{Storage: &session.Storage{Storage: s}}
	securityService := security.Service{
		Storage:     &security.Storage{Storage: s},
		Provider:    idProvider,
		UserService: &userService}

	a := &api.Api{
		AgentHandler:      agent.Handler{Service: &agentService},
		ConnectionHandler: connection.Handler{Service: &connectionService},
		UserHandler:       user.Handler{Service: &userService},
		PluginHandler:     plugin.Handler{Service: &pluginService},
		SessionHandler:    session.Handler{Service: &sessionService},
		SecurityHandler:   security.Handler{Service: &securityService},
		IDProvider:        idProvider,
		Profile:           profile,
	}

	g := &transport.Server{
		AgentService:      agentService,
		ConnectionService: connectionService,
		UserService:       userService,
		ClientService:     clientService,
		PluginService:     pluginService,
		SessionService:    sessionService,
		IDProvider:        idProvider,
		Profile:           profile,
	}

	if profile == pb.DevProfile {
		if err := a.CreateTrialEntities(); err != nil {
			panic(err)
		}
	}

	fmt.Printf("Running with PROFILE [%s]\n", profile)

	go g.StartRPCServer()
	a.StartAPI()
}
