package gateway

import (
	"fmt"
	"github.com/runopsio/hoop/gateway/analytics"
	"os"

	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/review/jit"
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

	profile := os.Getenv("PROFILE")
	idProvider := idp.NewProvider(profile)
	analyticsService := analytics.New()
	analytics.InitSentry()

	transport.LoadPlugins(idProvider.ApiURL)

	agentService := agent.Service{Storage: &agent.Storage{Storage: s}}
	pluginService := plugin.Service{Storage: &plugin.Storage{Storage: s}}
	connectionService := connection.Service{PluginService: &pluginService, Storage: &connection.Storage{Storage: s}}
	userService := user.Service{Storage: &user.Storage{Storage: s}}
	clientService := client.Service{Storage: &client.Storage{Storage: s}}
	sessionService := session.Service{Storage: &session.Storage{Storage: s}}
	reviewService := review.Service{Storage: &review.Storage{Storage: s}}
	jitService := jit.Service{Storage: &jit.Storage{Storage: s}}
	notificationService := notification.NewMagicBell()
	securityService := security.Service{
		Storage:     &security.Storage{Storage: s},
		Provider:    idProvider,
		UserService: &userService,
		Analytics:   analyticsService}

	a := &api.Api{
		AgentHandler:      agent.Handler{Service: &agentService},
		ConnectionHandler: connection.Handler{Service: &connectionService},
		UserHandler:       user.Handler{Service: &userService, Analytics: analyticsService},
		PluginHandler:     plugin.Handler{Service: &pluginService},
		SessionHandler:    session.Handler{Service: &sessionService},
		ReviewHandler:     review.Handler{Service: &reviewService},
		JitHandler:        jit.Handler{Service: &jitService},
		SecurityHandler:   security.Handler{Service: &securityService},
		IDProvider:        idProvider,
		Profile:           profile,
		Analytics:         analyticsService,
	}

	g := &transport.Server{
		AgentService:         agentService,
		ConnectionService:    connectionService,
		UserService:          userService,
		ClientService:        clientService,
		PluginService:        pluginService,
		SessionService:       sessionService,
		ReviewService:        reviewService,
		JitService:           jitService,
		NotificationService:  notificationService,
		IDProvider:           idProvider,
		Profile:              profile,
		GcpDLPRawCredentials: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON"),
		PluginRegistryURL:    os.Getenv("PLUGIN_REGISTRY_URL"),
		Analytics:            analyticsService,
	}
	reviewService.TransportService = g
	jitService.TransportService = g

	if profile == pb.DevProfile {
		if err := a.CreateTrialEntities(); err != nil {
			panic(err)
		}
	}

	fmt.Printf("Running with PROFILE [%s]\n", profile)

	go g.StartRPCServer()
	a.StartAPI()
}
