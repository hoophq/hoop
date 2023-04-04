package gateway

import (
	"os"
	"time"

	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/api"
	"github.com/runopsio/hoop/gateway/client"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/jobs"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/review/jit"
	"github.com/runopsio/hoop/gateway/runbooks"
	"github.com/runopsio/hoop/gateway/security"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"
	xtdb "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/user"
)

func Run() {
	ver := version.Get()
	log.Infof("version=%s, compiler=%s, go=%s, platform=%s, commit=%s, multitenant=%v, build-date=%s",
		ver.Version, ver.Compiler, ver.GoVersion, ver.Platform, ver.GitCommit, user.IsOrgMultiTenant(), ver.BuildDate)
	defer log.Sync()
	s := xtdb.New()
	log.Infof("syncing xtdb at %s", s.Address())
	if err := s.Sync(time.Second * 60); err != nil {
		log.Fatal(err)
	}
	log.Infof("sync with success")

	profile := os.Getenv("PROFILE")
	idProvider := idp.NewProvider(profile)
	analyticsService := analytics.New()

	transport.LoadPlugins(
		&session.Storage{Storage: s},
		&plugin.Storage{Storage: s},
		idProvider.ApiURL,
	)

	agentService := agent.Service{Storage: &agent.Storage{Storage: s}}
	pluginService := plugin.Service{Storage: &plugin.Storage{Storage: s}}
	connectionService := connection.Service{PluginService: &pluginService, Storage: &connection.Storage{Storage: s}}
	userService := user.Service{Storage: &user.Storage{Storage: s}}
	clientService := client.Service{Storage: &client.Storage{Storage: s}}
	sessionService := session.Service{Storage: &session.Storage{Storage: s}}
	reviewService := review.Service{Storage: &review.Storage{Storage: s}}
	jitService := jit.Service{Storage: &jit.Storage{Storage: s}}
	notificationService := getNotification()
	securityService := security.Service{
		Storage:     &security.Storage{Storage: s},
		Provider:    idProvider,
		UserService: &userService,
		Analytics:   analyticsService}

	if !user.IsOrgMultiTenant() {
		log.Infof("provisioning / promoting default organization")
		if err := userService.CreateDefaultOrganization(); err != nil {
			log.Fatal(err)
		}
	}

	a := &api.Api{
		AgentHandler:      agent.Handler{Service: &agentService},
		ConnectionHandler: connection.Handler{Service: &connectionService},
		UserHandler:       user.Handler{Service: &userService, Analytics: analyticsService},
		PluginHandler:     plugin.Handler{Service: &pluginService},
		SessionHandler:    session.Handler{Service: &sessionService},
		IndexerHandler:    indexer.Handler{},
		ReviewHandler:     review.Handler{Service: &reviewService},
		JitHandler:        jit.Handler{Service: &jitService},
		SecurityHandler:   security.Handler{Service: &securityService},
		RunbooksHandler:   runbooks.Handler{PluginService: &pluginService, ConnectionService: &connectionService},
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
		PyroscopeIngestURL:   os.Getenv("PYROSCOPE_INGEST_URL"),
		PyroscopeAuthToken:   os.Getenv("PYROSCOPE_AUTH_TOKEN"),
		AgentSentryDSN:       os.Getenv("AGENT_SENTRY_DSN"),
		Analytics:            analyticsService,
	}
	if g.PyroscopeIngestURL != "" && g.PyroscopeAuthToken != "" {
		log.Infof("starting profiler, ingest-url=%v", g.PyroscopeIngestURL)
		_, err := monitoring.StartProfiler("gateway", monitoring.ProfilerConfig{
			PyroscopeServerAddress: g.PyroscopeIngestURL,
			PyroscopeAuthToken:     g.PyroscopeAuthToken,
			Environment:            g.IDProvider.ApiURL,
		})
		if err != nil {
			log.Fatalf("failed starting profiler, err=%v", err)
		}
	}
	sentryStarted, err := monitoring.StartSentry(nil, monitoring.SentryConfig{
		DSN:         os.Getenv("SENTRY_DSN"),
		Environment: g.IDProvider.ApiURL,
	})
	if err != nil {
		log.Fatalf("failed starting sentry, err=%v", err)
	}
	reviewService.TransportService = g
	jitService.TransportService = g

	//start scheduler for "weekly" report service (production mode)
	if profile != pb.DevProfile {
		jobs.InitReportScheduler(&jobs.Scheduler{
			UserStorage:    &userService,
			SessionStorage: &sessionService,
			Notification:   notificationService,
		})
	}

	if profile == pb.DevProfile {
		if err := a.CreateTrialEntities(); err != nil {
			panic(err)
		}
	}
	if grpc.ShouldDebugGrpc() {
		log.SetGrpcLogger()
	}

	log.Infof("profile=%v - starting servers", profile)
	go g.StartRPCServer()
	a.StartAPI(sentryStarted)
}

func getNotification() notification.Service {
	if os.Getenv("SMTP_HOST") != "" {
		return notification.NewSmtpSender()
	}
	return notification.NewMagicBell()
}
