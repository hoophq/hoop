package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/api"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/jobs"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/runbooks"
	"github.com/runopsio/hoop/gateway/security"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"
	xtdb "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/user"

	// plugins

	"github.com/runopsio/hoop/gateway/transport/adminapi"
	pluginsrbac "github.com/runopsio/hoop/gateway/transport/plugins/accesscontrol"
	pluginsaudit "github.com/runopsio/hoop/gateway/transport/plugins/audit"
	pluginsdcm "github.com/runopsio/hoop/gateway/transport/plugins/dcm"
	pluginsdlp "github.com/runopsio/hoop/gateway/transport/plugins/dlp"
	pluginsindex "github.com/runopsio/hoop/gateway/transport/plugins/index"
	pluginsreview "github.com/runopsio/hoop/gateway/transport/plugins/review"
	pluginsslack "github.com/runopsio/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
)

func Run(listenAdmAddr string) {
	ver := version.Get()
	log.Infof("version=%s, compiler=%s, go=%s, platform=%s, commit=%s, multitenant=%v, build-date=%s",
		ver.Version, ver.Compiler, ver.GoVersion, ver.Platform, ver.GitCommit, user.IsOrgMultiTenant(), ver.BuildDate)

	apiURL := os.Getenv("API_URL")
	if err := changeWebappApiURL(apiURL); err != nil {
		log.Fatal(err)
	}
	defer log.Sync()
	s := xtdb.New()
	log.Infof("syncing xtdb at %s", s.Address())
	if err := s.Sync(time.Second * 80); err != nil {
		log.Fatal(err)
	}
	log.Infof("sync with success")
	if !strings.HasPrefix(apiURL, "https://") {
		log.Warn("THE API_URL ENV IS CONFIGURED USING AN INSECURE SCHEME (HTTP)!")
	}

	storev2 := storagev2.NewStorage(nil)

	profile := os.Getenv("PROFILE")
	idProvider := idp.NewProvider(profile)
	analyticsService := analytics.New()

	grpcURL := os.Getenv("GRPC_URL")
	if grpcURL == "" {
		u, err := url.Parse(idProvider.ApiURL)
		if err != nil {
			log.Fatalf("failed parsing API_URL, reason=%v", err)
		}
		grpcURL = fmt.Sprintf("%s://%s:8443", u.Scheme, u.Hostname())
	}
	if !strings.HasPrefix(grpcURL, "https://") {
		log.Warn("THE GRPC_URL ENV IS CONFIGURED USING AN INSECURE SCHEME (HTTP)!")
	}

	agentService := agent.Service{Storage: &agent.Storage{Storage: s}}
	connectionService := connection.Service{Storage: &connection.Storage{Storage: s}}
	userService := user.Service{Storage: &user.Storage{Storage: s}}
	sessionService := session.Service{Storage: &session.Storage{Storage: s}}
	reviewService := review.Service{Storage: &review.Storage{Storage: s}}
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
		SessionHandler:    session.Handler{ApiURL: apiURL, Service: &sessionService, ConnectionService: &connectionService},
		IndexerHandler:    indexer.Handler{},
		ReviewHandler:     review.Handler{Service: &reviewService},
		SecurityHandler:   security.Handler{Service: &securityService},
		RunbooksHandler:   runbooks.Handler{ConnectionService: &connectionService},
		IDProvider:        idProvider,
		Profile:           profile,
		Analytics:         analyticsService,
		GrpcURL:           grpcURL,

		StoreV2: storev2,
	}

	g := &transport.Server{
		AgentService:         agentService,
		ConnectionService:    connectionService,
		UserService:          userService,
		SessionService:       sessionService,
		ReviewService:        reviewService,
		NotificationService:  notificationService,
		IDProvider:           idProvider,
		Profile:              profile,
		GcpDLPRawCredentials: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON"),
		PluginRegistryURL:    os.Getenv("PLUGIN_REGISTRY_URL"),
		PyroscopeIngestURL:   os.Getenv("PYROSCOPE_INGEST_URL"),
		PyroscopeAuthToken:   os.Getenv("PYROSCOPE_AUTH_TOKEN"),
		AgentSentryDSN:       os.Getenv("AGENT_SENTRY_DSN"),
		Analytics:            analyticsService,

		StoreV2: storev2,
	}
	// order matters
	g.RegisteredPlugins = []plugintypes.Plugin{
		pluginsreview.New(
			&review.Service{Storage: &review.Storage{Storage: s}, TransportService: g},
			&user.Service{Storage: &user.Storage{Storage: s}},
			notificationService,
			idProvider.ApiURL,
		),
		pluginsaudit.New(),
		pluginsindex.New(&session.Storage{Storage: s}),
		pluginsdlp.New(),
		pluginsrbac.New(),
		pluginsslack.New(
			&review.Service{Storage: &review.Storage{Storage: s}, TransportService: g},
			&user.Service{Storage: &user.Storage{Storage: s}},
			idProvider),
		pluginsdcm.New(),
	}
	plugintypes.RegisteredPlugins = g.RegisteredPlugins

	for _, p := range g.RegisteredPlugins {
		pluginContext := plugintypes.Context{}
		switch p.Name() {
		case plugintypes.PluginAuditName:
			pluginContext.ParamsData = map[string]any{pluginsaudit.StorageWriterParam: sessionService.Storage.NewGenericStorageWriter()}
		}
		if err := p.OnStartup(pluginContext); err != nil {
			log.Fatalf("failed initializing plugin %s, reason=%v", p.Name(), err)
		}
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
			log.Fatal(err)
		}
	}
	if grpc.ShouldDebugGrpc() {
		log.SetGrpcLogger()
	}

	log.Infof("profile=%v - starting servers", profile)
	go g.StartRPCServer()
	go adminapi.RunServer(listenAdmAddr)
	go func() {
		mainFilePath := "/app/api/main.js"
		if _, err := os.Stat(mainFilePath); err != nil && os.IsNotExist(err) {
			return
		}
		log.Infof("starting node api process ...")
		cmd := exec.Command("node", mainFilePath)
		cmd.Env = os.Environ()
		// https://expressjs.com/en/advanced/best-practice-performance.html#set-node_env-to-production
		cmd.Env = append(cmd.Env, "NODE_ENV", "production")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Errorf("failed running node api process, err=%v", err)
			return
		}
		log.Infof("node api process finished")
	}()
	a.StartAPI(sentryStarted)
}

func changeWebappApiURL(apiURL string) error {
	if apiURL != "" {
		staticUiPath := os.Getenv("STATIC_UI_PATH")
		if staticUiPath == "" {
			staticUiPath = "/app/ui/public"
		}
		appJsFile := filepath.Join(staticUiPath, "js/app.js")
		appBytes, err := os.ReadFile(appJsFile)
		if err != nil {
			log.Warnf("failed opening webapp js file %v, reason=%v", appJsFile, err)
			return nil
		}
		log.Infof("replacing api url from %v with %v", appJsFile, apiURL)
		appBytes = bytes.ReplaceAll(appBytes, []byte(`http://localhost:8009`), []byte(apiURL))
		if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
			return fmt.Errorf("failed saving app.js file, reason=%v", err)
		}
	}
	return nil
}

func getNotification() notification.Service {
	if os.Getenv("NOTIFICATIONS_BRIDGE_CONFIG") != "" && os.Getenv("ORG_MULTI_TENANT") != "true" {
		mBridgeConfigRaw := []byte(os.Getenv("NOTIFICATIONS_BRIDGE_CONFIG"))
		var mBridgeConfigMap map[string]string
		if err := json.Unmarshal(mBridgeConfigRaw, &mBridgeConfigMap); err != nil {
			log.Fatalf("failed decoding notifications bridge config")
		}
		log.Printf("Bridge notifications selected")
		matterbridgeConfig := `[slack]
[slack.myslack]
Token="%s"
PreserveThreading=true

[api.myapi]
BindAddress="127.0.0.1:4242"
Buffer=10000

[[gateway]]
name="hoop-notifications-bridge"
enable=true

[[gateway.in]]
account="api.myapi"
channel="api"

[[gateway.out]]
account="slack.myslack"
channel="general"`
		matterbridgeFolder, err := clientconfig.NewHomeDir("matterbridge")
		if err != nil {
			log.Fatal(err)
		}
		configFile := filepath.Join(matterbridgeFolder, "matterbridge.toml")
		configFileBytes := []byte(fmt.Sprintf(matterbridgeConfig, mBridgeConfigMap["slackBotToken"]))
		err = os.WriteFile(configFile, configFileBytes, 0600)
		if err != nil {
			log.Fatal(err)
		}

		err = exec.Command("matterbridge", "-conf", configFile).Start()

		if err != nil {
			log.Fatal(err)
		}
		return notification.NewMatterbridge()
	} else if os.Getenv("SMTP_HOST") != "" {
		log.Printf("SMTP notifications selected")
		return notification.NewSmtpSender()
	}
	log.Printf("MagicBell notifications selected")
	return notification.NewMagicBell()
}
